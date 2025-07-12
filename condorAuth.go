package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"time"
)

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
	funcLogger := logger.With("caller", "setupIDTOKENEnvironment")
	var oldSECClientAuthenticationMethods string
	val, ok := os.LookupEnv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS")
	if ok {
		oldSECClientAuthenticationMethods = val
	}

	err := os.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", "IDTOKENS")
	if err != nil {
		funcLogger.Error("error setting environment variable for condor authentication methods", "error", err)
		return func() {}
	}

	return func() {
		if oldSECClientAuthenticationMethods != "" {
			os.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", oldSECClientAuthenticationMethods)
			funcLogger.Debug("Restored old _condor_SEC_CLIENT_AUTHENTICATION_METHODS env var", "methods", oldSECClientAuthenticationMethods)
			return
		}
		os.Unsetenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS")
		funcLogger.Debug("Unset _condor_SEC_CLIENT_AUTHENTICATION_METHODS environment variable")
	}
}

// sciTokenAuth checks if the standard location for a scitoken has a valid scitoken. If so, it will
// set the environment variable BEARER_TOKEN_FILE to that path
func sciTokenAuth(ctx context.Context, c *condorSchedd) error {
	start := time.Now()
	funcLogger := logger.With("caller", "sciTokenAuth")
	if !checkForClientAuthMethod(ctx, SCITOKENS) {
		return errUnsupportedCondorAuthMethod
	}

	// TODO next version implement bearer token discovery, or use it from scitokens-go
	// Check for scitoken in the standard location
	user, err := user.Current()
	if err != nil {
		return fmt.Errorf("error getting current user: %w", err)
	}

	tokenFile := path.Join("/", "run", "user", user.Uid, "bt_u"+user.Uid)
	funcLogger.Debug("Checking for scitoken file", "tokenFile", tokenFile)
	cmd := exec.CommandContext(ctx, exeMap["httokendecode"], tokenFile)
	if err := cmd.Run(); err != nil {
		funcLogger.Error("error running httokendecode", "error", err, "command", cmd.String())
		return fmt.Errorf("error checking scitoken file: %w", err)
	}
	c.cmdEnv = append(c.cmdEnv, "BEARER_TOKEN_FILE="+tokenFile)
	condorScheddVerifyDuration.WithLabelValues(SCITOKENS.String()).Set(time.Since(start).Seconds())
	return nil
}

// idTokenAuth checks the standard location ~/.condor/tokens.d for the presence of an IDTOKEN file it can stat.
// It does not check the contents of the file, just that it exists and is readable.
func idTokenAuth(ctx context.Context, c *condorSchedd) error {
	// Do we support IDTOKEN auth?
	// TODO Can this be done with the condor library?  Not yet
	// checkCmd := condor.NewCommand("/usr/bin/condor_config_val").WithArg("SEC_CLIENT_AUTHENTICATION_METHODS")
	// slog.Debug("Running command", "command", append([]string{checkCmd.Command}, checkCmd.MakeArgs()...))
	start := time.Now()
	funcLogger := logger.With("caller", "idTokenAuth")
	if !checkForClientAuthMethod(ctx, IDTOKENS) {
		return errUnsupportedCondorAuthMethod
	}

	// Does at least one IDTOKEN file exist in the expected location?
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("error getting current user's home dir: %w", err)
	}

	funcLogger.Debug("Checking for IDTOKEN files in expected location ~/.condor/tokens.d")
	expectedPath := filepath.Join(homeDir, ".condor", "tokens.d")
	files, err := os.ReadDir(expectedPath)
	if err != nil {
		msg := "Could not find IDTOKEN in expected location"
		if errors.Is(err, fs.ErrNotExist) {
			funcLogger.Error("IDTOKEN directory does not exist - should be at ~/.condor/tokens.d", "directory", expectedPath)
			return fmt.Errorf("%s: %w", msg, err)
		}
		funcLogger.Error("error reading IDTOKEN directory", "error", err)
		return fmt.Errorf("%s: %w", msg, err)
	}

	foundNonDirFile := false
	for _, file := range files {
		_, err := os.Stat(filepath.Join(expectedPath, file.Name()))
		if err != nil {
			funcLogger.Error("error getting file information about IDTOKEN file", "error", err)
			continue
		}
		if !file.IsDir() {
			funcLogger.Debug("Verified that tokens directory is non-empty. Proceeding with IDTOKEN auth")
			foundNonDirFile = true
			break
		}
	}
	if !foundNonDirFile {
		return errNoIDTokensFound
	}

	c.authMethod = append(c.authMethod, IDTOKENS)

	condorScheddVerifyDuration.WithLabelValues(IDTOKENS.String()).Set(time.Since(start).Seconds())
	return nil
}

func checkForClientAuthMethod(ctx context.Context, m condorAuthMethod) bool {
	funcLogger := logger.With("caller", "checkForClientAuthMethod")
	checkCmd := exec.CommandContext(ctx, exeMap["condor_config_val"], "SEC_CLIENT_AUTHENTICATION_METHODS")
	funcLogger.Debug("Running command", "command", checkCmd.String())
	stdoutStderr, err := checkCmd.CombinedOutput()
	if err != nil {
		funcLogger.Error("error getting condor config value", "error", err)
		return false
	}
	if len(stdoutStderr) == 0 {
		funcLogger.Error("no condor config value returned")
		return false
	}
	methods := strings.Split(string(stdoutStderr), ",")
	for _, ad := range methods {
		if strings.TrimSpace(ad) == m.String() {
			funcLogger.Debug("Requested method found in supported auth methods", "method", m.String())
			return true
		}
	}
	msg := fmt.Sprintf("%s is not a supported authentication method", m.String())
	funcLogger.Error(msg)
	return false
}
