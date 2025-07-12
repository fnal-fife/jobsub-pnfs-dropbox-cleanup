package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	condor "github.com/retzkek/htcondor-go"
	classad "github.com/retzkek/htcondor-go/classad"
)

// Metrics
var (
	getScheddsDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "get_schedds_duration_seconds",
		Help:      "The amount of time it took to query the condor collector for schedds",
	})
	condorScheddVerifyDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "condor_schedd_verify_duration_seconds",
		Help:      "The amount of time it took to verify schedd authentication",
	},
		[]string{"auth_method"},
	)
	getPNFSJobsForExperimentDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "get_pnfs_jobs_for_experiment_duration_seconds",
		Help:      "The amount of time it took to get PNFS jobs on the schedd for the experiment",
	},
		[]string{"schedd", "experiment"},
	)
)

func init() {
	// Check for all required executables

	// We handle this differently than the other clients because a lot of the time, the jobsub_lite RPM may be installed on the same
	// machine that is running this code.  The jobsub_lite RPM installs its own versions of the condor executables, and puts them
	// in front of the default condor executables in the PATH. So we need to check for the existence of the executables in the default
	// locations that condor installs the executables at first, and then fall back to looking in the PATH.
	requiredExecutablesDefault := map[string]string{
		"condor_status":     "/usr/bin/condor_status",
		"condor_config_val": "/usr/bin/condor_config_val",
		"condor_q":          "/usr/bin/condor_q",
		"httokendecode":     "",
	}

	for exe, defaultPath := range requiredExecutablesDefault {
		if _, ok := exeMap[exe]; ok {
			continue // Already found this executable
		}

		// Make sure default locations of required executables exist
		if defaultPath != "" {
			_, err := os.Stat(defaultPath)
			if err == nil {
				exeMap[exe] = defaultPath
				continue // Found the executable at the default path
			}
		}

		// We either don't have a default path or it doesn't exist, so look in PATH
		p, err := exec.LookPath(exe)
		if err != nil {
			panic(fmt.Sprintf("Required executable %s not found in PATH", exe))
		}
		exeMap[exe] = p
	}
	slog.Info("Found all required executables for condor operations")

	// Register the metrics
	metricsRegistry.MustRegister(getScheddsDuration)
	metricsRegistry.MustRegister(condorScheddVerifyDuration)
	metricsRegistry.MustRegister(getPNFSJobsForExperimentDuration)
	slog.Debug("Registered metrics for condor schedds operations")
}

func getCondorSchedds(ctx context.Context, pool, constraint string) ([]*condorSchedd, error) {
	funcLogger := logger.With("caller", "getCondorSchedds")
	start := time.Now()
	cmd := condor.NewCommand(exeMap["condor_status"]).WithPool(pool).WithConstraint(constraint).WithArg("-schedd")
	funcLogger.Debug("Running command", "command", append([]string{cmd.Command}, cmd.MakeArgs()...))
	ads, err := cmd.RunWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("error querying condor collector for schedds: %w", err)
	}
	schedds := make([]*condorSchedd, 0, len(ads))
	for _, ad := range ads {
		name, ok := ad["Name"]
		if !ok {
			funcLogger.Error("Name not found in schedd ad", "ad", ad)
			continue
		}
		schedd := &condorSchedd{
			name: name.String(),
		}
		schedds = append(schedds, schedd)
	}

	getScheddsDuration.Set(time.Since(start).Seconds())
	return schedds, nil
}

type condorSchedd struct {
	name       string
	cmdEnv     []string // place to store things like BEARER_TOKEN_FILE for condor commands
	authMethod []condorAuthMethod
}

func (c *condorSchedd) verify(ctx context.Context, a condorAuthMethod) error {
	return a.verify(ctx, c)
}

func (c *condorSchedd) getPNFSJobsForExperiment(ctx context.Context, experiment string, constraint string) ([]classad.ClassAd, error) {
	start := time.Now()
	funcLogger := logger.With("caller", "getPNFSJobsForExperiment")
	useConstraint := buildConstraint(experiment, constraint)
	funcLogger.Debug("Final job constraint", "constraint", useConstraint)

	condorCmd := condor.NewCommand(exeMap["condor_q"]).WithName(c.name).WithConstraint(useConstraint)
	funcLogger.Debug("Running command", "command", append([]string{condorCmd.Command}, condorCmd.MakeArgs()...))
	cmd := condorCmd.CmdContext(ctx)
	cmd.Env = append(os.Environ(), c.cmdEnv...)
	out, err := cmd.Output()
	if err != nil {
		// Handle Error
		return nil, fmt.Errorf("error querying condorSchedd for PNFS-using jobs: %w", err)
	}
	ads, err := classad.ReadClassAds(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("error reading classads: %w", err)
	}

	getPNFSJobsForExperimentDuration.WithLabelValues(c.name, experiment).Set(time.Since(start).Seconds())
	return ads, nil
}

func (c *condorSchedd) getDropboxFilesFromJob(jobAd classad.ClassAd) ([]string, error) {
	stringAd := jobAd.Strings()

	attribute := "PNFS_INPUT_FILES"
	val, ok := stringAd[attribute]
	if !ok {
		return nil, errMissingJobDropboxFiles
	}

	rawSlice := strings.Split(val, ",")
	finalSlice := make([]string, 0, len(rawSlice))

	for _, elt := range rawSlice {
		finalSlice = append(finalSlice, strings.TrimSpace(elt))
	}
	return finalSlice, nil

}

func buildConstraint(experiment string, constraint string) string {
	exptConstraint := "Jobsub_Group==\"" + experiment + "\""
	if constraint == "" {
		return exptConstraint
	}
	return exptConstraint + " && (" + constraint + ")"
}

var (
	errMissingJobDropboxFiles      = errors.New("required job attribute is missing to get job dropbox files")
	errUnsupportedCondorAuthMethod = errors.New("unsupported condor authentication method")
	errNoIDTokensFound             = errors.New("no IDTOKEN files found in expected location")
)
