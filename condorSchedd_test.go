package main

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCondorScheddGetDropboxFilesFromJob(t *testing.T) {
	type testCase struct {
		description   string
		job           map[string]io.Reader
		expectedFiles []string
		expectedErr   error
	}

	testCases := []testCase{
		{
			"One file",
			map[string]io.Reader{"PNFS_INPUT_FILES": strings.NewReader("/path/to/myfile")},
			[]string{"/path/to/myfile"},
			nil,
		},
		{
			"Two files",
			map[string]io.Reader{"PNFS_INPUT_FILES": strings.NewReader("/path/to/myfile,/path/to/myfile2")},
			[]string{"/path/to/myfile", "/path/to/myfile2"},
			nil,
		},
		{
			"Three files, comma-space",
			map[string]io.Reader{"PNFS_INPUT_FILES": strings.NewReader("/path/to/myfile,/path/to/myfile2, /path/to/myfile3")},
			[]string{"/path/to/myfile", "/path/to/myfile2", "/path/to/myfile3"},
			nil,
		},
		{
			"Missing key in job",
			map[string]io.Reader{"PNFS_INPUT_FILES_WRONG": strings.NewReader("/path/to/myfile,/path/to/myfile2, /path/to/myfile3")},
			nil,
			ErrMissingJobDropboxFiles,
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				mySchedd := new(CondorSchedd)
				files, err := mySchedd.getDropboxFilesFromJob(test.job)
				assert.ErrorIs(t, err, test.expectedErr)
				assert.Equal(t, test.expectedFiles, files)
			},
		)
	}
}
