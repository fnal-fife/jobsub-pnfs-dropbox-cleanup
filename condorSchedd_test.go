package main

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/retzkek/htcondor-go/classad"
	"github.com/stretchr/testify/assert"
)

func TestCondorScheddGetDropboxFilesFromJob(t *testing.T) {
	type testCase struct {
		description   string
		job           map[string]classad.Attribute
		expectedFiles []string
		expectedErr   error
	}

	testCases := []testCase{
		{
			"One file",
			classad.MapStringStringToClassAd(map[string]string{"PNFS_INPUT_FILES": "/path/to/myfile"}),
			[]string{"/path/to/myfile"},
			nil,
		},
		{
			"Two files",
			classad.MapStringStringToClassAd(map[string]string{"PNFS_INPUT_FILES": "/path/to/myfile,/path/to/myfile2"}),
			[]string{"/path/to/myfile", "/path/to/myfile2"},
			nil,
		},
		{
			"Three files, comma-space",
			classad.MapStringStringToClassAd(map[string]string{"PNFS_INPUT_FILES": "/path/to/myfile,/path/to/myfile2, /path/to/myfile3"}),
			[]string{"/path/to/myfile", "/path/to/myfile2", "/path/to/myfile3"},
			nil,
		},
		{
			"Missing key in job",
			classad.MapStringStringToClassAd(map[string]string{"PNFS_INPUT_FILES_WRONG": "/path/to/myfile,/path/to/myfile2, /path/to/myfile3"}),
			nil,
			errMissingJobDropboxFiles,
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				mySchedd := new(condorSchedd)
				files, err := mySchedd.getDropboxFilesFromJob(test.job)
				assert.ErrorIs(t, err, test.expectedErr)
				assert.Equal(t, test.expectedFiles, files)
			},
		)
	}
}

func TestBuildConstraint(t *testing.T) {
	experiment := "test"
	experimentPortion := "Jobsub_Group==\"" + experiment + "\""
	type testCase struct {
		description string
		constraint  string
		expected    string
	}

	testCases := []testCase{
		{
			"Empty constraint",
			"",
			experimentPortion,
		},
		{
			"Simple constraint",
			"!IsUndefined(PNFS_INPUT_FILES)",
			experimentPortion + " && (!IsUndefined(PNFS_INPUT_FILES))",
		},
		{
			"Multiple constraints",
			"Name == \"schedd1\" || Arch == \"x86_64\"",
			experimentPortion + " && (Name == \"schedd1\" || Arch == \"x86_64\")",
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				assert.Equal(t, test.expected, buildConstraint(experiment, test.constraint))
			},
		)
	}
}

func TestSetupIDTOKENEnvironment(t *testing.T) {
	type testCase struct {
		description  string
		oldValue     string
		cleanupCheck func() bool
	}

	testCases := []testCase{
		{
			"_condor_SEC_CLIENT_AUTHENTICATION_METHODS unset",
			"",
			func() bool {
				_, ok := os.LookupEnv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS")
				return !ok
			},
		},
		{
			"_condor_SEC_CLIENT_AUTHENTICATION_METHODS set",
			"some_value",
			func() bool {
				val, ok := os.LookupEnv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS")
				return ok && val == "some_value"
			},
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				// Set the environment variable to an empty string
				t.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", test.oldValue)

				// Call the function to set up the environment
				cleanup := setupIDTOKENEnvironment()

				// Check if the environment variable is set correctly
				value, ok := os.LookupEnv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS")
				assert.True(t, ok, "_condor_SEC_CLIENT_AUTHENTICATION_METHODS should be set")
				assert.Equal(t, "IDTOKENS", value, "_condor_SEC_CLIENT_AUTHENTICATION_METHODS should be set to 'IDTOKENS'")

				// Call the cleanup function
				cleanup()
				assert.True(t, test.cleanupCheck(), "Cleanup function should restore the environment correctly")
			},
		)
	}
}

