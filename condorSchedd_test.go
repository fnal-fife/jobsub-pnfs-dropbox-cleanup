package main

import (
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
				files, err := getDropboxFilesFromJob(test.job)
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

// Tests to implement in the future
// TODO Use Testcontainers for this?
func TestGetCondorSchedds(t *testing.T) {
	t.Skip("getCondorSchedds test not implemented yet")
}

func TestGetPNFSJobsForExperiment(t *testing.T) {
	t.Skip("getPNFSJobsForExperiment test not implemented yet")
}
