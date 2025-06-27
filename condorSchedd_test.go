package main

import (
	"os"
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

// TODO Use Testcontainers for this?
func TestGetCondorSchedds(t *testing.T) {}

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
				if !ok {
					t.Errorf("Environment variable _condor_SEC_CLIENT_AUTHENTICATION_METHODS should be set")
				}
				// Assert that the environment variable is set to "IDTOKENS"
				if value != "IDTOKENS" {
					t.Errorf("Expected _condor_SEC_CLIENT_AUTHENTICATION_METHODS to be 'IDTOKENS', got '%s'", value)
				}
				// Call the cleanup function
				cleanup()
				if !test.cleanupCheck() {
					t.Errorf("Cleanup function did not restore the environment correctly")
				}
			},
		)
	}

}
