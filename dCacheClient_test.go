package main

import (
	"context"
	"testing"

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
			client := newDCacheClient("test-token", true)
			err := client.removeFile(tc.ctx, tc.urlPath)

			if tc.assertNoError {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.expectedErrContains)
		})
	}
}
