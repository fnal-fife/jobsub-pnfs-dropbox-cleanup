package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDcacheClientRemoveFile(t *testing.T) {
	defaultContext := context.Background()
	type testCase struct {
		description         string
		ctx                 context.Context
		urlPath             string
		assertNoError       bool
		expectedErrContains string
	}

	testCases := []testCase{
		{
			description:         "Nil context",
			ctx:                 nil,
			urlPath:             "",
			expectedErrContains: "error creating HTTP request to delete file",
		},
		{
			description:         "Invalid host in URL",
			ctx:                 defaultContext,
			urlPath:             "http://localhost:98765/file",
			expectedErrContains: "error sending HTTP request to delete file",
		},
		{
			description:         "Invalid file path",
			ctx:                 defaultContext,
			urlPath:             "http://localhost:8080/file/doesnt/exist",
			expectedErrContains: "failed to delete file",
		},
		{
			description:   "Delete file successfully",
			ctx:           defaultContext,
			urlPath:       "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/file1",
			assertNoError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel() // Run tests in parallel
			client := newDCacheClient("test-token", "", -1, 0, 0, true)
			err := client.removeFile(tc.ctx, tc.urlPath)

			if tc.assertNoError {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.expectedErrContains)
		})
	}
}

// Rewrite these tests:
// 2. Test function on request.  Nil request = errNilRequest; good request = bearer
func TestDCacheClientSetTokenAuth(t *testing.T) {
	type testCase struct {
		description                 string
		token                       string
		isRequestNil                bool
		expectedErr                 error
		expectedErrFromReturnedFunc error
		expectedHeader              string
	}

	testCases := []testCase{
		{
			description: "No token provided",
			token:       "",
			expectedErr: errNoTokenProvided,
		},
		{
			description:                 "Token provided, nil request",
			token:                       "12345",
			isRequestNil:                true,
			expectedErr:                 nil,
			expectedErrFromReturnedFunc: errNilRequest,
		},
		{
			description:                 "Token provided",
			token:                       "12345",
			isRequestNil:                false,
			expectedErr:                 nil,
			expectedErrFromReturnedFunc: nil,
			expectedHeader:              "Bearer 12345",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel()
			client := &dCacheClient{
				client: &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{
							InsecureSkipVerify: true,
						},
					},
				},
				token: strings.TrimSpace(tc.token),
			}

			// If we get an error from setTokenAuth, client.authFunc should also be nil
			err := client.setTokenAuth()
			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
				assert.Nil(t, client.authFunc)
				return
			}

			// Check client.authFunc for the right behavior
			var req *http.Request
			if !tc.isRequestNil {
				req, _ = http.NewRequest(http.MethodGet, "http://example.com", nil)
			}
			err = client.authFunc(req)
			if tc.expectedErrFromReturnedFunc != nil {
				assert.ErrorIs(t, err, tc.expectedErrFromReturnedFunc)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedHeader, req.Header.Get("Authorization"))
		})
	}

}

