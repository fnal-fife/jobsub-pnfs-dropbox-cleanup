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
			urlPath:       "http://localhost:8080/testexperiment/resilient/jobsub_stage/file1",
			assertNoError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel() // Run tests in parallel
			client := newDCacheClient("test-token", "", true)
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
