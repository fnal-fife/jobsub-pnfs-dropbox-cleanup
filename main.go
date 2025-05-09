package main

import (
	"context"
	"errors"
	"fmt"
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
		fmt.Println("error running htgettoken:", err)
		// Handle error
		return
	}

	// Get files
	addedEnvironment := []string{
		"BEARER_TOKEN_FILE=/run/user/10610/bt_u10610",
	}
	tokenBytes, err := os.ReadFile("/run/user/10610/bt_u10610")
	if err != nil {
		fmt.Println("error reading token file:", err)
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
	filesTree, err := client.getFilesTree(ctx, source, nil, nil)
	switch {
	case errors.Is(err, errFileCountLimitExceeded):
		fmt.Println("file count limit exceeded. Stopping collecting files now")
	case err != nil:
		fmt.Println("error getting files list:", err)
		// Handle error
		return
	default:
		// Nil error
	}

	// TODO combine this line into fileMap creating line like for _, file := range flattenEntryTree(files) {....
	flattenedFileEntries := flattenEntryTree(filesTree) // TODO If performance suffers, throw out files after this executes. We shouldn't need files anymore after this

	// TODO Note - if we exceed file limit, we may have directory that actually has files, but we didn't register them as entries.  We should make sure to
	// not crash out if that's the case, and just continue so the next run can clear them out.  Maybe we return an error if the directory is not empty

	// TODO DEBUG.  Don't need to make this code better, because it's going away.  We just want to go down two levels
	for _, file := range flattenedFileEntries {
		fmt.Printf("File entry:%s\n\n", file)
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
	fmt.Println("Getting condor job files")
	jobFiles := make(map[string]struct{}, 0)
	schedds, err := getCondorSchedds(ctx, scheddConstraint)
	if err != nil {
		// TODO Handle error
		fmt.Println("error getting condor schedds:", err)
		return
	}

	// sch := &CondorSchedd{name: schedd}
	for _, sch := range schedds {
		ads, err := sch.getPNFSJobsForExperiment(ctx, experiment)
		if err != nil {
			// Handle error
			fmt.Println("error getting PNFS jobs:", err)
			continue
		}
		for _, ad := range ads {
			scheddFiles, err := sch.getDropboxFilesFromJob(ad)
			if err != nil {
				// Handle error
				fmt.Println("error getting dropbox files from job:", err)
				continue
			}
			for _, file := range scheddFiles {
				jobFiles[file] = struct{}{}
			}
		}
	}

	fmt.Println("Job files:", jobFiles) // TODO

	// Remove any files from our delete list that are in the list of job files or are recent
	// We are iterating a second time to check if the files are recent, which may not be totally efficient, but it should improve readability
	// Maybe if we have performance problems, we first get the list of job files, then pass in a filter function to our tree-builder that could check
	// for recency or job file membership
	fmt.Println("Are the files not recent or being used by condor jobs?")
	for _, entry := range fileMap {
		func(file *FileEntry) {
			removeFileAndAncestorsFromDeleteList := func() {
				delete(fileMap, file.Name())
				parent := file.parent
				for parent != nil {
					fmt.Println("Removing parent from deletion list:", parent.Name())
					delete(fileMap, parent.Name())
					parent = parent.parent
				}
			}
			if _, ok := jobFiles[file.Name()]; ok {
				fmt.Println("File is in job files, so we will not delete it:", file.Name())
				removeFileAndAncestorsFromDeleteList()
				return
			}
			if fileIsRecent(file) {
				fmt.Println("File is recent, so we will not delete it:", file.Name())
				removeFileAndAncestorsFromDeleteList()
			}
		}(entry)
	}

	// TODO DEBUG
	fmt.Println("Remaining files to delete:")
	for name := range fileMap {
		fmt.Printf("File name:%s\n", name)
	}

	// TODO Need to check if directory is empty before deleting it

	// Recursively walk the tree and delete files if they're in our list to delete
	deletedFiles := make([]string, 0, len(fileMap))
	for _, entry := range filesTree {
		if len(deletedFiles) == len(fileMap) {
			fmt.Println("All files deleted.  Stopping.")
			break
		}

		_deletedFiles, err := tryDeleteFilesRecursively(ctx, entry, client, fileMap, deletedFiles)
		switch {
		// We have an error, but we can continue

		}
		if err != nil {
			var testErr *errDeleteFiles
			if errors.As(err, &testErr) && len(_deletedFiles) != 0 {
				// Some worked, some didn't
				fmt.Println("Some files were deleted, some were not.  Continuing:")
				continue
			}
			// TODO Handle error
			fmt.Println("error deleting files recursively:", err)
			continue
		}
		deletedFiles = append(deletedFiles, _deletedFiles...)
		// For all of our deleted files , we need to remove them from the fileMap
	}

	// Maybe don't use this

	// // Do a pass where we start with deleting files, then their parents if they're empty
	// for filename := range maps.Keys(fileMap) {
	// 	if fileMap[filename].isDirectory {
	// 		fmt.Println("Skipping directory:", filename)
	// 		continue
	// 	}
	// 	fmt.Println("Deleting file:", filename)
	// 	// Remove the file
	// 	err := client.removeFile(ctx, PNFSToHTTPS(filename, stripPNFSFromPath), false)
	// 	if err != nil {
	// 		// TODO Handle error
	// 		fmt.Println("error deleting file:", err)
	// 		continue
	// 	}
	// 	// Remove the file from our map and from its parent's containsFiles slice
	// 	slices.DeleteFunc(
	// 		fileMap[filename].parent.containsFiles,
	// 		func(f *FileEntry) bool {
	// 			return f.Name() == filename
	// 		},
	// 	)
	// 	// TODO implement thing where we delete parent and ancestors if it's empty
	// 	delete(fileMap, filename)
	// 	fmt.Println("File deleted:", filename)
	// }

	// // TODO Do a pass where we try to delete empty directories

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

// TODO Move this to a different file
func urlToFilename(URL string, filenameTransformFunc func(string) string) string {
	sourceURL, err := url.Parse(URL)
	if err != nil {
		// TODO Handle error
		fmt.Println("error parsing URL:", err)
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
	// TODO Make this better
	u, err := url.Parse(***REMOVED***)
	if err != nil {
		// TODO Handle error
		fmt.Println("error parsing URL:", err)
		return ""
	}
	// fmt.Println("URL String, ", u.String())
	// fmt.Println("PNFS path, ", pnfsPath)
	// return ***REMOVED*** + filenameTransformFunc(pnfsPath)
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
			slices.DeleteFunc(
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
