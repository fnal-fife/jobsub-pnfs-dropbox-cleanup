package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics
var (
	dCacheClientRemoveFileHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "dcache_client_remove_file_duration_seconds",
		Help:      "The duration of dCache client delete operations",
	})
)

var (
	defaultRetrySleep    time.Duration = 5 * time.Second // Default sleep time between retries
	defaultFileCountLeft int32         = 1000            // Default file count limit
)

// Steps:
// 0. setTokenAuth func - DONE
// 0. Check with delete method - DONE
// 1. Get client working - DONE
// 2. With recursion - DONE
//4. with retries - done here, need to update run() - DONE
// 5. Refactor code if needed, like moving PNFSToTHTTPS here, and docstrings

func init() {
	// Register the metrics
	metricsRegistry.MustRegister(dCacheClientRemoveFileHistogram)
	slog.Debug("Registered dCache client metrics")
}

// dCacheClient is a client for interacting with dCache via HTTP API. It uses a token for authentication.
type dCacheClient struct {
	client         *http.Client
	token          string
	authFunc       func(*http.Request) error
	apiEndpoint    string // The API endpoint for dCache, e.g., "/api/v1/namespace/"
	fileCountLimit uint
	fileCountLeft  atomic.Int32
	retryCount     uint
	retrySleep     time.Duration
}

// TODO Test all the cases of fileCountLimit, retryCount, retrySleep
// newDCacheClient creates a new dCacheClient instance. It sets up the HTTP client with TLS configuration
func newDCacheClient(token, apiEndpoint string, fileCountLimit int, retryCount uint, retrySleep time.Duration, skipTlsVerify bool) *dCacheClient {
	d := &dCacheClient{
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: skipTlsVerify, // We're running onsite, with token auth, so it may be OK to skip TLS verification
				},
			},
		},
		token:      strings.TrimSpace(token),
		retryCount: retryCount,
		retrySleep: defaultRetrySleep,
	}

	if !strings.HasPrefix(apiEndpoint, "/") {
		apiEndpoint = "/" + apiEndpoint // Ensure the API endpoint starts with a slash
	}
	if !strings.HasSuffix(apiEndpoint, "/") {
		apiEndpoint += "/" // Ensure the API endpoint ends with a slash
	}
	d.apiEndpoint = apiEndpoint

	if err := d.setTokenAuth(); err != nil {
		slog.Error("Failed to set token auth for dCache client", "error", err)
		return nil
	}

	if retrySleep > 0 {
		d.retrySleep = retrySleep
	}

	if fileCountLimit <= 0 {
		d.fileCountLimit = uint(defaultFileCountLeft)
		d.fileCountLeft.Store(defaultFileCountLeft)
		return d
	}
	d.fileCountLimit = uint(fileCountLimit)
	d.fileCountLeft.Store(int32(fileCountLimit))

	return d
}

// setTokenAuth sets the Authorization header for the HTTP request using the token provided to the dCacheClient.
func (d *dCacheClient) setTokenAuth() error {
	if d.token == "" {
		return errNoTokenProvided
	}
	d.authFunc = func(req *http.Request) error {
		if req == nil {
			return errNilRequest
		}
		req.Header.Set("Authorization", "Bearer "+d.token)
		return nil
	}
	return nil
}

