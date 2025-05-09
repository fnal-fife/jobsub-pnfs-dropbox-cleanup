package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	files, err := client.getFilesTree(ctx, source, nil, nil)
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
	flattenedFileEntries := flattenEntryTree(files) // TODO If performance suffers, throw out files after this executes. We shouldn't need files anymore after this

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
