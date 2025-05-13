package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var now = time.Now()

// Vars we will eventually configure in a config file
var (
	experiment       = "gm2"
	scheddConstraint = "IsJobsubLite == true && InDowntime == false"
	// schedd           = ***REMOVED***
)

var exptNameOverride = map[string]string{
	"gm2": "GM2",
}

func main() {
	ctx := context.Background()
	// Get token
	slog.Debug("Ensuring token is available", "experiment", experiment)
	cmdArgs := []string{
		"-a",
		***REMOVED***,
		"-i",
		experiment,
	}
	cmd := exec.CommandContext(ctx, "htgettoken", cmdArgs...)
	err := cmd.Run()
	// stdoutStderr, err := cmd.CombinedOutput()
	// fmt.Println("Command output:", string(stdoutStderr))
	if err != nil {
		slog.Error("error running htgettoken", "error", err)
		// Handle error
		return
	}

	// Get files
	addedEnvironment := []string{
		"BEARER_TOKEN_FILE=/run/user/10610/bt_u10610",
	}
	tokenBytes, err := os.ReadFile("/run/user/10610/bt_u10610")
	if err != nil {
		slog.Error("error reading token file", "error", err)
		// Handle error
		return
	}
	tokenString := string(tokenBytes)
	addedEnvironment = append(addedEnvironment, "BEARER_TOKEN="+tokenString)

	client := &gfal2Client{
		addedEnvironment: addedEnvironment,
	}

	exptArea := experiment
	if override, ok := exptNameOverride[experiment]; ok {
		exptArea = override
	}

	source := ***REMOVED***
	slog.Debug("Getting files list", "source", source)
	filesTree, err := client.getFilesTree(ctx, source, nil, nil)
	switch {
	case errors.Is(err, errFileCountLimitExceeded):
		slog.Error("file count limit exceeded. Stopping collecting files now")
	case err != nil:
		slog.Error("error getting files list", "error", err)
		return
	default:
		// Nil error
		// TODO - do we need the default case?
	}

	// TODO combine this line into fileMap creating line like for _, file := range flattenEntryTree(files) {....
	flattenedFileEntries := flattenEntryTree(filesTree) // TODO If performance suffers, throw out files after this executes. We shouldn't need files anymore after this

	// TODO Note - if we exceed file limit, we may have directory that actually has files, but we didn't register them as entries.  We should make sure to
	// not crash out if that's the case, and just continue so the next run can clear them out.  Maybe we return an error if the directory is not empty

	// TODO DEBUG.  Don't need to make this code better, because it's going away.  We just want to go down two levels
	for _, file := range flattenedFileEntries {
		slog.Debug("File entry", "file", file.Name())
		// level := 0
		// fmt.Printf("File entry, level %d:%s\n\n", level, file)
		// if len(file.containsFiles) > 0 {
		// 	level++
		// 	for _, subFile := range file.containsFiles {
		// 		fmt.Printf("File entry, level %d:%s\n\n", level, subFile)
		// 		if len(subFile.containsFiles) > 0 {
		// 			level++
		// 			for _, subSubFile := range subFile.containsFiles {
		// 				fmt.Printf("File entry, level %d:%s\n\n", level, subSubFile)
		// 			}
		// 			level--
		// 		}
		// 		level--
		// 	}
		// }
	}

	// Create a file map to hold the filenames and quickly eliminate files we don't want to delete
	fileMap := make(fileEntryMap, 0)
	for _, file := range flattenedFileEntries {
		fileMap[file.Name()] = file
	}

	// for _, file := range files {
	// 	fmt.Printf("File entry:%s\n\n", file.String())
	// }

	// for _, file := range files {
	// 	fmt.Printf("Filename: %s, isRecent:%t\n", file.Name(), fileIsRecent(file))
	// }

	// Now, we need to get the condor job files
	slog.Debug("Getting condor job files")
	jobFiles := make(map[string]struct{}, 0)
	schedds, err := getCondorSchedds(ctx, scheddConstraint)
	if err != nil {
		// TODO Handle error
		slog.Error("error getting condor schedds:", "error", err)
		return
	}
	if len(schedds) == 0 {
		slog.Error("no condor schedds found. Exiting")
		return
	}

	for _, sch := range schedds {
		ads, err := sch.getPNFSJobsForExperiment(ctx, experiment)
		if err != nil {
			// Handle error
			slog.Error("error getting PNFS jobs:", "error", err, "schedd", sch.name)
			continue
		}
		for _, ad := range ads {
			scheddFiles, err := sch.getDropboxFilesFromJob(ad)
			if err != nil {
				// Handle error
				slog.Error("error getting dropbox files from job:", "error", err, "schedd", sch.name) // TODO Get job ID?
				continue
			}
			for _, file := range scheddFiles {
				jobFiles[file] = struct{}{}
			}
		}
	}

	slog.Debug("", "jobFiles", jobFiles) // TODO

	// Remove any files from our delete list that are in the list of job files or are recent
	// We are iterating a second time to check if the files are recent, which may not be totally efficient, but it should improve readability
	// Maybe if we have performance problems, we first get the list of job files, then pass in a filter function to our tree-builder that could check
	// for recency or job file membership
	slog.Debug("Are the files not recent or being used by condor jobs?")
	for _, entry := range fileMap {
		func(file *FileEntry) {
			removeFileAndAncestorsFromDeleteList := func() {
				delete(fileMap, file.Name())
				parent := file.parent
				for parent != nil {
					slog.Debug("Removing parent from deletion list", "fileEntry.parent", parent.Name())
					delete(fileMap, parent.Name())
					parent = parent.parent
				}
			}
			if _, ok := jobFiles[file.Name()]; ok {
				slog.Debug("File is in job files, so we will not delete it:", "filename", file.Name())
				removeFileAndAncestorsFromDeleteList()
				return
			}
			if fileIsRecent(file) {
				slog.Debug("File is recent, so we will not delete it:", "filename", file.Name())
				removeFileAndAncestorsFromDeleteList()
			}
		}(entry)
	}

	// TODO DEBUG
	slog.Debug("Remaining files to delete:")
	for name := range fileMap {
		slog.Debug("", "filename", name)
	}

	// TODO Need to check if directory is empty before deleting it

	// TODO this took a ton of memory. Let's do this the "dumber" way and see if it works better that way
	// Recursively walk the tree and delete files if they're in our list to delete
	// deletedFiles := make([]string, 0, len(fileMap))
	// for _, entry := range filesTree {
	// 	if len(deletedFiles) == len(fileMap) {
	// 		fmt.Println("All files deleted.  Stopping.")
	// 		break
	// 	}

	// 	_deletedFiles, err := tryDeleteFilesRecursively(ctx, entry, client, fileMap, deletedFiles)
	// 	if err != nil {
	// 		var testErr *errDeleteFiles
	// 		if errors.As(err, &testErr) && len(_deletedFiles) != 0 {
	// 			// Some worked, some didn't
	// 			fmt.Println("Some files were deleted, some were not.  Continuing:")
	// 			continue
	// 		}
	// 		// TODO Handle error
	// 		fmt.Println("error deleting files recursively:", err)
	// 		continue
	// 	}
	// 	deletedFiles = append(deletedFiles, _deletedFiles...)
	// 	// For all of our deleted files , we need to remove them from the fileMap
	// }

	// Note:  This isn't as slick as recursion, but the former used way more memory, and actually made the program get killed by the OOM killer
	// Do a pass where we start with deleting files, then their parents if they're empty
	slog.Info("Deleting files")
	deletedFilenames := make([]string, 0)
	// for filename := range maps.Keys(fileMap) {
	for filename := range fileMap.AllNonDirFilesNames() {
		// if fileMap[filename].isDirectory {
		// 	fmt.Println("Skipping directory", filename)
		// 	continue
		// }
		slog.Debug("Deleting file:", filename)
		// Remove the file
		err := client.removeFile(ctx, PNFSToHTTPS(filename, stripPNFSFromPath), false)
		if err != nil {
			// TODO Handle error
			slog.Error("error deleting file", "error", err)
			continue
		}
		// Remove the file from our map and from its parent's containsFiles slice
		if fileMap[filename].parent != nil {
			fileMap[filename].parent.containsFiles = slices.DeleteFunc(
				fileMap[filename].parent.containsFiles,
				func(f *FileEntry) bool {
					return f.Name() == filename
				},
			)
		}
		deletedFilenames = append(deletedFilenames, filename)
		slog.Info("File deleted", "filename", filename)
	}

	for _, filename := range deletedFilenames {
		delete(fileMap, filename)
	}

	slog.Info("Deleting empty directories")
	deletedFilenames = make([]string, 0)
	// for filename := range maps.Keys(fileMap) {
	for filename := range fileMap.AllDirNames() {
		if len(fileMap[filename].containsFiles) != 0 {
			slog.Info("Directory is not empty, so we will not delete it:", "dirName", filename)
			continue
		}

		slog.Debug("Deleting directory", "dirName", filename)
		// Remove the file
		err := client.removeFile(ctx, PNFSToHTTPS(filename, stripPNFSFromPath), true)
		if err != nil {
			// TODO Handle error
			slog.Error("error deleting directory", "error", err)
			continue
		}

		deletedFilenames = append(deletedFilenames, filename)
		slog.Info("Empty directory deleted", "dirName", filename)

		// Keep walking up the tree and deleting empty directories recursively
		// Remove the file from our map and from its parent's containsFiles slice
		_parent := fileMap[filename].parent
		for _parent != nil {
			_parent.containsFiles = slices.DeleteFunc(
				_parent.containsFiles,
				func(f *FileEntry) bool {
					return f.Name() == filename
				},
			)
			// Check if the parent is empty. If not, we can stop
			if len(_parent.containsFiles) != 0 {
				slog.Debug("Parent is not empty, so we will not delete it", "dirName", _parent.Name())
				break
			}
			// Delete parent directory, since we've established that it's empty
			slog.Debug("Parent is empty, so we will delete it", "dirName", _parent.Name())
			err := client.removeFile(ctx, PNFSToHTTPS(filename, stripPNFSFromPath), true)
			if err != nil {
				// TODO Handle error
				slog.Error("error deleting directory", "error", err)
				break
			}
			deletedFilenames = append(deletedFilenames, filename)
			_parent = _parent.parent
		}
	}

	// Save some memory
	for _, filename := range deletedFilenames {
		delete(fileMap, filename)
	}

	// entries, err := client.parseOutputToFileEntries(ctx, out)
	// if err != nil {
	// 	fmt.Println("error parsing output to file entries:", err)
	// 	// Handle error
	// 	return
	// }

	// Check files
	// walkDirFunc := func(path string, info os.FileInfo, err error) error {

	// for _, entry := range entries {
	// 	if entry != nil {
	// 		fmt.Printf("File entry:\nname:%s\ndate:%s\nisDir:%t", entry.filename, entry.created, entry.isDirectory)
	// 		if entry.isDirectory {
	// 			fmt.Println("Looking inside directory")
	// 			source1: =
	// 	}
	// }
}