func (d *dCacheClient) getFilesList(ctx context.Context, source string, dirContents []*FileEntry, parent *FileEntry, excludeFunc func(*FileEntry) bool) ([]*FileEntry, error) {
	funcLogger := logger.With("caller", "dCacheClient.getFilesList")
	// Check our context first
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

	// Create the request
	u := strings.TrimSuffix(source, "/") // Ensure the source URL does not end with a slash before we add query args
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		msg := "error creating HTTP request to get files list"
		funcLogger.Error(msg, "source", source, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	// Add query parameters:
	// 1. Get children of directory
	// 2. Limit number of returned values to the current fileCountLimit
	urlValues := req.URL.Query()
	urlValues.Add("children", "true") // This is required to get the children of the directory
	urlValues.Add("limit", strconv.Itoa(int(d.fileCountLeft.Load())))
	req.URL.RawQuery = urlValues.Encode()

	// Set the Authorization and Accept headers
	// TODO Setting these headers should be a method on dCacheClient
	if err = d.authFunc(req); err != nil {
		return nil, fmt.Errorf("error setting authorization header for files list request: %w", err)
	}
	req.Header.Set("Accept", "application/json") // Set the Accept header to application/json

	// Perform the request in a retry loop
	var resp *http.Response
	for i := range int(d.retryCount + 1) {
		funcLogger.Debug("Sending request to get files list", "url", req.URL.String(), "attempt", i+1)
		resp, err = d.client.Do(req)
		if err == nil {
			break // Successful request: break out of the retry loop
		}

		// Failure - handle the error and retry if possible
		msg := "error sending HTTP request to get files"
		errFields := []any{any("urlPath"), any(req.URL.String())}
		if resp != nil {
			errFields = append(errFields, any("status"), any(resp.Status))
		}
		errFields = append(errFields, any("error"), any(err))
		funcLogger.Error(msg, errFields...)
		if i < int(d.retryCount) {
			funcLogger.Debug("Will sleep 5s and then retry command", "try", i+1, "maxRetries", d.retryCount)
			time.Sleep(d.retrySleep) // Sleep before retrying
			continue
		}
		funcLogger.Error("Max retries exceeded for request", "url", req.URL.String(), "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	defer resp.Body.Close()

	// Check the response status code
	if resp.StatusCode != http.StatusOK {
		// Read the response to get the error message
		b := make([]byte, 0, resp.ContentLength)
		_, err := resp.Body.Read(b)
		msg := string(b)
		if err != nil {
			msg = fmt.Sprintf("failed to read response body: %s", err)
		}
		funcLogger.Error("request failed", "source", source, "status", resp.Status, "message", msg)
		return nil, fmt.Errorf("request failed: %s: %s", resp.Status, msg)
	}

	// Decode the response body into a dCacheDirListing struct
	var listing dCacheDirListing
	if err = json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		msg := "error reading dCache directory listing response"
		funcLogger.Error(msg, "source", source, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	sourceURL, err := url.Parse(source)
	if err != nil {
		// This should never really fail, but just in case, we'll log the error if it happens
		funcLogger.Error("error parsing source URL", "source", source, "error", err)
		return nil, fmt.Errorf("error parsing source URL: %w", err)
	}

	errs := make([]error, 0) // Errors while parsing files from directory

	for _, child := range listing.Children {
		// Check our file count limit
		funcLogger.Debug("File count left", "remaining", d.fileCountLeft.Load())

		// Create a file entry for the child
		entry, err := d.fileListingToFileEntry(child,
			func(s string) string {
				return path.Join("/pnfs", d.trimAPIEndpoint(sourceURL.Path), s)
			})
		if err != nil {
			// Handle error: print that there's an issue
			funcLogger.Error("error parsing file listing entry", "error", err)
			errs = append(errs, err)
			continue
		}
		entry.parent = parent

		// If the child is a directory, recursively call getFilesList
		if entry.isDirectory {
			// Query the dCache server for the directory contents
			// TODO This should be a call to PNFSTOHTTPS, right?
			fPath := strings.TrimPrefix(entry.filename, "/pnfs")
			apiPath := d.pathToAPIURLPath(fPath)
			newSource := sourceURL.Scheme + "://" + sourceURL.Host + apiPath
			files, err := d.getFilesList(ctx, newSource, nil, entry, excludeFunc)
			if err != nil {
				// If we hit the file count limit mid-directory, add the files that we got back from the getFilesList call, but do NOT add the directory, since the directory may not have been
				// fully parsed.
				// Then return what we have
				if errors.Is(err, errFileCountLimitExceeded) {
					funcLogger.Warn("File count limit exceeded mid-directory.", "directory", entry.filename)
					dirContents = append(dirContents, files...)   // Add the files we got back from the getFilesList call
					return dirContents, errFileCountLimitExceeded // Return what we have
				}

				// If we excluded some files, we should still add any entries we got back from the recursive call
				var errProcFiles *errProcessingFiles
				excludedFiles := false
				if errors.As(err, &errProcFiles) {
					for _, e := range err.(*errProcessingFiles).errors {
						if f, ok := e.(*errFileFlaggedToExclude); ok {
							funcLogger.Warn("File flagged to be excluded by excludeFunc", "file", f.filename)
							excludedFiles = true
						}
					}
				}

				// Otherwise, we just skip the whole directory and continue
				if !excludedFiles {
					funcLogger.Error("error getting files in directory. Moving to next entry", "directory", entry.filename, "error", err)
					errs = append(errs, err)
					continue
				}

			}
			// We got all the files in the directory back without hitting the file count limit or encountering an error, so finish populating the dir entry
			entry.containsFiles = files
			dirContents = append(dirContents, files...) // Add the entries in this directory to the dirContents list before adding the directory itself
		}

		// If the entry is excluded by the excludeFunc, skip it
		if excludeFunc != nil && excludeFunc(entry) {
			funcLogger.Debug("Excluding file entry", "file", entry.filename)
			errs = append(errs, &errFileFlaggedToExclude{filename: entry.filename})
			continue
		}

		dirContents = append(dirContents, entry) // Add the file or current-level dir to dirContents

		// Now we can decrement our file count limit counter, and then check if we hit the limit
		d.fileCountLeft.Add(-1)
		if d.fileCountLeft.Load() == 0 {
			funcLogger.Warn("File count limit exceeded, stopping")
			return dirContents, errFileCountLimitExceeded
		}
	}

	// If we had any errors, we should tell the caller
	if len(errs) > 0 {
		return dirContents, &errProcessingFiles{errors: errs}
	}

	return dirContents, nil
}

// removeFile deletes a file from dCache using the HTTP DELETE method.
func (d *dCacheClient) removeFile(ctx context.Context, urlPath string) error {
	start := time.Now()
	funcLogger := logger.With("caller", "dCacheClient.removeFile")
	// Create the request
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, urlPath, nil)
	if err != nil {
		msg := "error creating HTTP request to delete file"
		funcLogger.Error(msg, "urlPath", urlPath, "error", err)
		return fmt.Errorf("%s: %w", msg, err)
	}

	// Set the authorization header
	// This part is not tested because it is covered in the TestDCacheClientSetTokenAuth tests
	if err = d.authFunc(req); err != nil {
		return fmt.Errorf("error setting authorization header: %w", err)
	}

	// Perform the request
	resp, err := d.client.Do(req)
	if err != nil {
		msg := "error sending HTTP request to delete file"
		errFields := []any{any("urlPath"), any(urlPath)}
		if resp != nil {
			errFields = append(errFields, any("status"), any(resp.Status))
		}
		errFields = append(errFields, any("error"), any(err))
		funcLogger.Error(msg, errFields...)
		return fmt.Errorf("%s: %w", msg, err)
	}
	defer resp.Body.Close()

	// Check the response status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b := make([]byte, resp.ContentLength)
		_, err := resp.Body.Read(b)
		msg := string(b)
		if err != nil {
			msg = fmt.Sprintf("failed to read response body: %s", err)
		}
		funcLogger.Error("failed to delete file", "urlPath", urlPath, "status", resp.Status, "message", msg)
		return fmt.Errorf("failed to delete file: %s: %s", resp.Status, msg)
	}

	funcLogger.Debug("File deleted successfully", "urlPath", urlPath)
	dCacheClientRemoveFileHistogram.Observe(time.Since(start).Seconds())
	return nil
}

var (
	errNoTokenProvided = fmt.Errorf("no token provided to dCache client")
	errNilRequest      = fmt.Errorf("nil request provided to dCache client")
)

type dCacheFileListing struct {
	FileName string `json:"fileName"`
	FileType string `json:"fileType"`
	Mtime    int64  `json:"mtime"`
}
type dCacheDirListing struct {
	Children []dCacheFileListing `json:"children"`
	FileType string              `json:"fileType"`
	Mtime    int64               `json:"mtime"`
}

func (d *dCacheClient) fileListingToFileEntry(listing dCacheFileListing, filenameTransformFunc func(string) string) (*FileEntry, error) {
	f := &FileEntry{filename: filenameTransformFunc(listing.FileName)}

	sec, nsec := mSecToUnixTuple(listing.Mtime)
	f.modified = time.Unix(sec, nsec).UTC()

	switch listing.FileType {
	case "DIR":
		f.isDirectory = true
	case "REGULAR":
		f.isDirectory = false
	default:
		return nil, fmt.Errorf("unknown file type: %s", listing.FileType)
	}

	return f, nil
}

// TODO - should we move PNFSToHTTPS here?

func (d *dCacheClient) trimAPIEndpoint(urlPath string) string {
	// Remove the API endpoint prefix from the endpoint
	return strings.TrimPrefix(urlPath, d.apiEndpoint)
}

func (d *dCacheClient) pathToAPIURLPath(path string) string {
	// Add the API endpoint prefix to the endpoint
	return filepath.Join(d.apiEndpoint, path)
}

// mSecToUnixTuple converts milliseconds to a tuple of seconds and nanoseconds for use in time.Unix()
func mSecToUnixTuple(mSec int64) (int64, int64) {
	msecRemainder := mSec % 1000
	seconds := (mSec - msecRemainder) / 1000
	nanoseconds := msecRemainder * 1_000_000 // Convert milliseconds to nanoseconds
	return seconds, nanoseconds
}

type errFileFlaggedToExclude struct {
	filename string
}

func (e *errFileFlaggedToExclude) Error() string {
	return fmt.Sprintf("file %s flagged to be excluded by excludeFunc", e.filename)
}
