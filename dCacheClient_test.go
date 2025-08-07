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

func TestDCacheClientGetFilesList(t *testing.T) {
	type testCase struct {
		description      string
		contextSetupFunc func() (ctx context.Context, cleanupFunc func())
		fileCountLimit   int
		source           string
		dirContents      []*FileEntry
		parent           *FileEntry
		excludeFunc      func(*FileEntry) bool
		errCheckFunc     func(err error) bool
		expectedEntries  []*FileEntry
	}

	testCases := []testCase{
		{
			description: "Context checking",
			contextSetupFunc: func() (context.Context, func()) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel the context to simulate a timeout or cancellation
				return ctx, nil
			},
			errCheckFunc: func(err error) bool {
				return assert.ErrorIs(t, err, context.Canceled)
			},
			expectedEntries: []*FileEntry{},
		},
		{
			description: "Run against invalid URL - can't create request",
			source:      "\x00invalid-url",
			errCheckFunc: func(err error) bool {
				return assert.ErrorContains(t, err, "error creating HTTP request to get files list")
			},
			expectedEntries: []*FileEntry{},
		},
		{
			description: "Run against invalid host",
			source:      "http://localhost:98765",
			errCheckFunc: func(err error) bool {
				return assert.ErrorContains(t, err, "error sending HTTP request to get files")
			},
			expectedEntries: []*FileEntry{},
		},
		{
			description: "Run against invalid URL - get 404, handle it",
			source:      "http://localhost:8080/invalidendpoint",
			errCheckFunc: func(err error) bool {
				return assert.ErrorContains(t, err, "request failed")
			},
			expectedEntries: []*FileEntry{},
		},
		{
			description: "Run against valid URL with empty directory, get 200, return no entries",
			source:      "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/dir2",
			errCheckFunc: func(err error) bool {
				return assert.NoError(t, err)
			},
			expectedEntries: []*FileEntry{},
		},
		{
			description: "Run against valid URL, get 200, parse response, return file entries",
			source:      "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/",
			errCheckFunc: func(err error) bool {
				return assert.NoError(t, err)
			},
			expectedEntries: createFileEntriesForJobsubStageDir(),
		},
		{
			description: "Run against valid URL with invalid files, get 200, handle it",
			source:      "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/invalidfiledir",
			errCheckFunc: func(err error) bool {
				return assert.ErrorContains(t, err, "unknown file type")
			},
			expectedEntries: createFileEntriesForInvalidFileDir(),
		},
		{
			description: "Run against valid URL with empty page, get 200, should have error decoding JSON",
			source:      "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/emptyPage",
			errCheckFunc: func(err error) bool {
				return assert.ErrorContains(t, err, "error reading dCache directory listing response")
			},
			expectedEntries: []*FileEntry{},
		},
		{
			description: "Run against valid URL, exclude all files",
			source:      "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/",
			excludeFunc: func(*FileEntry) bool { return true },
			errCheckFunc: func(err error) bool {
				var errTest *errProcessingFiles
				return assert.ErrorAs(t, err, &errTest)
			},
			expectedEntries: []*FileEntry{},
		},
		{
			description:    "Run against valid URL, hit file count limit",
			fileCountLimit: 1,
			source:         "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/",
			dirContents:    nil,
			parent:         nil,
			errCheckFunc: func(err error) bool {
				return assert.ErrorContains(t, err, errFileCountLimitExceeded.Error())
			},
			expectedEntries: createFileEntriesForJobsubStageDir()[:1],
		},
		{
			description:    "Run against valid URL, hit file count limit mid-directory",
			fileCountLimit: 3,
			source:         "http://localhost:8080/api/testexperiment/resilient/jobsub_stage/",
			dirContents:    nil,
			parent:         nil,
			errCheckFunc: func(err error) bool {
				return assert.ErrorContains(t, err, errFileCountLimitExceeded.Error())
			},
			expectedEntries: createFileEntriesForJobsubStageDirInterruptMidDir(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel() // Run tests in parallel

			// Default context setup func
			contextSetupFunc := tc.contextSetupFunc
			if contextSetupFunc == nil {
				contextSetupFunc = func() (context.Context, func()) {
					return context.WithCancel(context.Background())
				}
			}
			ctx, cleanup := contextSetupFunc()
			if cleanup != nil {
				defer cleanup()
			}

			fcLimit := -1 // Unlimited file counts as default
			if tc.fileCountLimit > 0 {
				fcLimit = tc.fileCountLimit
			}

			d := newDCacheClient("faketoken", "/api/", fcLimit, 0, 0, true)
			entries, err := d.getFilesList(ctx, tc.source, tc.dirContents, tc.parent, tc.excludeFunc)

			// Tests
			assert.True(t, tc.errCheckFunc(err))
			assert.ElementsMatch(t, entries, tc.expectedEntries)
		})
	}
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

func TestPNFSToHTTPS(t *testing.T) {
	path := "/pnfs/path/to/file"
	urlHostPort := "https://example.com:1234"
	apiEndpoint := "//api"
	transformFunc := func(filename string) string {
		return strings.TrimPrefix(filename, "/pnfs")
	}
	expected := "https://example.com:1234/api/path/to/file"

	result := PNFSToHTTPS(path, urlHostPort, apiEndpoint, transformFunc)
	assert.Equal(t, expected, result)
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