/*
1) Have vault tokens provided by managed tokens?
2) htgettoken for bearer token (set -o flag to save it somewhere else)
3) gfal-ls -l to get list of dirs (NOTE:  Need to use BEARER_TOKEN, not BEARER_TOKEN_FILE)
4) for each dir in (3), output looks like:
```
-bash-4.2$ BEARER_TOKEN=`cat /run/user/10610/bt_u10610` gfal-ls -l  https://fndcadoor.fnal.gov:2880/GM2/resilient/jobsub_stage/5a48ca5816558220979fc6220cb93520b5ef89ed60108c45220327c0de1097f8/
-rwxrwxrwx   0 0     0            50 Sep 26 14:55 bogus_file.out
```

Dir output:
```
drwxrwxrwx   0 0     0             0 Apr  6  2023 bogus_dir
```

5) For each schedd, `condor_q -constraint 'Jobsub_Group=="<experiment>"'  -af PNFS_INPUT_FILES` to get job files (could be comma-separated list)
6) If (3) is too new, discard
7) If (3) is in (5), discard
8) Anything that's left, gfal-ls (3).
9) gfal-rm results from (8)
10) gfal-rm dir in (8)
*/

// "-rwxrwxrwx   0 0     0            50 Sep 26 14:55 bogus_file.out"
// "drwxrwxrwx   0 0     0             0 Apr  6  2022 bogus_dir"

