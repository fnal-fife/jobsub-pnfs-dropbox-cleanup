package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knadh/koanf/v2"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	// Setup code here if needed
	logger = slog.New(slog.NewTextHandler(os.Stdout, nil)) // Dummy logger for tests
	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestRun(t *testing.T) {

	type testCase struct {
		description   string
		setupFunc     func(*testing.T) (k *koanf.Koanf, cleanupFunc func())
		assertNoError bool
		errIs         error
		errContains   string
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
				k := newTestKoanf().
					withExperiment(t)
				k.ko.Set("deleteFilesOlderThan", "not-a-duration") // Set an invalid duration

				return k.ko, nil
			},
			errContains: "time: invalid duration",
		},
		{
			description: "Vault token does not exist",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf().
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
				k := newTestKoanf().
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
				k := newTestKoanf().
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withGfal2ClientNoRetries(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t), // Mock a working htgettoken command
					writeFakeBadGfalLs(t),  // Mock a faulty gfal-ls command
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
				k := newTestKoanf().
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withGfal2ClientNoRetries(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t),           // Mock a working htgettoken command
					writeFakeGfalLsReturnsNoFiles(t), // Mock a gfal-ls command that prints nothing
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
				k := newTestKoanf().
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withGfal2ClientNoRetries(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t),             // Mock a working htgettoken command
					writeFakeGfalLsReturnsSomeFiles(t), // Mock a gfal-ls command that prints some files
					writeFakeBadCondorStatus(t),        // Mock a failing condor_status command
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
				k := newTestKoanf().
					withExperiment(t).
					withValidAgeCutoff(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withGfal2ClientNoRetries(t)

				mockCleanupFuncs := []mockCleanup{
					writeGoodHtgettoken(t),             // Mock a working htgettoken command
					writeFakeGfalLsReturnsSomeFiles(t), // Mock a gfal-ls command that prints some files
					writeFakeGoodCondorStatus(t),       // Mock a good condor_status command
					writeFakeCondorQScript(t, strings.NewReader(`#!/bin/sh
					exit 1`)), // Mock a failing condor_q command
				}

				cleanupFunc := func() {
					for _, cleanup := range mockCleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			errIs: errScheddQueryFailed,
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
			default:
				t.Fatalf("No error assertion provided for test case: %s", tc.description)
			}
		})
	}
}

// A helper struct to more easily adjust the koanf configuration for tests
// This basically allows us to extend the koanf.Koanf type to add helper methods
type testKoanf struct {
	ko *koanf.Koanf
}

func newTestKoanf() *testKoanf {
	return &testKoanf{ko: koanf.New(".")}
}

func (k *testKoanf) withExperiment(t *testing.T) *testKoanf {
	t.Helper()
	k.ko.Set("experiment", "testexperiment")
	return k
}

func (k *testKoanf) withValidAgeCutoff(t *testing.T) *testKoanf {
	t.Helper()
	k.ko.Set("deleteFilesOlderThan", "1s")
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
	bearerTokenPath := filepath.Join("testData", "testTokens", "goodToken")
	k.ko.Set("vault.bearerTokenFile", bearerTokenPath)
	k.ko.Set("vault.experiment", "testexperiment")
	return k
}

