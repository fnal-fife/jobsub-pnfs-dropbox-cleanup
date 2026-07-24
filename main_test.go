package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/knadh/koanf/v2"
	"github.com/stretchr/testify/assert"

	"github.com/fnal-fife/jobsub-pnfs-dropbox-cleanup/internal/testserver"
)

var loggingMux sync.Mutex // Use this mutex if you're trying to run a test in parallel, or want to modify the global logger

func TestMain(m *testing.M) {
	// Setup code here if needed
	logger = slog.New(slog.NewTextHandler(os.Stdout, nil)) // Dummy logger for tests

	// Start our dCache test server
	ctx, cancel := context.WithCancel(context.Background())
	shutdown, err := testserver.StartServer(ctx)
	if err != nil {
		fmt.Printf("Failed to start test server: %v\n", err)
		os.Exit(1)
	}

	exitCode := m.Run()

	cancel()   // Cancel the context to shut down the server
	<-shutdown // Wait for the server to shut down

	os.Exit(exitCode)
}

func TestRun(t *testing.T) {

	type testCase struct {
		description            string
		setupFunc              func(*testing.T) (k *koanf.Koanf, cleanupFunc func())
		assertNoError          bool
		errIs                  error
		errContains            string
		expectedFileDeleteErrs []string
		extraTests             []func(t *testing.T)
	}

	testCases := []testCase{
		{
			description: "Experiment not set",
			setupFunc:   func(t *testing.T) (k *koanf.Koanf, cleanupFunc func()) { return koanf.New("."), nil },
			errIs:       errUsage,
		},
		{
			description: "parse error of ageCutoff",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExperiment(t)
				k.ko.Set("deleteFilesOlderThan", "not-a-duration") // Set an invalid duration

				return k.ko, nil
			},
			errContains: "time: invalid duration",
		},
		{
			description: "Vault token does not exist",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, false)
				return k.ko, nil
			},
			errIs: errNoVaultTokenFile,
		},
		{
			description: "getting bearer token fails",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true)
				cleanupFunc := writeBadHtgettoken(t) // Mock a failing htgettoken command
				return k.ko, cleanupFunc
			},
			errContains: "error getting and validating token",
		},
		{
			description: "getting pnfs dropbox files fails",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExptOverride(t, "/testexperiment/resilient/jobsub_stage/internalServerError").
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
				}

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			errContains: "error getting dropbox files list",
		},
		{
			description: "getting pnfs dropbox files succeeds, but no files are returned",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExptOverride(t, "/testexperiment/resilient/jobsub_stage/dir2").
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
				}

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			errIs: errNoFilesInDropbox,
		},
		{
			description: "getting pnfs dropbox files succeeds with files, cannot get condor schedds",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "exit1")), // Mock a failing condor_status command
				}

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			errContains: "error getting condor schedds",
		},
		{
			description: "getting pnfs dropbox files succeeds with files, schedds, cannot get condor jobs",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "condor_status-mock-ads")),                                  // Mock a good condor_status command
					useFakeExecutable(t, "condor_config_val", filepath.Join("internal", "testscripts", "condor_config_val-SEC_CLIENT_AUTHENTICATION_METHODS")), // Mock a working condor_config_val command
					useFakeExecutable(t, "condor_q", filepath.Join("internal", "testscripts", "exit1")),                                                        // Mock a failing condor_q command
				}

				// Set up fake idtokens dir
				fakeGoodIDTokenAuthSetup(t)

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			errIs: errScheddQueryFailed,
		},
		{
			// Both dropbox list and condor list should have /pnfs/testexperiment/resilient/jobsub_stage/file1
			description: "all files in dropbox list are used by jobs - no files to delete",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExptOverride(t, "/testexperiment/resilient/jobsub_stage_only_file1").
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "condor_status-mock-ads")),                                  // Mock a good condor_status command
					useFakeExecutable(t, "condor_config_val", filepath.Join("internal", "testscripts", "condor_config_val-SEC_CLIENT_AUTHENTICATION_METHODS")), // Mock a working condor_config_val command
					useFakeExecutable(t, "condor_q", filepath.Join("internal", "testscripts", "condor_q-mock-ads")),                                            // Mock a condor_q command that returns the same file as gfal-ls
				}

				// Set up fake idtokens dir
				fakeGoodIDTokenAuthSetup(t)

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			errIs: errNoFilesToDelete,
		},
		{
			description: "test mode - we should return nil error after getting planned delete list",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(true).
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "condor_status-mock-ads")),                                  // Mock a good condor_status command
					useFakeExecutable(t, "condor_config_val", filepath.Join("internal", "testscripts", "condor_config_val-SEC_CLIENT_AUTHENTICATION_METHODS")), // Mock a working condor_config_val command
					useFakeExecutable(t, "condor_q", filepath.Join("internal", "testscripts", "condor_q-mock-ads-empty-pnfs")),                                 // Mock a condor_q command that returns the same file as gfal-ls
				}

				// Set up fake idtokens dir
				fakeGoodIDTokenAuthSetup(t)

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			assertNoError: true,
		},
		{
			description: "NOT test mode - but there were only files, so we never try to delete directories",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(false).
					withExptOverride(t, "/testexperiment/resilient/jobsub_stage_only_file1").
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "condor_status-mock-ads")),                                  // Mock a good condor_status command
					useFakeExecutable(t, "condor_config_val", filepath.Join("internal", "testscripts", "condor_config_val-SEC_CLIENT_AUTHENTICATION_METHODS")), // Mock a working condor_config_val command
					useFakeExecutable(t, "condor_q", filepath.Join("internal", "testscripts", "condor_q-mock-ads-empty-pnfs")),                                 // Mock a condor_q command that returns the same file as gfal-ls
				}

				// Set up fake idtokens dir
				fakeGoodIDTokenAuthSetup(t)

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			assertNoError: true,
		},
		{
			description: "NOT test mode - one file, but can't be deleted",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(false).
					withExptOverride(t, "/testexperiment/resilient/jobsub_stage_only_file2").
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "condor_status-mock-ads")),                                  // Mock a good condor_status command
					useFakeExecutable(t, "condor_config_val", filepath.Join("internal", "testscripts", "condor_config_val-SEC_CLIENT_AUTHENTICATION_METHODS")), // Mock a working condor_config_val command
					useFakeExecutable(t, "condor_q", filepath.Join("internal", "testscripts", "condor_q-mock-ads-empty-pnfs")),                                 // Mock a working condor_q command
				}

				// Set up fake idtokens dir
				fakeGoodIDTokenAuthSetup(t)

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			expectedFileDeleteErrs: []string{"/pnfs/testexperiment/resilient/jobsub_stage_only_file2/file2"},
		},
		{
			description: "NOT test mode - one file, one dir, should return nil error",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf(false).
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withTestDcacheServer(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "condor_status-mock-ads")),                                  // Mock a good condor_status command
					useFakeExecutable(t, "condor_config_val", filepath.Join("internal", "testscripts", "condor_config_val-SEC_CLIENT_AUTHENTICATION_METHODS")), // Mock a working condor_config_val command
					useFakeExecutable(t, "condor_q", filepath.Join("internal", "testscripts", "condor_q-mock-ads-empty-pnfs")),                                 // Mock a working condor_q command
				}

				// Set up fake idtokens dir
				fakeGoodIDTokenAuthSetup(t)

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			assertNoError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			k, cleanupFunc := tc.setupFunc(t)
			if cleanupFunc != nil {
				defer cleanupFunc()
			}
			ctx := context.Background()
			err := run(ctx, k)
			switch {
			case tc.assertNoError:
				assert.NoError(t, err)
			case tc.errIs != nil:
				assert.ErrorIs(t, err, tc.errIs)
			case tc.errContains != "":
				assert.ErrorContains(t, err, tc.errContains)
			case len(tc.expectedFileDeleteErrs) > 0:
				var e1 *errDeletingFiles
				assert.ErrorAs(t, err, &e1)
				assert.ElementsMatch(t, e1.files, tc.expectedFileDeleteErrs)
			default:
				t.Fatalf("No error assertion provided for test case: %s", tc.description)
			}
		})
	}
}