// TODO Move this to a different file
type fileEntryMap map[string]*FileEntry

// TODO Move this to a different file
// TODO Write tests for this
func flattenEntryTree(entries []*FileEntry) []*FileEntry {
	flatEntries := make([]*FileEntry, 0)
	for _, entry := range entries {
		flatEntries = append(flatEntries, entry)
		if len(entry.containsFiles) > 0 {
			flatEntries = append(flatEntries, flattenEntryTree(entry.containsFiles)...)
		}
	}
	return flatEntries
}

// TODO move somewhere else

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

// TODO Move somewhere else
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

// TODO Move somewhere else
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

// TODO Move somewhere else
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

// TODO Move this to a different file
func urlToFilename(URL string, filenameTransformFunc func(string) string) string {
	sourceURL, err := url.Parse(URL)
	if err != nil {
		// TODO Handle error
		slog.Error("error parsing URL", "error", err)
		return ""
	}
	return filenameTransformFunc(sourceURL.Path)
}

// TODO Move this to a different file
func prependPNFSToPath(urlPath string) string {
	// TODO this should get fed by configuration
	return filepath.Join("/pnfs", urlPath)
}

func stripPNFSFromPath(pnfsPath string) string {
	// parts := filepath.SplitList(pnfsPath)
	// // TODO this should get fed by configuration
	// fmt.Println("Parts:", parts)
	// if parts[0] != "/pnfs" {
	// 	return ""
	// }
	// return "/" + strings.Join(parts[1:], "/")
	// TODO see if there's a better way than this
	return strings.TrimPrefix(pnfsPath, "/pnfs")
}

