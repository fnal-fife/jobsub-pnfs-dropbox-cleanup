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

// TODO NOTE: This will be deprecated when the dCache client is fully implemented.  Because of this, I won't bother now with mocking out gfal-ls command failure

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

func init() {
	// Check for all required executables
	requiredExecutables := []string{
		"gfal-ls",
		"gfal-rm",
	}
	for _, exe := range requiredExecutables {
		if _, ok := exeMap[exe]; ok {
			continue // Already found this executable
		}

		p, err := exec.LookPath(exe)
		if err != nil {
			panic(fmt.Sprintf("Required executable %s not found in PATH", exe))
		}
		exeMap[exe] = p
	}
}

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

	if retryCount >= 0 {
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

// getFilesList retrieves a list of files and directories from the specified source path using the `gfal-ls` command.
// It supports recursive traversal of directories, respects a file count limit, and handles context cancellation and retries.
// It returns a slice of FileEntry objects representing the files and directories found, or an error if any occurred.
// A nil error or errProcessingFiles indicates that at least some entries were successfully processed, and the returned
// slice of FileEntry objects can be used if the caller wishes
//
// TODO: Can this be implemented using a fs.WalkDirFunc?
func (g *gfal2Client) getFilesList(ctx context.Context, source string, dirContents []*FileEntry, parent *FileEntry) ([]*FileEntry, error) {
	funcLogger := logger.With("caller", "gfal2Client.getFilesList")
	if err := ctx.Err(); err != nil {
		msg := "context deadline exceeded before getting files list"
		if errors.Is(err, context.Canceled) {
			msg = "context canceled before getting files list"
			funcLogger.Error(msg, "error", err)
			return nil, fmt.Errorf("%s: %w", msg, err)
		}
		funcLogger.Error(msg, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	// Setup environment
	environ := os.Environ()
	environ = append(environ, g.addedEnvironment...)

	// Run command
	cmdArgs := []string{"-l", source}

	// gfal-ls -l <source>
	var stdoutStderr []byte
	var err error
	for i := 0; i <= int(g.retryCount); i++ {
		c := exec.CommandContext(ctx, exeMap["gfal-ls"], cmdArgs...)
		c.Env = environ

		funcLogger.Debug("Running command", "command", c.String(), "try", i+1, "maxRetries", g.retryCount)
		stdoutStderr, err = c.CombinedOutput()
		if err != nil {
			msg := "error running gfal-ls command"
			slog.Error(msg, "error", err)
			if i < int(g.retryCount)-1 {
				slog.Debug("Will sleep 5s and then retry command", "try", i+1, "maxRetries", g.retryCount)
				time.Sleep(g.retrySleep) // Sleep before retrying
				continue
			}
			funcLogger.Error("Max retries exceeded for command", "command", c.String(), "error", err)
			return nil, fmt.Errorf("error getting files list: %w", err)
		}
		break
	}

	scanner := bufio.NewScanner(bytes.NewReader(stdoutStderr))
	scanner.Split(bufio.ScanLines) // Tokenize by line

	errs := make([]error, 0)

	sourceURL, err := url.Parse(source)
	if err != nil {
		funcLogger.Error("error parsing source URL", "source", source, "error", err)
		return nil, fmt.Errorf("error parsing source URL: %w", err)
	}

	for scanner.Scan() {
		funcLogger.Debug("File count left", "remaining", g.fileCountLeft.Load())
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
			funcLogger.Debug("File count limit exceeded, stopping")
			return dirContents, err
		}
		if err != nil {
			// Handle error: print that there's an issue
			funcLogger.Error("error parsing line", "error", err)
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
				funcLogger.Error("error getting files in directory. Moving to next entry", "directory", entry.filename, "error", err)

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
						funcLogger.Debug(
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
		return nil, fmt.Errorf("error scanning files list query output: %w", scanner.Err())
	}

	// If we had any errors, we should tell the caller
	if len(errs) > 0 {
		return dirContents, &errProcessingFiles{errors: errs}
	}

	return dirContents, nil
}

// fileListingToFileEntry parses a line from the `gfal-ls` output and returns a FileEntry object.
// The line should be in the format:
// -rwxrwxrwx   0 0     0            50 Sep 26 14:55 bogus_file.out
// or for directories:
// drwxrwxrwx   0 0     0             0 Apr  6  2023 bogus_dir
// It returns an error if the line cannot be parsed correctly.
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

// parsePermsToDirectoryFlag checks the permissions string and returns true if it indicates a directory
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

// parseDateStampToTime parses a date string as returned by gfal-ls (in the format "Jan  2 15:04" or "Jan 2 2006")
// and returns a time.Time object.
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
	c := exec.CommandContext(ctx, "gfal-rm", cmdArgs...)
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

// errProcessingFiles is an error type that holds a slice of errors encountered while processing files.
type errProcessingFiles struct {
	errors []error
}

func (e *errProcessingFiles) Error() string {
	var b strings.Builder
	for _, err := range e.errors {
		b.WriteString(err.Error())
		b.WriteString(", ")
	}
	return strings.TrimRight(b.String(), ", ")
}
