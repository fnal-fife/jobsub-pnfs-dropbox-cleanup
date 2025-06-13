package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strings"

	condor "github.com/retzkek/htcondor-go"
	classad "github.com/retzkek/htcondor-go/classad"
)

func getCondorSchedds(ctx context.Context, constraint string) ([]*condorSchedd, error) {
	cmd := condor.NewCommand(***REMOVED***)
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

type condorAuthMethod int

const (
	FS condorAuthMethod = iota
	IDTOKENS
	SCITOKENS
	UNSUPPORTED
)

func (a condorAuthMethod) String() string {
	switch a {
	case FS:
		return "FS"
	case IDTOKENS:
		return "IDTOKENS"
	case SCITOKENS:
		return "SCITOKENS"
	default:
		return "UNKNOWN"
	}
}

func newCondorAuthMethod(s string) condorAuthMethod {
	switch strings.ToUpper(s) {
	case FS.String():
		return FS
	case IDTOKENS.String():
		return IDTOKENS
	case SCITOKENS.String():
		return SCITOKENS
	default:
		slog.Error("Unsupported condor authentication method", "method", s)
		return UNSUPPORTED
	}
}

func (a condorAuthMethod) verify(ctx context.Context, c *condorSchedd) error {
	switch a {
	case FS:
		return nil // No-op for FS, since it doesn't require any special handling
	case IDTOKENS:
		return idTokenAuth(ctx, c)
	case SCITOKENS:
		return sciTokenAuth(ctx, c)
	default:
		msg := fmt.Sprintf("Unsupported condor authentication method: %s", a.String())
		slog.Error(msg)
		return errors.New(msg)
	}
}

// setupEnv sets up the environment for the specified condor authentication method.
// It returns a cleanup function that restores the old environment after execution.
func (a condorAuthMethod) setupEnv() (cleanupFunc func()) {
	switch a {
	case FS:
		return func() {} // No-op for FS, since it doesn't require any special handling
	case IDTOKENS:
		return setupIDTOKENEnvironment()
	case SCITOKENS:
		return func() {
			// No specific environment setup needed for SCITOKENS
		}
	default:
		slog.Error("Unsupported condor authentication method", "method", a.String())
		return func() {}
	}
}

// Set environment so we can use IDTOKENS for authentication.  Returns a function to restore the old environment after execution
func setupIDTOKENEnvironment() (cleanupFunc func()) {
	var oldSECClientAuthenticationMethods string
	val, ok := os.LookupEnv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS")
	if ok {
		oldSECClientAuthenticationMethods = val
	}

	err := os.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", "IDTOKENS")
	if err != nil {
		slog.Error("error setting environment variable for condor authentication methods", "error", err)
		return func() {}
	}

	return func() {
		if oldSECClientAuthenticationMethods != "" {
			os.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", oldSECClientAuthenticationMethods)
			slog.Debug("Restored old _condor_SEC_CLIENT_AUTHENTICATION_METHODS env var", "methods", oldSECClientAuthenticationMethods)
			return
		}
		os.Unsetenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS")
		slog.Debug("Unset _condor_SEC_CLIENT_AUTHENTICATION_METHODS environment variable")
	}
}

// Function that identifies and sets necessary items for authn/authz to the condor schedd
type condorAuth func(context.Context, *condorSchedd) error

// TODO fake auth that gets us through testing.
func sciTokenAuth(ctx context.Context, c *condorSchedd) error {
	if !checkForClientAuthMethod(ctx, SCITOKENS) {
		msg := fmt.Sprintf("%s authentication method not supported by condor client", SCITOKENS.String())
		slog.Error(msg)
		return errors.New(msg)
	}

	// TODO implement bearer token discovery
	// Check for scitoken in the standard location
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

// idTokenAuth checks the standard location ~/.condor/tokens.d for the presence of an IDTOKEN file it can stat.
// It does not check the contents of the file, just that it exists and is readable.
func idTokenAuth(ctx context.Context, c *condorSchedd) error {
	// Do we support IDTOKEN auth?
	// TODO Can this be done with the condor library?
	// checkCmd := condor.NewCommand("/usr/bin/condor_config_val").WithArg("SEC_CLIENT_AUTHENTICATION_METHODS")
	// slog.Debug("Running command", "command", append([]string{checkCmd.Command}, checkCmd.MakeArgs()...))
	authMethod := IDTOKENS
	if !checkForClientAuthMethod(ctx, authMethod) {
		msg := fmt.Sprintf("%s authentication method not supported by condor client", authMethod.String())
		slog.Error(msg)
		return errors.New(msg)
	}

	// Does at least one IDTOKEN file exist in the expected location?
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("error getting current user's home dir", "error", err)
		return err
	}
	expectedPath := filepath.Join(homeDir, ".condor", "tokens.d")
	files, err := os.ReadDir(expectedPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Error("IDTOKEN directory does not exist - should be at ~/.condor/tokens.d", "directory", expectedPath)
			return err
		}
		slog.Error("error reading IDTOKEN directory", "error", err)
		return err
	}

	for _, file := range files {
		_, err := os.Stat(filepath.Join(expectedPath, file.Name()))
		if err != nil {
			slog.Error("error getting file information about IDTOKEN file", "error", err)
			return err
		}
		if !file.IsDir() {
			slog.Debug("Verified that tokens directory is non-empty. Proceeding with IDTOKEN auth")
			break
		}
	}
	c.authMethod = append(c.authMethod, IDTOKENS)
	return nil
}

