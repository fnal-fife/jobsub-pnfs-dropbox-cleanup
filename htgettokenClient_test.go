package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewHtgettokenClient(t *testing.T) {
	tempDir := t.TempDir()
	vaultServer := "https://vault.example.com"
	vaultTokenFile, err := os.CreateTemp(tempDir, "vault_token_file")
	if err != nil {
		t.Error("failed to create temporary vault token file:", err)
	}
	outFile := "/path/to/output/file"

	// Adapted from https://stackoverflow.com/a/26806093
	captureOutput := func(f func()) []byte {
		var buf bytes.Buffer
		oldLogger := logger
		logger = slog.New(slog.NewTextHandler(&buf, nil))
		f()
		logger = oldLogger
		return buf.Bytes()
	}

	type testCase struct {
		description    string
		setupFunc      func(*testing.T)
		vaultTokenFile string
		options        []string
		expected       *htgettokenClient
		expectedStderr []string
	}

	testCases := []testCase{
		{
			"Default client with no options",
			func(t *testing.T) {},
			vaultTokenFile.Name(),
			[]string{},
			&htgettokenClient{
				vaultServer:    vaultServer,
				vaultTokenFile: vaultTokenFile.Name(),
				outFile:        outFile,
				options:        []string{},
			},
			nil,
		},
		{
			"Default client with options",
			func(t *testing.T) {},
			vaultTokenFile.Name(),
			[]string{"--option1", "value1", "--option2", "--option3", "value3"},
			&htgettokenClient{
				vaultServer:    vaultServer,
				vaultTokenFile: vaultTokenFile.Name(),
				outFile:        outFile,
				options:        []string{"--option1", "value1", "--option2", "--option3", "value3"},
			},
			nil,
		},
		{
			"Default client with bad infile",
			func(t *testing.T) {},
			"/path/to/nonexistent/file",
			[]string{},
			nil,
			[]string{"vault token file does not exist", "/path/to/nonexistent/file"},
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				test.setupFunc(t)
				var client *htgettokenClient
				out := string(captureOutput(
					func() {
						client = newHtgettokenClient(vaultServer, test.vaultTokenFile, outFile, test.options...)
					},
				))
				assert.Equal(t, test.expected, client)
				if test.expectedStderr != nil {
					for _, expectedStderr := range test.expectedStderr {
						assert.Contains(t, out, expectedStderr)
					}
				}
			},
		)
	}

}

func TestMergeHtgettokenopts(t *testing.T) {
	type testCase struct {
		description string
		env         []string
		options     []string
		expected    []string
	}

	testCases := []testCase{
		{
			"Empty environment and options",
			[]string{},
			[]string{},
			[]string{},
		},
		{
			"Environment with options",
			[]string{"HTGETTOKENOPTS=\"--env-option=value\"", "--env-option2=value2"},
			[]string{},
			[]string{"--env-option=value"},
		},
		{
			"Options without environment",
			[]string{},
			[]string{"--option=value"},
			[]string{"--option=value"},
		},
		{
			"Environment and options combined",
			[]string{"HTGETTOKENOPTS=\"--env-option=value\""},
			[]string{"--option2=value2"},
			[]string{"--env-option=value", "--option2=value2"},
		},
		{
			"Environment and options combined, but with conflicts - passed options should take precedence",
			[]string{"HTGETTOKENOPTS=\"--option1=value1 --env-option=value\""},
			[]string{"--option1=value2", "--option2=value2"},
			[]string{"--env-option=value", "--option2=value2", "--option1=value2"},
		},
		{
			"Environment and options combined, but with conflicts and spaces - passed options should take precedence",
			[]string{"HTGETTOKENOPTS=\"--option1=value1 --env-option=value --option3 --option4 value4\""},
			[]string{"--option1=value2", "--option2=value2"},
			[]string{"--env-option=value", "--option2=value2", "--option1=value2", "--option3", "--option4=value4"},
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				result := mergeHtgettokenopts(test.env, test.options)
				slices.Sort(test.expected)
				slices.Sort(result)
				assert.Equal(t, test.expected, result)
			},
		)
	}
}