// Assumes condor_config_val is available in the PATH
func TestIdTokenAuth(t *testing.T) {
	ctx := context.Background()
	c := &condorSchedd{name: "test_schedd"}

	type testCase struct {
		description string
		setup       func(t *testing.T)
		expectedErr error
	}

	testCases := []testCase{
		{
			"IDTOKENS not set as auth method",
			func(t *testing.T) {
				t.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", "SOME_OTHER_METHOD")
			},
			errUnsupportedCondorAuthMethod,
		},
		{
			"IDTOKENS set as auth method, directory doesn't exist",
			func(t *testing.T) {
				t.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", "IDTOKENS")

				// Simulate a home dir
				homedir := t.TempDir()
				t.Setenv("HOME", homedir)

			},
			fs.ErrNotExist,
		},
		{
			"IDTOKENS set as auth method, directory not readable",
			func(t *testing.T) {
				t.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", "IDTOKENS")

				// Simulate a home dir
				homedir := t.TempDir()
				os.MkdirAll(filepath.Join(homedir, ".condor", "tokens.d"), 0o000) // No permissions
				t.Setenv("HOME", homedir)

			},
			fs.ErrPermission,
		},
		{
			"IDTOKENS set as auth method, directory readable, only dirs inside",
			func(t *testing.T) {
				t.Setenv("_condor_SEC_CLIENT_AUTHENTICATION_METHODS", "IDTOKENS")

				// Simulate a home dir
				homedir := t.TempDir()
				os.MkdirAll(filepath.Join(homedir, ".condor", "tokens.d", "dir"), 0o755)
				t.Setenv("HOME", homedir)

			},
			errNoIDTokensFound,
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				test.setup(t)
				assert.ErrorIs(t, idTokenAuth(ctx, c), test.expectedErr)
			},
		)
	}

}

func TestCheckForClientAuthMethod(t *testing.T) {
	// Save and restore exeMap after test
	origExeMap := make(map[string]string)
	maps.Copy(origExeMap, exeMap)
	defer func() {
		// Restore exeMap to its original state
		maps.Copy(exeMap, origExeMap)
	}()

	// Create a temporary fake condor_config_val executable
	tmpDir := t.TempDir()
	fakeCondorConfigVal := filepath.Join(tmpDir, "condor_config_val")

	// Helper to write a fake condor_config_val script that we'll feed test values into
	writeFakeCondorConfigVal := func(output string, exitCode int) {
		script := "#!/bin/sh\n"
		if output != "" {
			script += "echo \"" + output + "\"\n"
		}
		script += fmt.Sprintf("exit %d\n", exitCode)
		if err := os.WriteFile(fakeCondorConfigVal, []byte(script), 0o755); err != nil {
			t.Fatalf("failed to write fake condor_config_val: %v", err)
		}
	}

	ctx := context.Background()
	exeMap["condor_config_val"] = fakeCondorConfigVal

	tests := []struct {
		name     string
		output   string
		exitCode int
		method   condorAuthMethod
		want     bool
	}{
		{
			name:     "Supported method present",
			output:   "FS,IDTOKENS,SCITOKENS",
			exitCode: 0,
			method:   IDTOKENS,
			want:     true,
		},
		{
			name:     "Supported method present with spaces",
			output:   "FS, IDTOKENS , SCITOKENS",
			exitCode: 0,
			method:   SCITOKENS,
			want:     true,
		},
		{
			name:     "Supported method not present",
			output:   "FS",
			exitCode: 0,
			method:   IDTOKENS,
			want:     false,
		},
		{
			name:     "Empty output",
			output:   "",
			exitCode: 0,
			method:   FS,
			want:     false,
		},
		{
			name:     "Command fails",
			output:   "",
			exitCode: 1,
			method:   FS,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeFakeCondorConfigVal(tt.output, tt.exitCode)
			got := checkForClientAuthMethod(ctx, tt.method)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Tests to implement in the future
// TODO Use Testcontainers for this?
func TestGetCondorSchedds(t *testing.T) {
	t.Skip("getCondorSchedds test not implemented yet")
}

func TestGetPNFSJobsForExperiment(t *testing.T) {
	t.Skip("getPNFSJobsForExperiment test not implemented yet")
}
