package main

import (
	"bytes"
	"context"
	"errors"
	"io"
)

type FileAccessor interface {
	getFilesList(ctx context.Context, source string) ([][]byte, error)
	fileListingToFileEntry(line io.Reader) (FileEntry, error)
	// removeFile(urlOrPath string) error
	// removeDir(urlOrPath string) error
}

// GetDropboxFiles uses a FileAccessor to provide a slice of the files present at the path or URL given by the source string
func GetDropboxFiles(ctx context.Context, f FileAccessor, source string) ([]FileEntry, error) {
	fileListings, err := f.getFilesList(ctx, source)
	if err != nil {
		return nil, err
	}

	fileEntries := make([]FileEntry, 0, len(fileListings))
	for _, listing := range fileListings {
		if entry, err := f.fileListingToFileEntry(bytes.NewReader(listing)); err != nil {
			// TODO log error
			continue
		} else {
			fileEntries = append(fileEntries, entry)
		}
	}

	if len(fileListings) != 0 && len(fileEntries) == 0 {
		return nil, errors.New("there was an error processing the file listings into file entries.  No file entries were generated")
	}
	return fileEntries, nil
}
