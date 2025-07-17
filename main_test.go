package main

import (
	"log/slog"
	"os"
	"testing"
)

// TODO
// FileAccessor interface - arg to GetDropboxFiles() func that returns ([]FileEntry, error).  Constructor to FileAccessor should take pathOrURL string arg
// * Test that checks *condorSchedd.queryJobsList
// * Test that checks *gfalList.getFilesList
// * Test that checks *gfalList.fileListingToFileEntry

func TestMain(m *testing.M) {
	// Setup code here if needed
	logger = slog.New(slog.NewTextHandler(os.Stdout, nil)) // Dummy logger for tests
	exitCode := m.Run()
	os.Exit(exitCode)
}