// TODO Move this to a different file
func PNFSToHTTPS(pnfsPath string, filenameTransformFunc func(string) string) string {
	// TODO this should get fed by configuration
	u, err := url.Parse(***REMOVED***)
	if err != nil {
		// TODO Handle error
		slog.Error("error parsing URL", "error", err)
		return ""
	}
	return u.JoinPath(filenameTransformFunc(pnfsPath)).String()
}

// TODO Move this to a different file
// Returns list of deleted files
// TODO Implement this
// func deleteEmptyDirectoriesAndAncestors(ctx context.Context, client *gfal2Client) error {
// 	// TODO Implement this
// 	return nil
// }

// TODO Move this to a different file
// Check membership in deleteMap.  Maybe we pass this in as a dynamic filter function in a future version to make it more flexible and testable
func tryDeleteFilesRecursively(ctx context.Context, entry *FileEntry, client *gfal2Client, deleteMap fileEntryMap, prevDeletedFiles []string) ([]string, error) {
	// TODO DEBUG
	fmt.Println("Trying to delete files recursively.  Entry:", entry.Name())
	// END DEBUG

	var retErr *errDeleteFiles
	errFiles := make([]string, 0)

	// Cases
	if entry.isDirectory {
		// Case: If the entry is a directory and not empty, recursively call this function on each of its children
		if len(entry.containsFiles) != 0 {
			for _, childFile := range entry.containsFiles {
				deletedFiles, err := tryDeleteFilesRecursively(ctx, childFile, client, deleteMap, prevDeletedFiles)
				if err != nil {
					var testErr *errDeleteFiles
					// TODO Handle error properly. Should use errDeleteFiles
					fmt.Println("error deleting files recursively. Will continue:", err)
					if errors.As(err, &testErr) {
						errFiles = append(errFiles, err.(*errDeleteFiles).files...)
					}
					continue
				}

				prevDeletedFiles = append(prevDeletedFiles, deletedFiles...)
			}

			if len(errFiles) != 0 {
				retErr = &errDeleteFiles{files: errFiles}
			}
			if len(entry.containsFiles) != 0 {
				fmt.Println("Not all files within this directory were deleted successfully. Will move to next entry:", entry.Name())
				return prevDeletedFiles, retErr
			}
		}

		entry.containsFiles = nil // Nil this out so that the GC can clean it up and reclaim memory

		// Case: If the entry is a directory and empty, delete it if it is in the deleteMap. This case also covers if we had a non-empty directory
		// that we deleted all the files from
		if _, ok := deleteMap[entry.Name()]; !ok {
			fmt.Println("Directory is not in deleteMap, so we will not delete it:", entry.Name())
			return prevDeletedFiles, nil
		}

		fmt.Println("Deleting empty directory:", entry.Name())
		err := client.removeFile(ctx, PNFSToHTTPS(entry.Name(), stripPNFSFromPath), true)
		if err != nil {
			// TODO Handle error
			fmt.Println("error deleting empty directory:", err)
			errFiles = append(errFiles, entry.Name())
			return prevDeletedFiles, &errDeleteFiles{files: errFiles}
		}
		fmt.Println("Deleted empty directory:", entry.Name())
		// Remove the directory from parent's containsFiles slice
		// TODO maybe make this a function or method
		if entry.parent != nil {
			entry.parent.containsFiles = slices.DeleteFunc(
				entry.parent.containsFiles,
				func(f *FileEntry) bool {
					return f.Name() == entry.Name()
				},
			)
		}
		if len(errFiles) != 0 {
			return prevDeletedFiles, &errDeleteFiles{files: errFiles}
		}
		return prevDeletedFiles, nil
	}

	// Base case - if the entry is a file, delete it

	// Don't delete the file if it's not in the deleteMap
	if _, ok := deleteMap[entry.Name()]; !ok {
		fmt.Println("Directory is not in deleteMap, so we will not delete it:", entry.Name())
		return prevDeletedFiles, nil
	}

	fmt.Println("Deleting file:", entry.Name())
	err := client.removeFile(ctx, PNFSToHTTPS(entry.Name(), stripPNFSFromPath), false)
	if err != nil {
		// TODO Handle error
		fmt.Println("error deleting file:", err)
		return nil, &errDeleteFiles{files: []string{entry.Name()}}
	}
	// File was deleted successfully.  Remove it from parent's containsFiles slice
	if entry.parent != nil {
		slices.DeleteFunc(
			entry.parent.containsFiles,
			func(f *FileEntry) bool {
				return f.Name() == entry.Name()
			},
		)
	}

	prevDeletedFiles = append(prevDeletedFiles, entry.Name())
	return prevDeletedFiles, nil
}

type errDeleteFiles struct {
	files []string
}

func (e *errDeleteFiles) Error() string {
	return fmt.Sprintf("Could not delete files: %v", e.files)
}