func TestPrepareHtgettokenopts(t *testing.T) {
	type testCase struct {
		description string
		options     []string
		expected    []string
	}

	testCases := []testCase{
		{
			"Empty string",
			[]string{""},
			[]string{},
		},
		{
			"Option with value",
			[]string{"--option=value"},
			[]string{"--option=value"},
		},
		{
			"Option without value",
			[]string{"--option"},
			[]string{"--option"},
		},
		{
			"Single-dashed option without value",
			[]string{"-o"},
			[]string{"-o"},
		},
		{
			"Option with space and value",
			[]string{"--option", "value"},
			[]string{"--option=value"},
		},
		{
			"Single-dashed option with value",
			[]string{"-o", "value"},
			[]string{"-o=value"},
		},
		// These are malformed, but we should handle them
		{
			"Option with space and value",
			[]string{"--option value"},
			[]string{"--option=value"},
		},
		{
			"Single-dashed option with value",
			[]string{"-o value"},
			[]string{"-o=value"},
		},
		{
			"Extra spaces",
			[]string{"--option  value ", "--option2=value2"},
			[]string{"--option=value", "--option2=value2"},
		},
		{
			"Mix of options with and without values, with malformed options mixed in",
			[]string{"--option=value", "--option2=value2", "--option3", "value3", "--option4  value4 "},
			[]string{"--option=value", "--option2=value2", "--option3=value3", "--option4=value4"},
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				result := prepareHtgettokenopts(test.options)
				assert.Equal(t, test.expected, result)
			},
		)
	}
}

func TestWithKerberosKeytabAuth(t *testing.T) {
	type testCase struct {
		description         string
		keytab              string
		principal           string
		setupFunc           func(*testing.T) func() // returns a cleanup function
		expectedFunc        func()
		expectedErrContains string
	}

	// Cases:
	// 4. kinit fails to run - set kinit to script that exits 1
	// 5. kinit runs successfully - set kinit to script that exits 0, check cleanup script

	testCases := []testCase{
		{
			"Blank keytab",
			"",
			"principalString",
			nil,
			nil,
			"error setting up kerberos keytab auth: keytab path and principal must be provided for Kerberos authentication",
		},
		{
			"Blank principal",
			"/path/to/keytab",
			"",
			nil,
			nil,
			"error setting up kerberos keytab auth: keytab path and principal must be provided for Kerberos authentication",
		},
		{
			"Can't create tempdir",
			"/path/to/keytab",
			"principalString",
			func(t *testing.T) func() {
				//	 set $TMPDIR to a non-existent directory
				oldTmpDir, ok := os.LookupEnv("TMPDIR")
				temp := t.TempDir()
				badDir := path.Join(temp, "nonexistent")
				os.Mkdir(badDir, 0000)
				os.Setenv("TMPDIR", badDir)
				return func() {
					if ok {
						os.Setenv("TMPDIR", oldTmpDir)
						return
					}
					os.Unsetenv("TMPDIR")
				}
			},
			nil,
			"error setting up kerberos keytab auth: error creating file kerberos cache",
		},
		{
			"kinit doesn't exist in path",
			"/path/to/keytab",
			"principalString",
			func(t *testing.T) func() {
				//	 set $PATH to some temp directory with no kinit executable in it
				oldPath := os.Getenv("PATH")
				temp := t.TempDir()
				os.Setenv("PATH", temp)
				return func() {
					os.Setenv("PATH", oldPath)
				}
			},
			nil,
			"kinit executable not found in PATH",
		},
	}

	for _, test := range testCases {
		t.Run(test.description, func(t *testing.T) {
			t.Log("Running test case:", test.description)
			if test.setupFunc != nil {
				cleanupFunc := test.setupFunc(t)
				t.Log("TMPDIR: ", os.Getenv("TMPDIR"))
				defer func() {
					cleanupFunc()
					t.Log("TMPDIR: ", os.Getenv("TMPDIR"))
				}()
			}
			c := new(htgettokenClient)
			c.withKerberosKeytabAuth(test.keytab, test.principal)
			cleanupFunc, err := c.auth(context.Background())
			if err != nil {
				assert.ErrorContains(t, err, test.expectedErrContains)
				assert.Nil(t, cleanupFunc, "cleanup function should be nil when there is an error")
			}
			// if test.expectedErr == nil {
			// 	assert.Nil(t, err)
			// 	if test.expectedFunc != nil {
			// 		cleanupFunc()
			// 		_, ok := os.LookupEnv("KRB5CCNAME")
			// 		assert.False(t, ok, "KRB5CCNAME should not be set")
			// 	}
			// }
		})
	}
}
