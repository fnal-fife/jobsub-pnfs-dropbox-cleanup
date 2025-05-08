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
	// schedd           = "jobsub03.fnal.gov"
)

var exptNameOverride = map[string]string{
	"gm2": "GM2",
}

func main() {
	ctx := context.Background()
	// Get token
	cmdArgs := []string{
		"-a",
		"htvaultprod.fnal.gov",
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

	source := "https://fndcadoor.fnal.gov:2880/" + exptArea + "/resilient/jobsub_stage/"
	files, err := client.getFilesTree(ctx, source, nil)
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

	// TODO Note - if we exceed file limit, we may have directory that actually has files, but we didn't register them as entries.  We should make sure to
	// not crash out if that's the case, and just continue so the next run can clear them out.  Maybe we return an error if the directory is not empty

	for _, file := range files {
		fmt.Printf("File entry:%s\n\n", file.String())
	}

	fmt.Println("Are the files not recent?")

	for _, file := range files {
		fmt.Printf("Filename: %s, isRecent:%t\n", file.Name(), fileIsRecent(file))
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
