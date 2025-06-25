package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// TODO NOTE: This will be deprecated when the dCache client is fully implemented

var lineRegex = regexp.MustCompile(`((?:\w|-)+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\w+\s+\d+\s+(?:(?:\d+:\d+)|\d+))\s+(.+)`)

var (
	defaultRetryCount    uint          = 5
	defaultRetrySleep    time.Duration = 5 * time.Second // Default sleep time between retries
	defaultFileCountLeft int32         = 1000            // Default file count limit
	// In our directory structure, we expect that each directory will most likely have at least
	// 2 files (the directory itself and at least one file inside it).
	defaultLenDirPlusFile int = 2
)

var (
	dateWithTimeNoYearLayout string = "Jan  2 15:04"
	dateWithYearLayout       string = "Jan 2 2006"
)

// TODO This should probably have an authenticator (token or proxy?)
type gfal2Client struct {
	addedEnvironment []string
	fileCountLimit   uint
	fileCountLeft    atomic.Int32
	retryCount       uint
	retrySleep       time.Duration
}

// newGfal2Client creates a new gfal2Client with the specified file count limit, retry count, retry sleep duration, and additional environment variables.
// Passsing the zero-values of the parameters to this constructor will yield a usable default *gfal2Client.
func newGfal2Client(fileCountLimit int, retryCount uint, retrySleep time.Duration, environment []string) *gfal2Client {
	c := &gfal2Client{
		addedEnvironment: environment,
		retryCount:       defaultRetryCount,
		retrySleep:       defaultRetrySleep,
	}

	if retryCount > 0 {
		c.retryCount = retryCount
	}

	if retrySleep > 0 {
		c.retrySleep = retrySleep
	}

	if fileCountLimit <= 0 {
		c.fileCountLimit = uint(defaultFileCountLeft)
		c.fileCountLeft.Store(defaultFileCountLeft)
		return c
	}
	c.fileCountLimit = uint(fileCountLimit)
	c.fileCountLeft.Store(int32(fileCountLimit))
	return c
}