func TestRunFileLimit(t *testing.T) {
	k := newTestKoanf(false).
		withExperiment(t).
		withValidAgeCutoff(t).
		withVaultToken(t, true).
		withBearerToken(t).
		withFileCountLimit(t, 5). // Set a file count limit of 5.  With our test server, this should trigger the limit after we process
		// /api/testexperiment/resilient/jobsub_stage/dir1/dir1a. Since /api/testexperiment/resilient/jobsub_stage/dir1 will then be "not
		// completely processed", our test should show that we skip trying to delete /api/testexperiment/resilient/jobsub_stage/dir1.
		withDebug(t). // Enable debug logging to capture the file limit message
		withTestDcacheServer(t)

	// Redirect stdout to a bytes.Buffer so we can inspect logs
	loggingMux.Lock()
	oldLogger := logger
	b := bytes.NewBuffer(nil)
	logger = slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{
		Level: slog.LevelDebug, // Turn on debug for this test
	}))
	defer func() {
		logger = oldLogger // Restore original logger after test
		loggingMux.Unlock()
	}()

	// Setup and Deferred cleanup
	cleanupFuncs := []mockCleanup{
		writeGoodHtgettoken(t), // Mock a working htgettoken command
		useFakeExecutable(t, "condor_status", filepath.Join("internal", "testscripts", "condor_status-mock-ads")),                                  // Mock a good condor_status command
		useFakeExecutable(t, "condor_config_val", filepath.Join("internal", "testscripts", "condor_config_val-SEC_CLIENT_AUTHENTICATION_METHODS")), // Mock a working condor_config_val command
		useFakeExecutable(t, "condor_q", filepath.Join("internal", "testscripts", "condor_q-mock-ads-empty-pnfs")),                                 // Mock a working condor_q command
	}

	for _, cleanup := range cleanupFuncs {
		defer cleanup()
	}

	// Set up fake idtokens dir
	fakeGoodIDTokenAuthSetup(t)

	ctx := context.Background()
	err := run(ctx, k.ko)

	assert.NoError(t, err)

	// Read the output from the buffer
	output := b.String()

	// Check if the output contains the expected message about file limit
	expectedMessage := "Parent did not get all files processed, so we will not delete it"
	if !strings.Contains(string(output), expectedMessage) {
		t.Errorf("Expected output to contain %q, but it did not. Output: %s", expectedMessage, string(output))
	}
}

