package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseDateStampToTime(t *testing.T) {
	type testCase struct {
		description string
		input       string
		output      time.Time
		expectedErr error
	}

	now := time.Now()
	curYear := now.Year()

	createFutureDateStringAndCorrectedTime := func() (string, time.Time) {
		futureDate := now.AddDate(0, 1, 0)
		layout := "Jan 2 15:04"
		return futureDate.Format(layout), now.Truncate(time.Minute).AddDate(-1, 1, 0)
	}

	futureDateString, correctTime := createFutureDateStringAndCorrectedTime()

	testCases := []testCase{
		{
			"Timestamp with time, no year",
			"Sep 26 14:55",
			time.Date(curYear, 9, 26, 14, 55, 0, 0, time.Local),
			nil,
		},
		{
			"Timestamp with time, no year, make sure year gets rewound",
			futureDateString,
			correctTime,
			nil,
		},
		{
			"Timestamp with date, year",
			"Apr  6  2022",
			time.Date(2022, 4, 6, 0, 0, 0, 0, time.Local),
			nil,
		},
		{
			"malformed timestamp",
			"Apr  96  2022",
			time.Time{},
			&time.ParseError{},
		},
		{
			"malformed timestamp 2",
			"boogityboo",
			time.Time{},
			&time.ParseError{},
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				result, err := parseDateStampToTime(test.input)
				if test.expectedErr != nil {
					var err2 *time.ParseError
					assert.ErrorAs(t, err, &err2)
					return
				}
				assert.Equal(t, test.output, result)
			},
		)
	}
}

// adjustAnswerYearIfNeeded is a helper function to ensure that our tests work in the future.
// It is meant to be used with tests that try to use the "current year" by grabbing the Year()
// from time.Now() and using it in a different time.Time object.  This func can be used to wrap
// a time.Date() call to decrement the year if the generated date is in the future.  For example,
// if we have the following (written on 2023-10-08):
//
//		now := time.Now()
//	 t := time.Date(now.Year(), 11, 13, 1, 2, 3, 0, time.Local)
//
// then t is in the future.  If we instead wrap this call in adjustAnswerYearIfNeeded, it will
// ensure that both of the following return dates in the present or past
//
//	t1 := adjustAnswerYearIfNeeded(time.Date(now.Year(), 11, 13, 1, 2, 3, 0, time.Local))
//	t2 := adjustAnswerYearIfNeeded(time.Date(now.Year(), 9, 13, 1, 2, 3, 0, time.Local))
//
// As of this writing, t1 represents a time of 2022-11-13T01:23:00.0 Local Time, and
// t2 represents a time of 2023-09-13T01:23:00.0 Local Time
func adjustAnswerYearIfNeeded(t time.Time) time.Time {
	if t.After(time.Now()) {
		return t.AddDate(-1, 0, 0)
	}
	return t
}

func TestParsePermsToDirectoryFlag(t *testing.T) {
	type testCase struct {
		input       string
		isDir       bool
		expectedErr error
	}

	testCases := []testCase{
		{
			"-rwxrwxrwx",
			false,
			nil,
		},
		{
			"drwxrwxrwx",
			true,
			nil,
		},
		{
			"boogityboo",
			false,
			ErrMalformedPerms,
		},
	}

	for idx, test := range testCases {
		t.Run(
			fmt.Sprintf("Test%d", idx),
			func(t *testing.T) {
				result, err := parsePermsToDirectoryFlag(test.input)
				if test.expectedErr != nil {
					assert.ErrorIs(t, err, test.expectedErr)
					return
				}
				assert.Equal(t, test.isDir, result)
			},
		)
	}
}

func TestScanDropboxLineToFileEntry(t *testing.T) {
	type testCase struct {
		description       string
		line              string
		expectedFileEntry *FileEntry
	}

	testCases := []testCase{
		{
			"File, no year on datestamp",
			"-rwxrwxrwx   0 0     0            50 Sep 26 14:55 bogus_file.out",
			&FileEntry{
				"bogus_file.out",
				adjustAnswerYearIfNeeded(time.Date(time.Now().Year(), 9, 26, 14, 55, 0, 0, time.Local)),
				false,
			},
		},
		{
			"Directory, no year on datestamp",
			"drwxrwxrwx   0 0     0            50 Sep 26 14:55 bogus_directory",
			&FileEntry{
				"bogus_directory",
				adjustAnswerYearIfNeeded(time.Date(time.Now().Year(), 9, 26, 14, 55, 0, 0, time.Local)),
				true,
			},
		},
		{
			"Timestamp with date, year",
			"drwxrwxrwx   0 0     0             0 Apr  6  2022 bogus_dir",
			&FileEntry{
				"bogus_dir",
				adjustAnswerYearIfNeeded(time.Date(2022, 4, 6, 0, 0, 0, 0, time.Local)),
				true,
			},
		},
	}
	// Should grab file entry for each line

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				entry, _ := scanDropboxLineToFileEntry(test.line)
				assert.Equal(t, test.expectedFileEntry, entry)
			},
		)
	}
}
