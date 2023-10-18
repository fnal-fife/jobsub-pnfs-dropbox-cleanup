package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFileIsRecent(t *testing.T) {
	type testCase struct {
		description string
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
			&FileEntry{
				"/path/to/recent_file.txt",
				recentDate,
				false,
			},
			true,
		},
		{
			"old file",
			&FileEntry{
				"/path/to/old_file.txt",
				oldDate,
				false,
			},
			false,
		},
		{
			"reallyOld file",
			&FileEntry{
				"/path/to/reallyOld_file.txt",
				reallyOldDate,
				false,
			},
			false,
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				assert.Equal(t, test.isRecent, fileIsRecent(test.f))
			},
		)
	}
}
