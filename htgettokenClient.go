package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/lestrrat-go/jwx/jwt"
	scitokens "github.com/scitokens/scitokens-go"
)

type htgettokenClient struct {
	vaultServer      string
	vaultTokenInFile string
	outFile          string
	options          []string
}

func newHtgettokenClient(vaultServer, vaultTokenInFile, outFile string, options ...string) *htgettokenClient {
	// TODO Check to see if vaultTokenInFile is a valid file
	if vaultTokenInFile != "" {
		_, err := os.Stat(defaultVaultTokenFile)
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

// TODO Implement this properly with a real htgettoken call
func (h *htgettokenClient) getToken(ctx context.Context, issuer, role string) ([]byte, error) {
	opts := prepareHtgettokenopts(h.options)
	envString := "HTGETTOKENOPTS=" + opts

	cmdArgs := []string{
		"-a",
		h.vaultServer,
		"-i",
		issuer,
		"-r",
		role,
		"--vaulttokeninfile",
		h.vaultTokenInFile,
		"-o",
		h.outFile,
	}
	cmd := exec.CommandContext(ctx, "htgettoken", cmdArgs...)
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

func prepareHtgettokenopts(options []string) string {
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
	return strings.Join(finalOpts, " ")
}
