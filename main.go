package main

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

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

// TODO rename this
var lineRegex = regexp.MustCompile(`((?:\w|-)+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\w+\s+\d+\s+(?:(?:\d+:\d+)|\d+))\s+(.+)`)

var (
	dateWithTimeNoYearLayout string        = "Jan  2 15:04"
	dateWithYearLayout       string        = "Jan 2 2006"
	now                                    = time.Now()
	recentDuration           time.Duration = time.Duration(30 * time.Hour * 24)
)

// FileEntry is a directory file listing
type FileEntry struct {
	filename    string
	created     time.Time
	isDirectory bool
}

func scanDropboxLineToFileEntry(line string) (*FileEntry, error) {
	var err error
	lineParts := lineRegex.FindStringSubmatch(line)
	if lineParts == nil {
		return nil, ErrParseLine
	}

	f := &FileEntry{filename: lineParts[7]}
	perms := lineParts[1]
	dateString := lineParts[6]

	f.isDirectory, err = parsePermsToDirectoryFlag(perms)
	if err != nil {
		return nil, ErrParseLine
	}

	f.created, err = parseDateStampToTime(dateString)
	if err != nil {
		return nil, ErrParseLine
	}

	return f, nil
}

func parsePermsToDirectoryFlag(perms string) (bool, error) {
	if len(perms) != 10 {
		return false, ErrMalformedPerms
	}

	validPrefixes := []string{"d", "-"}
	if !slices.Contains(validPrefixes, string(perms[0])) {
		return false, ErrMalformedPerms
	}

	if strings.HasPrefix(perms, "d") {
		return true, nil
	}
	return false, nil
}

func parseDateStampToTime(dateString string) (time.Time, error) {
	var rawDateStamp time.Time
	var err error
	// See if our dateString matches the "Jan  2 15:04 format"
	rawDateStamp, err = time.ParseInLocation(dateWithTimeNoYearLayout, dateString, time.Local)
	if err == nil {
		// We succeeded at parsing this time, so the year will be 0000.  Add the current year on.
		yearDateStamp := rawDateStamp.AddDate(now.Year(), 0, 0)
		if yearDateStamp.After(now) {
			// We're in the future, so subtract a year
			return yearDateStamp.AddDate(-1, 0, 0), nil
		}
		return yearDateStamp, nil
	}
	// The previous parsing attempt failed, so we must be in the "Jan 2 2006" format
	rawDateStamp, err = time.ParseInLocation(dateWithYearLayout, dateString, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	return rawDateStamp, nil
}

func fileIsRecent(f *FileEntry) bool {
	return now.Sub(f.created) < recentDuration
}

var (
	ErrParseLine      = errors.New("could not parse line")
	ErrMalformedPerms = errors.New("perms string is malformed")
)