// A helper struct to more easily adjust the koanf configuration for tests
// This basically allows us to extend the koanf.Koanf type to add helper methods
type testKoanf struct {
	ko *koanf.Koanf
}

func newTestKoanf(testMode bool) *testKoanf {
	k := &testKoanf{ko: koanf.New(".")}
	k.ko.Set("test", testMode) // Set a dummy value to indicate this is a test koanf
	return k
}

func (k *testKoanf) withExperiment(t *testing.T) *testKoanf {
	t.Helper()
	k.ko.Set("experiment", "testexperiment")
	return k
}

func (k *testKoanf) withExptOverride(t *testing.T, path string) *testKoanf {
	t.Helper()
	k.ko.Set("experiment", "testexperiment")
	k.ko.Set("exptNameOverride.testexperiment", path)
	return k
}

func (k *testKoanf) withValidAgeCutoff(t *testing.T) *testKoanf {
	t.Helper()
	k.ko.Set("deleteFilesOlderThan", "1s")
	return k
}

func (k *testKoanf) withFileCountLimit(t *testing.T, limit int) *testKoanf {
	t.Helper()
	k.ko.Set("dCache.totalFileCountLimit", limit)
	return k
}

func (k *testKoanf) withDebug(t *testing.T) *testKoanf {
	t.Helper()
	k.ko.Set("debug", true)
	return k
}

func (k *testKoanf) withVaultToken(t *testing.T, exists bool) *testKoanf {
	t.Helper()
	temp := t.TempDir()
	vaultTokenPath := filepath.Join(temp, "fake-vault-token")

	// If we specify that the file should exist, write it. Otherwise, we're assuming that we don't want the test to see a file at the path we indicate in
	// vault.vaultTokenFile
	if exists {
		if err := os.WriteFile(vaultTokenPath, []byte("fake-token"), 0644); err != nil {
			t.Fatalf("failed to write fake vault token: %v", err)
		}
	}
	k.ko.Set("vault.vaultTokenFile", vaultTokenPath)
	k.ko.Set("vault.vaultTokenAgeCutoff", "1h")
	return k
}

func (k *testKoanf) withBearerToken(t *testing.T) *testKoanf {
	t.Helper()
	bearerTokenPath := filepath.Join("internal", "testtokens", "goodToken")
	k.ko.Set("vault.bearerTokenFile", bearerTokenPath)
	k.ko.Set("vault.experiment", "testexperiment")
	return k
}

func (k *testKoanf) withTestDcacheServer(t *testing.T) *testKoanf {
	t.Helper()
	k.ko.Set("dCache.hostPort", "http://localhost:8080")
	k.ko.Set("dCache.apiEndpoint", "/api")
	return k
}

type mockCleanup func()

func writeBadHtgettoken(t *testing.T) mockCleanup {
	t.Helper()
	return writeHtgettokenTestScript(t, 1)
}

func writeGoodHtgettoken(t *testing.T) mockCleanup {
	t.Helper()
	return writeHtgettokenTestScript(t, 0)
}

// useFakeExecutable is a helper function to mock an executable command in the test environment by changing the exeMap map.
// It returns a cleanup function that restores the original command path after the test.
func useFakeExecutable(t *testing.T, exeName, filename string) mockCleanup {
	t.Helper()
	oldPath, ok := exeMap[exeName]
	cleanupFunc := func() {
		if ok {
			exeMap[exeName] = oldPath // Restore original command after our test
			return
		}
		delete(exeMap, exeName) // Remove command from exeMap if it was not set
	}
	exeMap[exeName] = filename
	return cleanupFunc
}
