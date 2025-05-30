package main

import (
	"fmt"
	"io/fs"
	"time"
)

var (
	defaultRecentDuration time.Duration = time.Duration(30 * time.Hour * 24)
)

// FileEntry is a directory file listing. It implements the fs.DirEntry interface
type FileEntry struct {
	filename      string
	created       time.Time
	isDirectory   bool
	containsFiles []*FileEntry
	parent        *FileEntry
}

// fileIsRecent checks if the file is recent based on the provided ageCutoff. To use the default recent duration, pass a zero or negative value for ageCutoff.
func fileIsRecent(f *FileEntry, ageCutoff time.Duration) bool {
	if ageCutoff <= 0 {
		ageCutoff = defaultRecentDuration
	}
	return now.Sub(f.created) < ageCutoff
}

func (f *FileEntry) Name() string {
	return f.filename
}

func (f *FileEntry) IsDir() bool {
	return f.isDirectory
}

func (f *FileEntry) Type() fs.FileMode {
	// DUMMY.  TODO implement this better later
	return f.Mode()
}

func (f *FileEntry) Info() (fs.FileInfo, error) {
	// TODO implement this better later
	return f, nil
}

func (f *FileEntry) Size() int64 {
	// TODO implement this better later
	return 0
}

func (f *FileEntry) Mode() fs.FileMode {
	if f.isDirectory {
		return fs.ModeDir
	}
	return 0
}

func (f *FileEntry) ModTime() time.Time {
	return f.created
}

func (f *FileEntry) Sys() any {
	return nil
}

func (f *FileEntry) String() string {
	parentName := "nil"
	if f.parent != nil {
		parentName = f.parent.Name()
	}
	return fmt.Sprintf("name:%s\tdate:%s\tisDir:%t\tcontainsFiles:%v\tparentName:%s\n", f.filename, f.created, f.isDirectory, f.containsFiles, parentName)
}