// TODO: Can this be implemented using a fs.WalkDirFunc?
// Recursive
func (g *gfal2Client) getFilesList(ctx context.Context, source string, dirContents []*FileEntry, parent *FileEntry) ([]*FileEntry, error) {
	if err := ctx.Err(); err != nil {
		msg := "context deadline exceeded before getting files list"
		if errors.Is(err, context.Canceled) {
			msg = "context canceled before getting files list"
			slog.Error(msg, "error", err)
			return nil, fmt.Errorf("%s: %w", msg, err)
		}
		slog.Error(msg, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	// Setup environment
	environ := os.Environ()
	environ = append(environ, g.addedEnvironment...)

	// environ := append(os.Environ(), "BEARER_TOKEN=)

	// Run command
	cmdArgs := []string{"-l", source}

	// gfal-ls -l <source>
	var stdoutStderr []byte
	var err error
	for i := 0; i < int(g.retryCount); i++ {
		c := exec.CommandContext(ctx, "gfal-ls", cmdArgs...)
		c.Env = environ

		slog.Debug("Running command", "command", c.String(), "try", i+1, "maxRetries", g.retryCount)
		stdoutStderr, err = c.CombinedOutput()
		if err != nil {
			msg := "error running gfal-ls command"
			slog.Error(msg, "error", err)
			if i < int(g.retryCount)-1 {
				slog.Debug("Will sleep 5s and then retry command", "try", i+1, "maxRetries", g.retryCount)
				time.Sleep(g.retrySleep) // Sleep before retrying
				continue
			}
			slog.Error("Max retries exceeded for command", "command", c.String(), "error", err)
			return nil, fmt.Errorf("%s: %w", msg, err)
		}
		break
	}

	scanner := bufio.NewScanner(bytes.NewReader(stdoutStderr))
	scanner.Split(bufio.ScanLines) // Tokenize by line

	errs := make([]error, 0)

	sourceURL, err := url.Parse(source)
	if err != nil {
		slog.Error("error parsing source URL", "source", source, "error", err)
		return nil, fmt.Errorf("error parsing source URL: %w", err)
	}

	for scanner.Scan() {
		slog.Debug("File count left", "remaining", g.fileCountLeft.Load())
		if g.fileCountLeft.Load() == 0 {
			return dirContents, errFileCountLimitExceeded
		}
		g.fileCountLeft.Add(-1)
		line := scanner.Text()

		entry, err := g.fileListingToFileEntry(line, func(s string) string {
			return path.Join("/pnfs", sourceURL.Path, s)
		})
		if errors.Is(err, errFileCountLimitExceeded) {
			// We exceeded our file count limit, so we should stop
			slog.Debug("File count limit exceeded, stopping")
			return dirContents, err
		}
		if err != nil {
			// Handle error: print that there's an issue
			slog.Error("error parsing line", "error", err)
			errs = append(errs, err)
			continue
		}
		entry.parent = parent

		if entry.isDirectory {
			urlFile := strings.TrimPrefix(entry.filename, "/pnfs")
			newSource := sourceURL.Scheme + "://" + sourceURL.Host + urlFile

			// Get files in this directory recursively
			files, err := g.getFilesList(ctx, newSource, nil, entry)
			if err != nil {
				// Skip the directory
				slog.Error("error getting files in directory. Moving to next entry", "directory", entry.filename, "error", err)

				// If we hit the file count limit mid-directory, we want to also reset the g.fileCountLeft counter to
				// however many spots were open before we started parsing this directory, so g.fileCountLimit - len(dirContents)
				//
				// We also need to make sure we don't end up hitting this case over and over again by resetting the counter to 1
				// if we're left with a series of directories that all have one file in them, and we previously only had one spot
				// left. So we will also check to make sure we have at least defaultLenDirPlusFile spots left in the counter before
				// resetting it.  If we have fewer spots than that left, we'll just leave the counter as is, and be OK with deleting fewer
				// than g.fileCountLimit files for the current run
				if errors.Is(err, errFileCountLimitExceeded) {
					newCounterVal := int32(int(g.fileCountLimit) - len(dirContents))
					if int(newCounterVal) > defaultLenDirPlusFile {
						g.fileCountLeft.Store(newCounterVal)
						slog.Debug(
							"File count limit exceeded mid-directory. Resetting file limit counter to pre-current-directory count",
							"directory", entry.filename, "newCount", g.fileCountLeft.Load())
					}
				}
				// Skip this directory
				errs = append(errs, err)
				continue
			}
			entry.containsFiles = files
			dirContents = append(dirContents, files...) // Add the entries in this directory to the dirContents list before adding the directory itself
		}
		dirContents = append(dirContents, entry)
	}
	if scanner.Err() != nil {
		// Handle error
		msg := "error scanning output"
		slog.Error(msg, "error", scanner.Err())
		return nil, fmt.Errorf("%s: %w", msg, scanner.Err())
	}

	// If we had any errors, we should tell the caller
	if len(errs) > 0 {
		return dirContents, fmt.Errorf("errors occurred while processing: %v", errs)
	}

	return dirContents, nil
}

func (g *gfal2Client) fileListingToFileEntry(line string, filenameTransformFunc func(string) string) (*FileEntry, error) {
	var err error
	lineParts := lineRegex.FindStringSubmatch(line)
	if lineParts == nil {
		return nil, errParseLine
	}

	f := &FileEntry{filename: filenameTransformFunc(strings.TrimSpace(lineParts[7]))}
	perms := lineParts[1]
	dateString := lineParts[6]

	f.isDirectory, err = g.parsePermsToDirectoryFlag(perms)
	if err != nil {
		return nil, errParseLine
	}

	f.created, err = g.parseDateStampToTime(dateString)
	if err != nil {
		return nil, errParseLine
	}

	return f, nil
}

func (g *gfal2Client) parsePermsToDirectoryFlag(perms string) (bool, error) {
	if len(perms) != 10 {
		return false, errMalformedPerms
	}

	validPrefixes := []string{"d", "-"}
	if !slices.Contains(validPrefixes, string(perms[0])) {
		return false, errMalformedPerms
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

// This is unused for now, but we keep it here in case we want to use it later
func (g *gfal2Client) removeFile(ctx context.Context, source string, isDir bool) error {
	// Setup environment
	environ := os.Environ()
	environ = append(environ, g.addedEnvironment...)

	// Args
	cmdArgs := make([]string, 0)
	if isDir {
		// gfal-rm -r <dir>
		cmdArgs = append(cmdArgs, "-r")
	}
	cmdArgs = append(cmdArgs, source)

	// gfal-rm <file>
	c := exec.CommandContext(ctx, "echo", cmdArgs...)
	c.Env = environ

	slog.Debug("Running delete command", "command", c.String())
	err := c.Run()
	if err != nil {
		slog.Error("error running command", "command", c.String(), "error", err)
		return err
	}
	slog.Debug("Removed file", "filename", source)

	return nil
}

var (
	errParseLine              = errors.New("could not parse line")
	errMalformedPerms         = errors.New("perms string is malformed")
	errFileCountLimitExceeded = errors.New("file parse limit exceeded")
)
