package main

import "time"

var (
	recentDuration time.Duration = time.Duration(30 * time.Hour * 24)
)

// FileEntry is a directory file listing
type FileEntry struct {
	filename    string
	created     time.Time
	isDirectory bool
}

func fileIsRecent(f *FileEntry) bool {
	return now.Sub(f.created) < recentDuration
}
