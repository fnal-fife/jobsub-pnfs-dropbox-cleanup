package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetConfigFilePath(t *testing.T) {
	// Create a config file in tmpDir
	tmpDir := t.TempDir()
	configFilePath := filepath.Join(tmpDir, defaultConfigFileName)
	if err := os.WriteFile(configFilePath, []byte("test config"), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	emptyDir := t.TempDir()
	otherDir := t.TempDir()
	otherConfigFilePath := filepath.Join(otherDir, defaultConfigFileName)
	if err := os.WriteFile(otherConfigFilePath, []byte("other config"), 0644); err != nil {
		t.Fatalf("failed to create other config file: %v", err)
	}

	type testCase struct {
		description  string
		flagVal      string
		dirs         []string
		expectedPath string
		expectedErr  error
		setup        func()
	}

	tests := []testCase{
		{
			description:  "flag points to valid file",
			flagVal:      configFilePath,
			dirs:         []string{tmpDir},
			expectedPath: configFilePath,
			expectedErr:  nil,
		},
		{
			description:  "flag empty, config file exists in dirs",
			flagVal:      "",
			dirs:         []string{tmpDir},
			expectedPath: configFilePath,
			expectedErr:  nil,
		},
		{
			description:  "flag empty, no config file in dirs",
			flagVal:      "",
			dirs:         []string{emptyDir},
			expectedPath: "",
			expectedErr:  errNoConfigFileFound,
		},
		{
			description:  "flag points to non-existent file, config file exists in dirs",
			flagVal:      filepath.Join(tmpDir, "doesnotexist"),
			dirs:         []string{tmpDir},
			expectedPath: configFilePath,
			expectedErr:  nil,
		},
		{
			description:  "flag points to non-existent file, no config file in dirs",
			flagVal:      filepath.Join(emptyDir, "doesnotexist"),
			dirs:         []string{emptyDir},
			expectedPath: "",
			expectedErr:  errNoConfigFileFound,
		},
		{
			description:  "multiple dirs, config file in second dir",
			flagVal:      "",
			dirs:         []string{emptyDir, otherDir},
			expectedPath: otherConfigFilePath,
			expectedErr:  nil,
		},
		{
			description:  "multiple dirs, config file in first dir",
			flagVal:      "",
			dirs:         []string{tmpDir, otherDir},
			expectedPath: configFilePath,
			expectedErr:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			got, err := getConfigFilePath(tt.flagVal, tt.dirs)
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			}
			assert.Equal(t, tt.expectedPath, got)
		})
	}
}

func TestCheckVaultTokenFile(t *testing.T) {
	// Helper function to create a token file with a specific modification time
	tokenSetup := func(t *testing.T, modTimeOffset time.Duration) (tokenfile string) {
		tmpDir := t.TempDir()
		tokenFile := filepath.Join(tmpDir, "vault.token")
		if err := os.WriteFile(tokenFile, []byte("token"), 0600); err != nil {
			t.Fatalf("failed to create token file: %v", err)
		}
		modTime := now.Add(modTimeOffset)
		if err := os.Chtimes(tokenFile, modTime, modTime); err != nil {
			t.Fatalf("failed to set mod time: %v", err)
		}
		return tokenFile
	}

	type testCase struct {
		description     string
		modTimeOffset   time.Duration // Offset from now for mod time
		createTokenFile bool
		ageCutoff       string
		expectedErr     error
	}

	now = time.Now() // override global now for deterministic tests

	tests := []testCase{
		{
			description: "file does not exist",
			ageCutoff:   "1h",
			expectedErr: errNoVaultTokenFile,
		},
		{
			description:     "file exists and is new enough",
			createTokenFile: true,
			modTimeOffset:   -30 * time.Minute,
			ageCutoff:       "1h",
			expectedErr:     nil,
		},
		{
			description:     "file exists and is too old",
			createTokenFile: true,
			modTimeOffset:   -2 * time.Hour,
			ageCutoff:       "1h",
			expectedErr:     errVaultTokenTooOld,
		},
		{
			description:     "invalid ageCutoff uses default, file is new enough",
			createTokenFile: true,
			modTimeOffset:   time.Duration(math.Round(-0.5 * float64(defaultVaultTokenAgeCutoff))), // Half of the default age cutoff
			ageCutoff:       "notaduration",
			expectedErr:     nil,
		},
		{
			description:     "invalid ageCutoff uses default, file is too old",
			createTokenFile: true,
			modTimeOffset:   -2 * defaultVaultTokenAgeCutoff, // Twice the default age cutoff
			ageCutoff:       "notaduration",
			expectedErr:     errVaultTokenTooOld,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			tokenFile := "vault.token"
			if tt.createTokenFile {
				tokenFile = tokenSetup(t, tt.modTimeOffset)
			}

			err := checkVaultTokenFile(tokenFile, tt.ageCutoff)
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			}
		})
	}
}

func TestGetScheddFiles(t *testing.T) {
	experiment := "testexperiment"
	testSchedd := &condorSchedd{
		name: "test-schedd",
	}

	type testCase struct {
		description         string
		authSetupFunc       func(*testing.T) // Mock our auth method
		condorQMockScript   string           // Mock the condor_q command that will be used by getScheddFiles
		expectedFiles       []string
		expectedErrContains string // Error message substring to check
	}

	testCases := []testCase{
		{
			description: "auth fails",
			authSetupFunc: func(t *testing.T) {
				t.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", "IDTOKENS")

				// Simulate a home dir
				homedir := t.TempDir()
				t.Setenv("HOME", homedir)
			},
			expectedFiles:       nil,
			expectedErrContains: "error verifying auth to condor schedd",
		},
		{
			description:         "schedd query fails",
			authSetupFunc:       fakeGoodIDTokenAuthSetup,
			condorQMockScript:   filepath.Join("internal", "testscripts", "exit1"),
			expectedFiles:       nil,
			expectedErrContains: "error getting PNFS jobs for experiment " + experiment,
		},
		{
			description:         "schedd query succeeds",
			authSetupFunc:       fakeGoodIDTokenAuthSetup,
			condorQMockScript:   filepath.Join("internal", "testscripts", "condor_q-mock-ads"),
			expectedFiles:       []string{"/pnfs/testexperiment/resilient/jobsub_stage/file1", "/pnfs/testexperiment/resilient/jobsub_stage/file2", "/pnfs/testexperiment/resilient/jobsub_stage/file3", "/pnfs/testexperiment/resilient/jobsub_stage/file1b", "/pnfs/testexperiment/resilient/jobsub_stage/file2b", "/pnfs/testexperiment/resilient/jobsub_stage/file3b"},
			expectedErrContains: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			if tc.authSetupFunc != nil {
				tc.authSetupFunc(t)
			}

			if tc.condorQMockScript != "" {
				cleanupFunc := useFakeExecutable(t, "condor_q", tc.condorQMockScript)
				defer cleanupFunc() // Ensure we clean up the mock command
			}

			// Run the test
			files, err := getScheddFiles(context.Background(), testSchedd, IDTOKENS, experiment)
			if err != nil {
				assert.Contains(t, err.Error(), tc.expectedErrContains)
			}
			assert.ElementsMatch(t, tc.expectedFiles, files)
		})
	}
}
