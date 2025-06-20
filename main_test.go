package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TODO
// FileAccessor interface - arg to GetDropboxFiles() func that returns ([]FileEntry, error).  Constructor to FileAccessor should take pathOrURL string arg
// * Test that checks *condorSchedd.queryJobsList
// * Test that checks *gfalList.getFilesList
// * Test that checks *gfalList.fileListingToFileEntry

func TestGetConfigFilePath(t *testing.T) {
	// expectedPath := "/etc/jobsub-pnfs-dropbox-cleanup/jobsub-pnfs-dropbox-cleanup.yml"
	t1 := t.TempDir()
	t2 := t.TempDir()
	standardDirsToCheck := []string{t1, t2}
	configFileName := "jobsub-pnfs-dropbox-cleanup.yml"

	type testCase struct {
		description    string
		configFileFlag string
		checkDirs      []string
		setupFunc      func()
		cleanupFunc    func()
		expectedPath   string
		expectedErr    error
	}

	testCases := []testCase{
		{
			description:    "Config file flag given with valid path",
			configFileFlag: filepath.Join(t1, configFileName),
			checkDirs:      standardDirsToCheck,
			setupFunc: func() {
				os.Create(filepath.Join(t1, configFileName))
			},
			cleanupFunc: func() {
				os.Remove(filepath.Join(t1, configFileName))
			},
			expectedPath: filepath.Join(t1, configFileName),
			expectedErr:  nil,
		},
		{
			description:    "Config file flag given with an invalid path",
			configFileFlag: filepath.Join(t1, configFileName),
			checkDirs:      standardDirsToCheck,
			setupFunc:      func() {},
			cleanupFunc:    func() {},
			expectedPath:   "",
			expectedErr:    errNoConfigFileFound,
		},
		{
			description:    "Config file flag given with an invalid path, but valid config file in standard directories",
			configFileFlag: filepath.Join(t1, configFileName),
			checkDirs:      standardDirsToCheck,
			setupFunc: func() {
				os.Create(filepath.Join(t2, configFileName))
			},
			cleanupFunc: func() {
				os.Remove(filepath.Join(t2, configFileName))
			},
			expectedPath: filepath.Join(t2, configFileName),
			expectedErr:  nil,
		},
		{
			description:    "No config file flag given, but valid config file in standard directories",
			configFileFlag: "",
			checkDirs:      standardDirsToCheck,
			setupFunc: func() {
				os.Create(filepath.Join(t2, configFileName))
			},
			cleanupFunc: func() {
				os.Remove(filepath.Join(t2, configFileName))
			},
			expectedPath: filepath.Join(t2, configFileName),
			expectedErr:  nil,
		},
		{
			description:    "No valid config files found",
			configFileFlag: "",
			checkDirs:      standardDirsToCheck,
			setupFunc:      func() {},
			cleanupFunc:    func() {},
			expectedPath:   "",
			expectedErr:    errNoConfigFileFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.setupFunc()
			defer tc.cleanupFunc()

			path, err := getConfigFilePath(tc.configFileFlag, tc.checkDirs)
			if err != tc.expectedErr {
				t.Errorf("Expected error %v, got %v", tc.expectedErr, err)
			}
			if path != tc.expectedPath {
				t.Errorf("Expected path %s, got %s", tc.expectedPath, path)
			}
		},
		)
	}

}
