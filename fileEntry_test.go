package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TODO add tests for fileIsRecent with different ageCutoff values
func TestFileIsRecent(t *testing.T) {
	type testCase struct {
		description string
		ageCutoff   time.Duration // This is not used in the test, but should be part of the test case
		f           *FileEntry
		isRecent    bool
	}

	now := time.Now()
	recentDate := now.AddDate(0, 0, -7)
	oldDate := now.AddDate(0, -2, 0)
	reallyOldDate := now.AddDate(-2, 0, 0)

	testCases := []testCase{
		{
			"Recent file",
			0,
			&FileEntry{
				"/path/to/recent_file.txt",
				recentDate,
				false,
				nil,
				nil,
			},
			true,
		},
		{
			"old file",
			0,
			&FileEntry{
				"/path/to/old_file.txt",
				oldDate,
				false,
				nil,
				nil,
			},
			false,
		},
		{
			"reallyOld file",
			0,
			&FileEntry{
				"/path/to/reallyOld_file.txt",
				reallyOldDate,
				false,
				nil,
				nil,
			},
			false,
		},
		{
			"file with different ageCutoff",
			time.Duration(3 * 24 * 365 * time.Hour), // 3 years
			&FileEntry{
				"/path/to/reallyOld_file.txt",
				reallyOldDate,
				false,
				nil,
				nil,
			},
			true, // Our file IS really old, but the age cutoff is 3 years.
		},
		{
			"file with negative ageCutoff - should use default",
			-1,
			&FileEntry{
				"/path/to/reallyOld_file.txt",
				reallyOldDate,
				false,
				nil,
				nil,
			},
			false,
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				assert.Equal(t, test.isRecent, fileIsRecent(test.f, test.ageCutoff)) // TODO This should come from test case
			},
		)
	}
}
