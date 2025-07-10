package main

import (
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

func checkVaultTokenFile(location, ageCutoff string) error {
	// vaultTokenAgeCutoff, err := time.ParseDuration(k.String("vault.vaultTokenAgeCutoff"))
	funcLogger := logger.With("caller", "checkVaultTokenFile")
	vaultTokenAgeCutoff, err := time.ParseDuration(ageCutoff)
	if err != nil {
		funcLogger.Error("error parsing vault token age cutoff duration. Using default vault token age cutoff", "error", err, "defaultVaultTokenAgeCutoff", defaultVaultTokenAgeCutoff)
		vaultTokenAgeCutoff = defaultVaultTokenAgeCutoff
	}

	// Is vault token file new enough?
	// runLogger.Debug("Ensuring vault token is available, and new enough", "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", vaultTokenAgeCutoff)
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

func stripPNFSFromPath(pnfsPath string) string {
	return strings.TrimPrefix(pnfsPath, "/pnfs")
}

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
