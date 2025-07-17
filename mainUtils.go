package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// getConfigFilePath attempts to locate the configuration file to use for the application.
// It first checks if a specific config file path was provided via the configFileFlagVal argument.
// If provided and the file exists, it returns that path.
// If not, it iterates through the provided checkDirs slice, looking for a file named defaultConfigFileName
// in each directory. If found, it returns the path to that file.
// If the configuration file cannot be found in any of the specified locations, it returns the error errNoConfigFileFound.
func getConfigFilePath(configFileFlagVal string, checkDirs []string) (string, error) {
	// Check configFileFlag first
	if configFileFlagVal != "" {
		_, err := os.Stat(configFileFlagVal)
		if err == nil {
			// Config file exists, return it
			return configFileFlagVal, nil
		}
	}

	// Now check all of our checkDirs for the file
	for _, dir := range checkDirs {
		configFilePath := filepath.Join(dir, defaultConfigFileName)
		_, err := os.Stat(configFilePath)
		if err == nil {
			// Config file exists in this directory, return it
			slog.Info("Found config file", "configFilePath", configFilePath)
			return configFilePath, nil
		}
	}
	return "", errNoConfigFileFound
}

// checkVaultTokenFile checks if the Vault token file at the specified location exists and is not older than the given age cutoff.
// The ageCutoff parameter should be a duration string (e.g., "1h", "24h").
// If the ageCutoff cannot be parsed, a default value is used.
// Returns errNoVaultTokenFile if the file does not exist, errVaultTokenTooOld if the file is too old, or another error if an unexpected error occurs.
func checkVaultTokenFile(location, ageCutoff string) error {
	funcLogger := logger.With("caller", "checkVaultTokenFile")
	vaultTokenAgeCutoff, err := time.ParseDuration(ageCutoff)
	if err != nil {
		funcLogger.Error("error parsing vault token age cutoff duration. Using default vault token age cutoff", "error", err, "defaultVaultTokenAgeCutoff", defaultVaultTokenAgeCutoff)
		vaultTokenAgeCutoff = defaultVaultTokenAgeCutoff
	}

	// Is vault token file new enough?
	funcLogger.Debug("Ensuring vault token is available, and new enough", "vaultTokenFile", location, "vaultTokenAgeCutoff", ageCutoff)
	stat, err := os.Stat(location)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errNoVaultTokenFile
		}
		return err
	}
	if stat.ModTime().Add(vaultTokenAgeCutoff).Before(now) { // File is older than vaultTokenAgeCutoff
		return errVaultTokenTooOld
	}
	return nil
}

// getScheddFiles retrieves the list of PNFS dropbox files associated with batch jobs for a given experiment
// from a specified Condor schedd.  Returns a slice of file paths in PNFS or an error if any step fails.
func getScheddFiles(ctx context.Context, sch *condorSchedd, authMethod condorAuthMethod, experiment string) ([]string, error) {
	funcLogger := logger.With("caller", "getScheddFiles")
	scheddFiles := make([]string, 0)
	sch.cmdEnv = os.Environ()

	funcLogger.Debug("Verifying authentication to condor schedd", "schedd", sch.name, "authMethod", authMethod)
	err := sch.verify(ctx, authMethod)
	if err != nil {
		return nil, fmt.Errorf("error verifying auth to condor schedd: %w", err)
	}

	funcLogger.Debug("Getting PNFS jobs for experiment", "experiment", experiment, "schedd", sch.name)
	ads, err := sch.getPNFSJobsForExperiment(ctx, k.String("experiment"), k.String("condor.jobConstraint"))
	if err != nil {
		return nil, fmt.Errorf("error getting PNFS jobs for experiment %s: %w", experiment, err)
	}

	for _, ad := range ads {
		files, err := getDropboxFilesFromJob(ad)
		if err != nil {
			funcLogger.Error("error getting dropbox files from job:", "error", err, "schedd", sch.name, "jobId", fmt.Sprintf("%s.%s", ad["ClusterId"], ad["ProcId"])) // TODO Get job ID?
			continue
		}
		funcLogger.Debug("Got schedd dropbox files", "schedd", sch.name, "files", files)
		scheddFiles = append(scheddFiles, files...)
	}

	return scheddFiles, nil
}

func stripPNFSFromPath(pnfsPath string) string {
	return strings.TrimPrefix(pnfsPath, "/pnfs")
}

// PNFSToHTTPS converts a given PNFS file path to an HTTPS URL using the specified host and port.
// The function applies a filename transformation function to the PNFS path before joining it to the base URL.
// If the provided urlHostPort is invalid, it logs an error and returns an empty string.
func PNFSToHTTPS(pnfsPath string, urlHostPort string, filenameTransformFunc func(string) string) string {
	u, err := url.Parse(urlHostPort)
	if err != nil {
		slog.Error("error parsing URL", "error", err)
		return ""
	}
	return u.JoinPath(filenameTransformFunc(pnfsPath)).String()
}

func userConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		slog.Error("error getting user config directory", "error", err)
		return ""
	}
	return dir
}

var (
	errNoConfigFileFound = errors.New("no config file found in any of the specified directories")
	errNoVaultTokenFile  = fmt.Errorf("could not find vault token file at configured location: %w", fs.ErrNotExist)
	errVaultTokenTooOld  = errors.New("vault token file is older than the time cutoff")
)