func checkForClientAuthMethod(ctx context.Context, m condorAuthMethod) bool {
	checkCmd := exec.CommandContext(ctx, "/usr/bin/condor_config_val", "SEC_CLIENT_AUTHENTICATION_METHODS")
	slog.Debug("Running command", "command", checkCmd.String())
	stdoutStderr, err := checkCmd.CombinedOutput()
	if err != nil {
		slog.Error("error getting condor config value", "error", err)
		return false
	}
	if len(stdoutStderr) == 0 {
		slog.Error("no condor config value returned")
		return false
	}
	methods := strings.Split(string(stdoutStderr), ",")
	for _, ad := range methods {
		if strings.TrimSpace(ad) == m.String() {
			slog.Debug("Requested method found in supported auth methods", "method", m.String())
			return true
		}
	}
	msg := fmt.Sprintf("%s is not a supported authentication method", m.String())
	slog.Error(msg)
	return false
}

// END TODO

type condorSchedd struct {
	name       string
	cmdEnv     []string // place to store things like BEARER_TOKEN_FILE for condor commands
	authMethod []condorAuthMethod
}

func (c *condorSchedd) verify(ctx context.Context, a condorAuthMethod) error {
	return a.verify(ctx, c)
}

func (c *condorSchedd) getPNFSJobsForExperiment(ctx context.Context, experiment string, constraint string) ([]classad.ClassAd, error) {
	useConstraint := buildConstraint(experiment, constraint)
	slog.Debug("Final job constraint", "constraint", useConstraint)

	condorCmd := condor.NewCommand("/usr/bin/condor_q").WithName(c.name).WithConstraint(useConstraint)
	slog.Debug("Running command", "command", append([]string{condorCmd.Command}, condorCmd.MakeArgs()...))
	// ads, err := cmd.RunWithContext(ctx)
	cmd := condorCmd.CmdContext(ctx)
	cmd.Env = append(os.Environ(), c.cmdEnv...) // TODO Make this configurable
	out, err := cmd.Output()
	if err != nil {
		// Handle Error
		msg := "error querying condorSchedd for PNFS-using jobs"
		slog.Error(msg, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	ads, err := classad.ReadClassAds(bytes.NewReader(out))
	if err != nil {
		slog.Error("error reading classads from condor_q output", "error", err)
		return nil, fmt.Errorf("error reading classads: %w", err)
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

func buildConstraint(experiment string, constraint string) string {
	exptConstraint := "Jobsub_Group==\"" + experiment + "\""
	if constraint == "" {
		return exptConstraint
	}
	return exptConstraint + " && (" + constraint + ")"
}

var (
	errMissingJobDropboxFiles        = errors.New("required job attribute is missing to get job dropbox files")
	errNoConfiguredClientAuthMethods = errors.New("no configured client authentication methods")
)