func (k *testKoanf) withGfal2ClientNoRetries(t *testing.T) *testKoanf {
	t.Helper()
	k.ko.Set("gfal2.retryCount", 0)
	k.ko.Set("gfal2.retrySleep", "1ns")
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

func writeFakeBadGfalLs(t *testing.T) mockCleanup {
	t.Helper()
	temp := t.TempDir()
	oldPath, ok := exeMap["gfal-ls"]
	cleanupFunc := func() {
		if ok {
			exeMap["gfal-ls"] = oldPath // Restore original gfal-ls command after our test
			return
		}
		delete(exeMap, "gfal-ls") // Remove gfal-ls from exeMap if it was not set
	}
	gfalLsPath := filepath.Join(temp, "gfal-ls")
	failingScript := `#!/bin/sh
	exit 1
	`
	if err := os.WriteFile(gfalLsPath, []byte(failingScript), 0755); err != nil {
		t.Fatalf("failed to write mock gfal-ls script: %v", err)
	}
	exeMap["gfal-ls"] = gfalLsPath
	return cleanupFunc
}

func writeFakeGfalLsReturnsNoFiles(t *testing.T) mockCleanup {
	t.Helper()
	temp := t.TempDir()
	oldPath, ok := exeMap["gfal-ls"]
	cleanupFunc := func() {
		if ok {
			exeMap["gfal-ls"] = oldPath // Restore original gfal-ls command after our test
			return
		}
		delete(exeMap, "gfal-ls") // Remove gfal-ls from exeMap if it was not set
	}
	gfalLsPath := filepath.Join(temp, "gfal-ls")
	script := `#!/bin/sh
	exit 0
	`
	if err := os.WriteFile(gfalLsPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock gfal-ls script: %v", err)
	}
	exeMap["gfal-ls"] = gfalLsPath
	return cleanupFunc
}

func writeFakeGfalLsReturnsSomeFiles(t *testing.T) mockCleanup {
	t.Helper()
	temp := t.TempDir()
	oldPath, ok := exeMap["gfal-ls"]
	cleanupFunc := func() {
		if ok {
			exeMap["gfal-ls"] = oldPath // Restore original gfal-ls command after our test
			return
		}
		delete(exeMap, "gfal-ls") // Remove gfal-ls from exeMap if it was not set
	}
	gfalLsPath := filepath.Join(temp, "gfal-ls")
	script := `#!/bin/sh
	echo "-rwxrwxrwx   0 0     0            50 Sep 26 14:55 bogus_file.out"
	echo "drwxrwxrwx   0 0     0             0 Apr  6  2023 bogus_dir"
	exit 0
	`
	if err := os.WriteFile(gfalLsPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock gfal-ls script: %v", err)
	}
	exeMap["gfal-ls"] = gfalLsPath
	return cleanupFunc
}

func writeFakeBadCondorStatus(t *testing.T) mockCleanup {
	t.Helper()
	temp := t.TempDir()
	oldPath, ok := exeMap["condor_status"]
	cleanupFunc := func() {
		if ok {
			exeMap["condor_status"] = oldPath // Restore original condor_status command after our test
			return
		}
		delete(exeMap, "condor_status") // Remove condor_status from exeMap if it was not set
	}
	condorStatusPath := filepath.Join(temp, "condor_status")
	failingScript := `#!/bin/sh
	exit 1
	`
	if err := os.WriteFile(condorStatusPath, []byte(failingScript), 0755); err != nil {
		t.Fatalf("failed to write mock condor_status script: %v", err)
	}
	exeMap["condor_status"] = condorStatusPath
	return cleanupFunc
}

func writeFakeGoodCondorStatus(t *testing.T) mockCleanup {
	t.Helper()
	temp := t.TempDir()
	oldPath, ok := exeMap["condor_status"]
	cleanupFunc := func() {
		if ok {
			exeMap["condor_status"] = oldPath // Restore original condor_status command after our test
			return
		}
		delete(exeMap, "condor_status") // Remove condor_status from exeMap if it was not set
	}
	condorStatusPath := filepath.Join(temp, "condor_status")
	fakeAdsFile := filepath.Join("testData", "condorOutput", "condor_status_mock_ads")
	// TODO How does condor_status return the classads?
	workingScript := fmt.Sprintf(`#!/bin/sh
	cat %s
	exit 0
	`, fakeAdsFile)
	if err := os.WriteFile(condorStatusPath, []byte(workingScript), 0755); err != nil {
		t.Fatalf("failed to write mock condor_status script: %v", err)
	}
	exeMap["condor_status"] = condorStatusPath
	return cleanupFunc
}
