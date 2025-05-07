package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

var lineRegex = regexp.MustCompile(`((?:\w|-)+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\w+\s+\d+\s+(?:(?:\d+:\d+)|\d+))\s+(.+)`)

var (
	dateWithTimeNoYearLayout string = "Jan  2 15:04"
	dateWithYearLayout       string = "Jan 2 2006"
)

// This should probably have an authenticator (token or proxy?)
type gfal2Client struct {
	addedEnvironment []string
}

// TODO Move this inside getFilesTree
var (
	totalFileCountLimit uint = 50
	fileCountLeft       uint = totalFileCountLimit
)

// TODO: implement this
// Recursive
func (g *gfal2Client) getFilesTree(ctx context.Context, source string, dirContents []*FileEntry) ([]*FileEntry, error) {
	// Setup environment
	environ := os.Environ()
	environ = append(environ, g.addedEnvironment...)

	// environ := append(os.Environ(), "BEARER_TOKEN=)

	// Run command
	cmdArgs := []string{"-l", source}

	c := exec.CommandContext(ctx, "gfal-ls", cmdArgs...)
	c.Env = environ

	fmt.Println("Running command:", c.String())
	stdoutStderr, err := c.CombinedOutput()
	// fmt.Println("Command output:", string(stdoutStderr))
	if err != nil {
		// Handle error
		fmt.Println("Error running command:", err)
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(stdoutStderr))
	scanner.Split(bufio.ScanLines) // Tokenize by line

	errors := make([]error, 0)

	for scanner.Scan() {
		// fmt.Println("File count left:", fileCountLeft)
		// fmt.Println("Length of dirContents:", len(dirContents))
		if fileCountLeft == 0 {
			// TODO This should be a specific error
			return dirContents, nil
		}
		fileCountLeft--
		line := scanner.Text()
		// fmt.Println(line)

		entry, err := g.fileListingToFileEntry(line)
		if err != nil {
			// Handle error: print that there's an issue
			fmt.Println("Error parsing line:", err)
			errors = append(errors, err)
			continue
		}
		// fmt.Printf("Entry name:%s\n", entry.filename)

		if entry.isDirectory {
			// TODO Make this better later
			sourceParts := strings.SplitN(source, "://", 2)
			newPath := path.Join(sourceParts[1], entry.filename)
			newSource := sourceParts[0] + "://" + newPath

			// Get files in this directory recursively
			files, err := g.getFilesTree(ctx, newSource, nil)
			if err != nil {
				// Skip this directory
				// Handle error: print that there's an issue
				errors = append(errors, err)
				continue
			}
			entry.containsFiles = files
		}
		dirContents = append(dirContents, entry)
	}
	if scanner.Err() != nil {
		// Handle error
		return nil, scanner.Err()
	}

	// If we had any errors, we should tell the caller
	if len(errors) > 0 {
		return dirContents, fmt.Errorf("errors occurred while processing: %v", errors)
	}

	return dirContents, nil
}

// // TODO Need to apply recursion somewhere here to get files inside directories
// func (g *gfal2Client) parseOutputToFileEntries(ctx context.Context, output []byte) ([]*FileEntry, error) {
// 	// TODO Maybe add verbose?
// 	fileEntries := make([]*FileEntry, 0)

// 	scanner := bufio.NewScanner(bytes.NewReader(output))
// 	scanner.Split(bufio.ScanLines) // Tokenize by line

// 	for scanner.Scan() {
// 		line := scanner.Text()
// 		fmt.Println(line)
// 		entry, err := g.fileListingToFileEntry(line)
// 		if err != nil {
// 			// Handle error: print that there's an issue
// 			continue
// 		}
// 		fileEntries = append(fileEntries, entry)
// 	}
// 	if scanner.Err() != nil {
// 		// Handle error
// 		return nil, scanner.Err()
// 	}
// 	return fileEntries, nil
// }

func (g *gfal2Client) fileListingToFileEntry(line string) (*FileEntry, error) {
	var err error
	lineParts := lineRegex.FindStringSubmatch(line)
	if lineParts == nil {
		return nil, ErrParseLine
	}

	f := &FileEntry{filename: strings.TrimSpace(lineParts[7])}
	perms := lineParts[1]
	dateString := lineParts[6]

	f.isDirectory, err = g.parsePermsToDirectoryFlag(perms)
	if err != nil {
		return nil, ErrParseLine
	}

	f.created, err = g.parseDateStampToTime(dateString)
	if err != nil {
		return nil, ErrParseLine
	}

	return f, nil
}

func (g *gfal2Client) parsePermsToDirectoryFlag(perms string) (bool, error) {
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

func (g *gfal2Client) parseDateStampToTime(dateString string) (time.Time, error) {
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

// TODO rename this
var (
	ErrParseLine      = errors.New("could not parse line")
	ErrMalformedPerms = errors.New("perms string is malformed")
)
