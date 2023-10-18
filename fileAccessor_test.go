package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTestFileAccessor(files []FileEntry, existsFileListingError bool, errorsByFileEntry []bool) *testFileAccessor {
	return &testFileAccessor{
		fileEntries:            files,
		existsFileListingError: existsFileListingError,
		errorsByFileEntry:      errorsByFileEntry,
	}
}

type testFileAccessor struct {
	fileEntries            []FileEntry
	existsFileListingError bool
	errorsByFileEntry      []bool
}

func (t *testFileAccessor) getFilesList(source string) ([][]byte, error) {
	if t.existsFileListingError {
		return nil, errors.New("some generic file listing error")
	}
	returnSlice := make([][]byte, 0, len(t.fileEntries))
	for _, entry := range t.fileEntries {
		returnSlice = append(returnSlice, []byte(entry.filename))
	}
	return returnSlice, nil
}

func (t *testFileAccessor) fileListingToFileEntry(r io.Reader) (FileEntry, error) {
	var b strings.Builder
	io.Copy(&b, r)
	filename := b.String()
	for idx, entry := range t.fileEntries {
		if entry.filename == filename {
			if t.errorsByFileEntry[idx] {
				return FileEntry{}, errors.New("Fake error that we staged")
			}
			return entry, nil
		}
	}
	return FileEntry{}, errors.New("File not found in testFileAccessor")
}

func TestGetDropboxFiles(t *testing.T) {
	type testCase struct {
		description string
		FileAccessor
		expectedFiles    []FileEntry
		expectedErrorNil bool
	}

	testCases := []testCase{
		{
			"Mix of files and dirs, no errors",
			newTestFileAccessor(
				[]FileEntry{
					{
						"/path/to/foo",
						time.Date(2023, 4, 5, 6, 54, 32, 0, time.Local),
						false,
					},
					{"/path/to/bardir",
						time.Date(2023, 1, 2, 3, 45, 6, 0, time.Local),
						true,
					},
					{
						"/more/sub/dir/paths/to/baz",
						time.Date(2023, 5, 6, 7, 12, 34, 0, time.Local),
						false,
					},
				},
				false,
				[]bool{false, false, false},
			),
			[]FileEntry{
				{
					"/path/to/foo",
					time.Date(2023, 4, 5, 6, 54, 32, 0, time.Local),
					false,
				},
				{"/path/to/bardir",
					time.Date(2023, 1, 2, 3, 45, 6, 0, time.Local),
					true,
				},
				{
					"/more/sub/dir/paths/to/baz",
					time.Date(2023, 5, 6, 7, 12, 34, 0, time.Local),
					false,
				},
			},
			true,
		},
		{
			"Mix of files and dirs, listing error",
			newTestFileAccessor(
				[]FileEntry{
					{
						"/path/to/foo",
						time.Date(2023, 4, 5, 6, 54, 32, 0, time.Local),
						false,
					},
					{"/path/to/bardir",
						time.Date(2023, 1, 2, 3, 45, 6, 0, time.Local),
						true,
					},
					{
						"/more/sub/dir/paths/to/baz",
						time.Date(2023, 5, 6, 7, 12, 34, 0, time.Local),
						false,
					},
				},
				true,
				[]bool{false, true, false},
			),
			nil,
			false,
		},
		{
			"Mix of files and dirs, lines-to-fileEntry errors in some cases",
			newTestFileAccessor(
				[]FileEntry{
					{
						"/path/to/foo",
						time.Date(2023, 4, 5, 6, 54, 32, 0, time.Local),
						false,
					},
					{"/path/to/bardir",
						time.Date(2023, 1, 2, 3, 45, 6, 0, time.Local),
						true,
					},
					{
						"/more/sub/dir/paths/to/baz",
						time.Date(2023, 5, 6, 7, 12, 34, 0, time.Local),
						false,
					},
				},
				false,
				[]bool{false, true, false},
			),
			[]FileEntry{
				{
					"/path/to/foo",
					time.Date(2023, 4, 5, 6, 54, 32, 0, time.Local),
					false,
				},
				{
					"/more/sub/dir/paths/to/baz",
					time.Date(2023, 5, 6, 7, 12, 34, 0, time.Local),
					false,
				},
			},
			true,
		},
		{
			"Mix of files and dirs, lines-to-fileEntry errors in all cases",
			newTestFileAccessor(
				[]FileEntry{
					{
						"/path/to/foo",
						time.Date(2023, 4, 5, 6, 54, 32, 0, time.Local),
						false,
					},
					{"/path/to/bardir",
						time.Date(2023, 1, 2, 3, 45, 6, 0, time.Local),
						true,
					},
					{
						"/more/sub/dir/paths/to/baz",
						time.Date(2023, 5, 6, 7, 12, 34, 0, time.Local),
						false,
					},
				},
				false,
				[]bool{true, true, true},
			),
			nil,
			false,
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				files, err := GetDropboxFiles(test.FileAccessor, "")
				if !test.expectedErrorNil {
					assert.Error(t, err)
				}
				assert.Equal(t, test.expectedFiles, files)
			},
		)
	}
}