// TODO Make these tests table-driven
func TestDCacheClientGetFilesList(t *testing.T) {
	// Notes:
	// don't need environment, since we have headers
	// need some kind of struct to hold data, unmarshal it to fileEntry struct

	// Cases:
	// 1a. Context checking
	func() {
		d := newDCacheClient("faketoken", "", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel the context to simulate a timeout or cancellation
		expectedErr := context.Canceled
		expectedEntries := []*FileEntry{}
		source := ""
		dirContents := []*FileEntry{}
		parent := &FileEntry{}
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ErrorIs(t, err, expectedErr)
		assert.ElementsMatch(t, entries, expectedEntries)
	}()

	// 2a. Run against invalid URL - can't create request.
	func() {
		d := newDCacheClient("faketoken", "", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedErrContains := "error creating HTTP request to get files list"
		expectedEntries := []*FileEntry{}
		source := "\x00invalid-url"
		dirContents := []*FileEntry{}
		parent := &FileEntry{}
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ErrorContains(t, err, expectedErrContains)
		assert.ElementsMatch(t, entries, expectedEntries)

	}()

	// 2b. Run against invalid host .
	// 2c. Run against invalid URL - get 404, handle it.
	func() {
		d := newDCacheClient("faketoken", "", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedErrContains := "error sending HTTP request to get files"
		expectedEntries := []*FileEntry{}
		source := "http://localhost:98765"
		dirContents := []*FileEntry{}
		parent := &FileEntry{}
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ErrorContains(t, err, expectedErrContains)
		assert.ElementsMatch(t, entries, expectedEntries)

	}()

	// 2c. Run against invalid URL - get 404, handle it.
	func() {
		var dirContents []*FileEntry
		var parent *FileEntry
		d := newDCacheClient("faketoken", "", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedErrContains := "request failed"
		expectedEntries := []*FileEntry{}
		source := "http://localhost:8080/invalidendpoint"
		dirContents = nil
		parent = nil
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ErrorContains(t, err, expectedErrContains)
		assert.ElementsMatch(t, entries, expectedEntries)

	}()

	// 3. GOOD CASE Run against valid URL, get 200, parse response, return file entries
	func() {
		var dirContents []*FileEntry
		var parent *FileEntry
		d := newDCacheClient("faketoken", "/api", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedEntries := createFileEntriesForJobsubStageDir()
		source := "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/"
		dirContents = nil
		parent = nil
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.NoError(t, err)
		assert.ElementsMatch(t, entries, expectedEntries)

	}()
	// 4. Run against valid URL with invalid files, get 200, handle it.
	func() {
		var dirContents []*FileEntry
		var parent *FileEntry
		d := newDCacheClient("faketoken", "/api", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedEntries := createFileEntriesForInvalidFileDir()
		source := "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/invalidfiledir"
		expectedErrContains := "unknown file type"
		dirContents = nil
		parent = nil
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ElementsMatch(t, entries, expectedEntries)
		assert.ErrorContains(t, err, expectedErrContains)
	}()

	// 5. Run against valid URL with empty page, get 200, should have error decoding JSON
	func() {
		var dirContents []*FileEntry
		var parent *FileEntry
		d := newDCacheClient("faketoken", "/api", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		source := "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/emptyPage"
		expectedEntries := []*FileEntry{}
		expectedErrContains := "error reading dCache directory listing response"
		dirContents = nil
		parent = nil
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ElementsMatch(t, entries, expectedEntries)
		assert.ErrorContains(t, err, expectedErrContains)
	}()

	// 6 . Exclude every file
	func() {
		var dirContents []*FileEntry
		var parent *FileEntry
		d := newDCacheClient("faketoken", "/api", -1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedEntries := []*FileEntry{}
		source := "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/"
		dirContents = nil
		parent = nil
		entries, err := d.getFilesList(ctx, source, dirContents, parent, func(*FileEntry) bool { return true })
		assert.NoError(t, err)
		assert.ElementsMatch(t, entries, expectedEntries)
	}()

	// TODO 7. Hit the file count limit
	func() {
		var dirContents []*FileEntry
		var parent *FileEntry
		d := newDCacheClient("faketoken", "/api", 1, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedEntries := createFileEntriesForJobsubStageDir()[:1]
		source := "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/"
		dirContents = nil
		parent = nil
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ErrorContains(t, err, errFileCountLimitExceeded.Error())
		assert.ElementsMatch(t, entries, expectedEntries)
	}()

	// TODO 7. Hit the file count limit mid-directory
	func() {
		var dirContents []*FileEntry
		var parent *FileEntry
		d := newDCacheClient("faketoken", "/api", 3, 0, 0, true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectedEntries := createFileEntriesForJobsubStageDirInterruptMidDir()
		source := "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/"
		dirContents = nil
		parent = nil
		entries, err := d.getFilesList(ctx, source, dirContents, parent, nil)
		assert.ErrorContains(t, err, errFileCountLimitExceeded.Error())
		assert.ElementsMatch(t, entries, expectedEntries)
	}()

}

func TestMsecToUnixTuple(t *testing.T) {
	type testCase struct {
		description  string
		inputMsec    int64
		expectedSec  int64
		expectedNsec int64
	}
	testCases := []testCase{
		{
			description:  "Zero milliseconds",
			inputMsec:    0,
			expectedSec:  0,
			expectedNsec: 0,
		},
		{
			description:  "Exactly one second",
			inputMsec:    1000,
			expectedSec:  1,
			expectedNsec: 0,
		},
		{
			description:  "One second and 500 milliseconds",
			inputMsec:    1500,
			expectedSec:  1,
			expectedNsec: 500_000_000,
		},
		{
			description:  "Random milliseconds",
			inputMsec:    1234567,
			expectedSec:  1234,
			expectedNsec: 567_000_000,
		},
		{
			description:  "Less than one second",
			inputMsec:    999,
			expectedSec:  0,
			expectedNsec: 999_000_000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			sec, nsec := mSecToUnixTuple(tc.inputMsec)
			assert.Equal(t, tc.expectedSec, sec)
			assert.Equal(t, tc.expectedNsec, nsec)
		})
	}
}

func TestFileListingToFileEntry(t *testing.T) {
	type testCase struct {
		description         string
		listing             dCacheFileListing
		expectedFilename    string
		expectedIsDirectory bool
		expectedModified    time.Time
		expectedErrContains string
	}

	filenameTransform := func(s string) string {
		return "/pnfs/testexperiment/resilient/jobsub_stage/" + s
	}

	testCases := []testCase{
		{
			description: "Regular file type",
			listing: dCacheFileListing{
				FileName: "file1",
				FileType: "REGULAR",
				Mtime:    1753116816381,
			},
			expectedFilename:    "/pnfs/testexperiment/resilient/jobsub_stage/file1",
			expectedIsDirectory: false,
			expectedModified:    time.Unix(1753116816, 381_000_000).UTC(),
		},
		{
			description: "Directory file type",
			listing: dCacheFileListing{
				FileName: "dir1",
				FileType: "DIR",
				Mtime:    1753116816383,
			},
			expectedFilename:    "/pnfs/testexperiment/resilient/jobsub_stage/dir1",
			expectedIsDirectory: true,
			expectedModified:    time.Unix(1753116816, 383_000_000).UTC(),
		},
		{
			description: "Unknown file type",
			listing: dCacheFileListing{
				FileName: "badfile",
				FileType: "UNKNOWN",
				Mtime:    1753116816382,
			},
			expectedErrContains: "unknown file type",
		},
	}

	d := &dCacheClient{}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			entry, err := d.fileListingToFileEntry(tc.listing, filenameTransform)
			if tc.expectedErrContains != "" {
				assert.Nil(t, entry)
				assert.ErrorContains(t, err, tc.expectedErrContains)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedFilename, entry.filename)
			assert.Equal(t, tc.expectedIsDirectory, entry.isDirectory)
			assert.True(t, entry.modified.Equal(tc.expectedModified))
		})
	}
}

func TestDCacheClientTrimAPIEndpoint(t *testing.T) {
	type testCase struct {
		description string
		apiEndpoint string
		urlPath     string
		expected    string
	}
	testCases := []testCase{
		{
			description: "Prefix matches exactly",
			apiEndpoint: "/api/v1/namespace/",
			urlPath:     "/api/v1/namespace/test/path/file.txt",
			expected:    "test/path/file.txt",
		},
		{
			description: "Prefix does not match",
			apiEndpoint: "/api/v1/namespace/",
			urlPath:     "/otherprefix/test/path/file.txt",
			expected:    "/otherprefix/test/path/file.txt",
		},
		{
			description: "Prefix is empty",
			apiEndpoint: "",
			urlPath:     "/api/v1/namespace/test/path/file.txt",
			expected:    "/api/v1/namespace/test/path/file.txt",
		},
		{
			description: "Prefix is partial match",
			apiEndpoint: "/api/v1/",
			urlPath:     "/api/v1/namespace/test/path/file.txt",
			expected:    "namespace/test/path/file.txt",
		},
		{
			description: "Prefix matches root",
			apiEndpoint: "/",
			urlPath:     "/test/path/file.txt",
			expected:    "test/path/file.txt",
		},
		{
			description: "Prefix matches but urlPath is only prefix",
			apiEndpoint: "/api/v1/namespace/",
			urlPath:     "/api/v1/namespace/",
			expected:    "",
		},
		{
			description: "Prefix matches but urlPath is only prefix with extra slash",
			apiEndpoint: "/api/v1/namespace/",
			urlPath:     "/api/v1/namespace//",
			expected:    "/",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			client := &dCacheClient{apiEndpoint: tc.apiEndpoint}
			result := client.trimAPIEndpoint(tc.urlPath)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDCacheClientPathToAPIURLPath(t *testing.T) {
	type testCase struct {
		description string
		apiEndpoint string
		inputPath   string
		expected    string
	}
	testCases := []testCase{
		{
			description: "Both apiEndpoint and path have leading slashes",
			apiEndpoint: "/api/v1/namespace/",
			inputPath:   "/test/path/file.txt",
			expected:    "/api/v1/namespace/test/path/file.txt",
		},
		{
			description: "apiEndpoint has trailing slash, path has no leading slash",
			apiEndpoint: "/api/v1/namespace/",
			inputPath:   "test/path/file.txt",
			expected:    "/api/v1/namespace/test/path/file.txt",
		},
		{
			description: "apiEndpoint has no trailing slash, path has leading slash",
			apiEndpoint: "/api/v1/namespace",
			inputPath:   "/test/path/file.txt",
			expected:    "/api/v1/namespace/test/path/file.txt",
		},
		{
			description: "apiEndpoint and path both have no slashes",
			apiEndpoint: "api",
			inputPath:   "file.txt",
			expected:    "api/file.txt",
		},
		{
			description: "apiEndpoint is empty",
			apiEndpoint: "",
			inputPath:   "/test/path/file.txt",
			expected:    "/test/path/file.txt",
		},
		{
			description: "path is empty",
			apiEndpoint: "/api/v1/namespace/",
			inputPath:   "",
			expected:    "/api/v1/namespace",
		},
		{
			description: "apiEndpoint is root",
			apiEndpoint: "/",
			inputPath:   "file.txt",
			expected:    "/file.txt",
		},
		{
			description: "path is just a slash",
			apiEndpoint: "/api/v1/namespace/",
			inputPath:   "/",
			expected:    "/api/v1/namespace",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			client := &dCacheClient{apiEndpoint: tc.apiEndpoint}
			result := client.pathToAPIURLPath(tc.inputPath)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSetGetHeaders(t *testing.T) {
	fakeToken := "testtoken"
	fakeReq, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

	type testCase struct {
		description         string
		token               string
		authFunc            func(req *http.Request) error
		req                 *http.Request
		expectedErr         error
		expectedHeadersHave map[string]string
	}

	testCases := []testCase{
		{
			description:         "Nil request",
			token:               fakeToken,
			authFunc:            nil,
			req:                 nil,
			expectedErr:         errNilRequest,
			expectedHeadersHave: nil,
		},
		{
			description: "Valid request with token, no authFunc",
			token:       fakeToken,
			authFunc:    func(req *http.Request) error { return nil },
			req:         fakeReq,
			expectedErr: nil,
			expectedHeadersHave: map[string]string{
				"Accept": "application/json",
			},
		},
		{
			description: "Valid request with token, authFunc errors",
			token:       "",
			authFunc: func(req *http.Request) error {
				return errNoTokenProvided
			},
			req:                 fakeReq,
			expectedErr:         errNoTokenProvided,
			expectedHeadersHave: nil,
		},
		{
			description: "Valid request with token, proper authFunc",
			token:       fakeToken,
			authFunc: func(req *http.Request) error {
				req.Header.Set("Authorization", "Bearer "+fakeToken)
				return nil
			},
			req:         fakeReq,
			expectedErr: nil,
			expectedHeadersHave: map[string]string{
				"Accept":        "application/json",
				"Authorization": "Bearer " + fakeToken,
			},
		},
	}

	// var req *http.Request
	// // 1. noop auth-function
	// token := "testtoken"
	// authFunc := func(req *http.Request) error { return nil }
	// expectedHeadersHave := map[string]string{
	// 	"Accept": "application/json",
	// }

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel()
			d := &dCacheClient{
				client:   http.DefaultClient,
				authFunc: tc.authFunc,
				token:    tc.token,
			}
			err := d.setGetHeaders(tc.req)
			assert.ErrorIs(t, err, tc.expectedErr)
			for k, v := range tc.expectedHeadersHave {
				assert.Contains(t, tc.req.Header, k)
				assert.Equal(t, v, tc.req.Header.Get(k))
			}
		})
	}
}

// Utility functions

func createFileEntriesForJobsubStageDir() []*FileEntry {
	// Need to make some pointers for linking purposes
	var dir1Entry, dir1File1a *FileEntry
	dir1Entry = &FileEntry{
		filename:      "/pnfs/testexperiment/resilient/jobsub_stage/dir1",
		modified:      time.Unix(1753116816, 382_000_000).UTC(),
		isDirectory:   true,
		containsFiles: nil,
		parent:        nil,
	}
	dir1File1a = &FileEntry{
		filename:      "/pnfs/testexperiment/resilient/jobsub_stage/dir1/file1a",
		modified:      time.Unix(1753116816, 382_000_000).UTC(),
		isDirectory:   false,
		containsFiles: nil,
		parent:        dir1Entry,
	}
	dir1Entry.containsFiles = append(dir1Entry.containsFiles, dir1File1a)

	return []*FileEntry{
		{
			filename:      "/pnfs/testexperiment/resilient/jobsub_stage/file1",
			modified:      time.Unix(1753116816, 382_000_000).UTC(),
			isDirectory:   false,
			containsFiles: nil,
			parent:        nil,
		},
		{
			filename:      "/pnfs/testexperiment/resilient/jobsub_stage/file1b",
			modified:      time.Unix(1753116816, 382_000_000).UTC(),
			isDirectory:   false,
			containsFiles: nil,
			parent:        nil,
		},
		dir1File1a,
		dir1Entry,
		{
			filename:      "/pnfs/testexperiment/resilient/jobsub_stage/dir2",
			modified:      time.Unix(1753116816, 382_000_000).UTC(),
			isDirectory:   true,
			containsFiles: nil,
			parent:        nil,
		},
	}
}

func createFileEntriesForJobsubStageDirInterruptMidDir() []*FileEntry {
	sl := createFileEntriesForJobsubStageDir()[:3]
	last := sl[len(sl)-1]
	last.parent.containsFiles = nil // Simulate an interrupted directory listing
	sl[len(sl)-1] = last            // Update the last entry
	return sl
}

func createFileEntriesForInvalidFileDir() []*FileEntry {
	return []*FileEntry{
		{
			filename:      "/pnfs/testexperiment/resilient/jobsub_stage/invalidfiledir/file1a",
			modified:      time.Unix(1753116816, 382_000_000).UTC(),
			isDirectory:   false,
			containsFiles: nil,
			parent:        nil,
		},
	}
}
