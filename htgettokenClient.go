package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/lestrrat-go/jwx/jwt"
	scitokens "github.com/scitokens/scitokens-go"
)

var htgettokenExecutable string

func init() {
	var err error
	if htgettokenExecutable, err = exec.LookPath("htgettoken"); err != nil {
		msg := "htgettoken executable not found in PATH"
		slog.Error(msg, "error", err)
		panic(msg)
	}
}

type htgettokenClient struct {
	vaultServer      string
	vaultTokenInFile string
	outFile          string
	options          []string
}

// newHtgettokenClient creates a new htgettokenClient instance. It will check that vaultTokenInFile exists and is readable.
// outFile and options are optional - if not provided, they will be set to default values. If
// options are provided, they will be passed to the HTGETTOKENOPTS environment variable.
func newHtgettokenClient(vaultServer, vaultTokenInFile, outFile string, options ...string) *htgettokenClient {
	if vaultTokenInFile != "" {
		_, err := os.Stat(vaultTokenInFile)
		if err != nil {
			if os.IsNotExist(err) {
				slog.Error("vault token file does not exist", "file", vaultTokenInFile)
				return nil
			}
			slog.Error("error getting file information about vault token file", "error", err)
			return nil
		}
	}

	return &htgettokenClient{
		vaultServer:      vaultServer,
		vaultTokenInFile: vaultTokenInFile,
		outFile:          outFile,
		// Options to pass to the HTGETTOKENOPTS environment variable
		options: options,
	}
}

// getToken runs htgettoken to obtain a SciToken from the token issuer
func (h *htgettokenClient) getToken(ctx context.Context, issuer, role string) ([]byte, error) {
	opts := strings.Join(
		mergeHtgettokenopts(os.Environ(), prepareHtgettokenopts(h.options)),
		" ",
	)
	envString := "HTGETTOKENOPTS=" + opts

	cmdArgs := []string{
		"-a",
		h.vaultServer,
		"-i",
		issuer,
		"--vaulttokeninfile",
		h.vaultTokenInFile,
		"-o",
		h.outFile,
	}

	if role != "" {
		cmdArgs = append(cmdArgs, "--role", role)
	}

	cmd := exec.CommandContext(ctx, htgettokenExecutable, cmdArgs...)
	cmd.Env = append(cmd.Env, envString)
	err := cmd.Run()
	if err != nil {
		slog.Error("error running htgettoken to obtain bearer token", "error", err)
		slog.Debug("htgettoken command", "command", cmd.String())
		return nil, fmt.Errorf("error running htgettoken: %w", err)
	}

	// We have a token now in outFile, so read it in, validate it as a SciToken, and return it
	tok, err := os.ReadFile(h.outFile)
	if err != nil {
		slog.Error("error reading token outFile", "outfile", h.outFile, "error", err)
		return nil, fmt.Errorf("error validating token: %w", err)
	}

	// Parse the token to verify that it's a valid JWT
	jt, err := jwt.Parse(tok)
	if err != nil {
		slog.Error("error parsing token", "tokenfile", h.outFile, "error", err)
		return nil, fmt.Errorf("error ingesting token outFile to SciToken: %w", err)
	}

	// Convert our token to a SciToken
	st, err := scitokens.NewSciToken(jt)
	if err != nil {
		slog.Error("error creating SciToken from token", "tokenfile", h.outFile, "error", err)
		return nil, fmt.Errorf("error validating token: %w", err)
	}

	enf, err := scitokens.NewEnforcer(st.Issuer())
	if err != nil {
		slog.Error("error creating SciToken from token", "tokenfile", h.outFile, "error", err)
		return nil, fmt.Errorf("error validating token: %w", err)
	}

	// Validate the token
	err = enf.Validate(st)
	if err != nil {
		slog.Error("error validating token", "tokenfile", h.outFile, "error", err)
		return nil, fmt.Errorf("error validating token: %w", err)
	}

	return tok, nil
}

