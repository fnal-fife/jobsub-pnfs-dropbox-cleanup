package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testJobLister struct {
	queryError bool
	jobs       []map[string][]byte
	fileErrors map[string]bool
	files      []testFileString
}

func (jl *testJobLister) queryJobsList([]string, []string) ([]map[string][]byte, error) {
	if jl.queryError {
		return nil, errors.New("this is an error")
	}
	return jl.jobs, nil
}

func (jl *testJobLister) getDropboxFilesFromJob(j map[string]io.Reader) ([]string, error) {
	attr := "FILE_ATTRIBUTE"
	if val, ok := j[attr]; ok {
		b := new(strings.Builder)
		io.Copy(b, val)
		if isErr, ok := jl.fileErrors[b.String()]; ok {
			if isErr {
				return nil, errors.New("error!")
			}
		}
		return strings.Split(b.String(), ","), nil
	}
	return nil, errors.New("key missing")
}

func TestGetActiveFiles(t *testing.T) {
	type testCase struct {
		description   string
		jobLister     JobLister
		attributes    []string
		expectedFiles []string
		shouldError   bool
	}

	testCases := []testCase{
		{
			"Good JobLister",
			newTestJobLister(false, testFileString{"/path/to/file1", false}, testFileString{"/path/to/file2", false}),
			nil,
			[]string{"/path/to/file1", "/path/to/file2"},
			false,
		},
		{
			"Empty Good JobLister",
			newTestJobLister(false),
			nil,
			[]string{},
			false,
		},
		{
			"Good JobLister, multiple files per job",
			newTestJobLister(false, testFileString{"/path/to/file1,/path/to/file3", false}, testFileString{"/path/to/file2", false}),
			nil,
			[]string{"/path/to/file1", "/path/to/file3", "/path/to/file2"},
			false,
		},
		{
			"Bad JobLister, bad query, single file",
			newTestJobLister(true, testFileString{"/path/to/file2", false}),
			nil,
			[]string{},
			true,
		},
		{
			"Bad JobLister, bad files extract, single file",
			newTestJobLister(false, testFileString{"/path/to/file2", true}),
			nil,
			[]string{},
			false,
		},
		{
			"Bad JobLister, bad files extract, one good file, one bad",
			newTestJobLister(false, testFileString{"/path/to/file2", true}, testFileString{"/path/to/file1,blahblah", false}),
			nil,
			[]string{"/path/to/file1", "blahblah"},
			false,
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				files, err := GetActiveFiles(test.jobLister, test.attributes, []string{})
				if test.shouldError {
					assert.Error(t, err)
				}
				assert.Equal(t, test.expectedFiles, files)
			},
		)
	}
}
