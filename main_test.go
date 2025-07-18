package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
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
			description: "Vault token does not exist",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf().withExperiment(t).withVaultToken(t, false)
				return k.ko, nil
			},
			errIs: errNoVaultTokenFile,
		},
		{
			description: "getting bearer token fails",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf().
					withExperiment(t).
					withVaultToken(t, true)
				cleanupFunc := writeHtgettokenTestScript(t, 1)
				return k.ko, cleanupFunc
			},
			errContains: "error getting and validating token",
		},
		{
			description: "getting pnfs dropbox files fails",
			setupFunc: func(t *testing.T) (*koanf.Koanf, func()) {
				k := newTestKoanf().
					withExperiment(t).
					withVaultToken(t, true).
					withBearerToken(t).
					withGfal2ClientNoRetries(t)

				cleanupFuncs := []func(){
					writeHtgettokenTestScript(t, 0), // Mock a working htgettoken command
					writeFakeGfalLs(t),              // Mock a faulty gfal-ls command

				}

				cleanupFunc := func() {
					for _, cleanup := range cleanupFuncs {
						defer cleanup()
					}
				}
				return k.ko, cleanupFunc
			},
			errContains: "error getting dropbox files list",
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

func writeFakeGfalLs(t *testing.T) (cleanupFunc func()) {
	t.Helper()
	temp := t.TempDir()
	oldPath, ok := exeMap["gfal-ls"]
	cleanupFunc = func() {
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
