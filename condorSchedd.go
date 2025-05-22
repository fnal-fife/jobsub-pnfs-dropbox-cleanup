package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"os/user"
	"path"
	"strings"

	condor "github.com/retzkek/htcondor-go"
	classad "github.com/retzkek/htcondor-go/classad"
)

func getCondorSchedds(ctx context.Context, constraint string) ([]*condorSchedd, error) {
	cmd := condor.NewCommand("/usr/bin/condor_status").WithPool("gpcollector04.fnal.gov").WithConstraint(constraint).WithArg("-schedd")
	slog.Debug("Running command", "command", append([]string{cmd.Command}, cmd.MakeArgs()...))
	ads, err := cmd.RunWithContext(ctx)
	if err != nil {
		slog.Error("error querying condor collector for schedds", "error", err)
		return nil, err
	}
	schedds := make([]*condorSchedd, 0, len(ads))
	for _, ad := range ads {
		name, ok := ad["Name"]
		if !ok {
			slog.Error("Name not found in schedd ad", "ad", ad)
			continue
		}
		schedd := &condorSchedd{
			name: name.String(),
		}
		schedds = append(schedds, schedd)
	}
	return schedds, nil
}

// Function that identifies and sets necessary items for authn/authz to the condor schedd
type condorAuth func(context.Context, *condorSchedd) error

// TODO fake auth that gets us through testing.
func fakeCondorAuth(ctx context.Context, c *condorSchedd) error {
	// TODO This is just for now - we're using sbhat's scitoken to authenticate to the schedd for development.  Remove this later
	user, err := user.Current()
	if err != nil {
		slog.Error("error getting current user", "error", err)
		return err
	}
	tokenFile := path.Join("/", "run", "user", user.Uid, "bt_u"+user.Uid)
	cmd := exec.CommandContext(ctx, "/usr/bin/httokendecode", tokenFile)
	if err := cmd.Run(); err != nil {
		slog.Error("error running httokendecode", "error", err, "command", cmd.String())
		return err
	}
	c.cmdEnv = append(c.cmdEnv, "BEARER_TOKEN_FILE="+tokenFile)
	return nil
}

// END TODO

type condorSchedd struct {
	name   string
	cmdEnv []string // place to store things like BEARER_TOKEN_FILE for condor commands
}

func (c *condorSchedd) verify(ctx context.Context, cFunc condorAuth) error {
	// TODO write a condorAuth func that ensures that IDToken exists at ~/.condor/tokens.d
	return cFunc(ctx, c)
}

func (c *condorSchedd) getPNFSJobsForExperiment(ctx context.Context, experiment string) ([]classad.ClassAd, error) {
	// TODO Should be configured
	constraint := "Jobsub_Group=='" + experiment + "'" + " && !IsUndefined(PNFS_INPUT_FILES)"

	cmd := condor.NewCommand("/usr/bin/condor_q").WithName(c.name).WithConstraint(constraint)
	slog.Debug("Running command", "command", append([]string{cmd.Command}, cmd.MakeArgs()...))
	ads, err := cmd.RunWithContext(ctx)
	if err != nil {
		// Handle Error
		msg := "error querying condorSchedd for PNFS-using jobs"
		slog.Error(msg, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
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

var errMissingJobDropboxFiles = errors.New("required job attribute is missing to get job dropbox files")