func prepareHtgettokenopts(options []string) []string {
	finalOpts := make([]string, 0)

	// Preprocess args to remove extra spaces and correct malformed strings, like
	// "--option value", " -o " or "-o   value "
	useOpts := make([]string, 0)
	for _, opt := range options {
		if strings.Contains(opt, " ") {
			for _, useOpt := range strings.Split(opt, " ") {
				use := strings.TrimSpace(useOpt) // If we had extra spaces, use = ""
				if use == "" {
					continue
				}
				useOpts = append(useOpts, use)
			}
			continue
		}
		useOpts = append(useOpts, strings.TrimSpace(opt))
	}

	lenOpts := len(useOpts)
	for i := 0; i < lenOpts; i++ {
		switch {
		// Case 1. "--option=value" or "-option=value"
		case strings.HasPrefix(useOpts[i], "-") && strings.Contains(useOpts[i], "="):
			finalOpts = append(finalOpts, useOpts[i]) // Use as is, but with trimmed spaces

		// Case 2. "-option/--option" as in -option value, or --option value, or just --option
		case strings.HasPrefix(useOpts[i], "-") && !strings.Contains(useOpts[i], "="):
			finalOpt := useOpts[i] // e.g. --option
			// Look ahead to see if the next option is a value
			if i+1 < lenOpts {
				// If the next option is a value, add it to the current option
				if !strings.HasPrefix(useOpts[i+1], "-") {
					finalOpt += "=" + useOpts[i+1]
					i++
				}
			}
			finalOpts = append(finalOpts, finalOpt)
		}
	}
	return finalOpts
}

func mergeHtgettokenopts(env []string, options []string) []string {
	dropEmptyStringFromSlice := func(s []string) []string {
		return slices.DeleteFunc(s, func(s2 string) bool { return s2 == "" })
	}

	// If HTGETTOKENOPTS is set in the environment, we need to merge it with options so that options take precedence
	if !slices.ContainsFunc(env, func(s string) bool {
		return strings.HasPrefix(s, "HTGETTOKENOPTS=")
	}) {
		// No previous HTGETTOKENOPTS, so we can just return the options
		return options
	}
	// Note: The above check does mean an extra iteration if HTGETTOKENOPTS is set in the environment, but benchmarking showed
	// a negligible difference in performance whether we do this check or not;  so it was kept in for readability and clarity.

	retOpts := dropEmptyStringFromSlice(prepareHtgettokenopts(options)) // Start with options, since those take precedence
	optsMap := make(map[string]struct{})                                // For faster lookup
	for _, opt := range retOpts {
		optKey := strings.SplitN(opt, "=", 2)[0] // Get the option key, e.g. "--option" or "-o"
		optsMap[optKey] = struct{}{}
	}

	for _, envOpt := range env {
		if !strings.HasPrefix(envOpt, "HTGETTOKENOPTS=") {
			continue
		}

		// HTGETTOKENOPTS=\"--option1=value1 --option2=value2 --option3 --option4 value4\"
		optVals := strings.TrimPrefix(envOpt, "HTGETTOKENOPTS=") // \"--option1=value1 --option2=value2 --option3 --option4 value4"\
		_htEnvOpts := strings.Split(optVals, " ")                // []string{"\"--option1=value1", "--option2=value2", "--option3", "--option4", "value4\""}
		htEnvOpts := make([]string, 0)
		for _, opt := range _htEnvOpts {
			_opt := strings.Trim(opt, "\"") // Remove any surrounding escaped quotes
			if _opt != "" {
				htEnvOpts = append(htEnvOpts, _opt) // Add only non-empty options
			}
		} // []string{"--option1=value1", "--option2=value2", "--option3", "--option4", "value4"}

		processedhtEnvOpts := dropEmptyStringFromSlice(prepareHtgettokenopts(htEnvOpts)) // []string{"--option1=value1", "--option2=value2", "--option3", "--option4=value4"}
		for _, prOpt := range processedhtEnvOpts {
			htOptKey := strings.SplitN(prOpt, "=", 2)[0] // Get the option key, e.g. "--option" or "-o" to look up
			if _, ok := optsMap[htOptKey]; !ok {
				retOpts = append(retOpts, prOpt)
			}
		}
	}
	return retOpts
}
