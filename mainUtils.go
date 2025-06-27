package main

import (
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

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

var (
	errNoConfigFileFound = errors.New("no config file found in any of the specified directories")
)
