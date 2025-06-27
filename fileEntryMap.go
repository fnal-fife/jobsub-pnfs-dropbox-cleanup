package main

import "iter"

type fileEntryMap map[string]*FileEntry

// Iterator to return all directory entries' names
func (f fileEntryMap) AllDirNames() iter.Seq[string] {
	return func(yield func(string) bool) {
		for name := range f {
			if f[name].isDirectory {
				if !yield(name) {
					return
				}
			}
		}
	}
}

// Iterator to return all directory entries
func (f fileEntryMap) AllDirs() iter.Seq2[string, *FileEntry] {
	return func(yield func(string, *FileEntry) bool) {
		for name, entry := range f {
			if entry.isDirectory {
				if !yield(name, entry) {
					return
				}
			}
		}
	}
}

// Iterator to return all non-directory entries
func (f fileEntryMap) AllNonDirFilesNames() iter.Seq[string] {
	return func(yield func(string) bool) {
		for name := range f {
			if !f[name].isDirectory {
				if !yield(name) {
					return
				}
			}
		}
	}
}

// Iterator to return all non-directory entries
func (f fileEntryMap) AllNonDirFiles() iter.Seq2[string, *FileEntry] {
	return func(yield func(string, *FileEntry) bool) {
		for name, entry := range f {
			if !entry.isDirectory {
				if !yield(name, entry) {
					return
				}
			}
		}
	}
}
