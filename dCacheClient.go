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
	"strings"
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

// Steps:
// 0. setTokenAuth func - DONE
// 0. Check with delete method - DONE
// 1. Get client working
// 2. With recursion
//4. with retries

func init() {
	// Register the metrics
	metricsRegistry.MustRegister(dCacheClientRemoveFileHistogram)
	slog.Debug("Registered dCache client metrics")
}

// dCacheClient is a client for interacting with dCache via HTTP API. It uses a token for authentication.
type dCacheClient struct {
	client      *http.Client
	token       string
	authFunc    func(*http.Request) error
	apiEndpoint string // The API endpoint for dCache, e.g., "/api/v1/namespace/"
}

// newDCacheClient creates a new dCacheClient instance. It sets up the HTTP client with TLS configuration
func newDCacheClient(token, apiEndpoint string, skipTlsVerify bool) *dCacheClient {
	d := &dCacheClient{
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: skipTlsVerify, // We're running onsite, with token auth, so it may be OK to skip TLS verification
				},
			},
		},
		token:       strings.TrimSpace(token),
		apiEndpoint: apiEndpoint,
	}
	if err := d.setTokenAuth(); err != nil {
		slog.Error("Failed to set token auth for dCache client", "error", err)
		return nil
	}
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

func (d *dCacheClient) getFilesList(ctx context.Context, source string, dirContents []*FileEntry, parent *FileEntry) ([]*FileEntry, error) {
	funcLogger := logger.With("caller", "dCacheClient.getFilesList")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		msg := "error creating HTTP request to get files list"
		funcLogger.Error(msg, "source", source, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	// Set the auth header
	if err = d.authFunc(req); err != nil {
		return nil, fmt.Errorf("error setting authorization header for files list request: %w", err)
	}
	req.Header.Set("Accept", "application/json") // Set the Accept header to application/json

	// Perform the request
	resp, err := d.client.Do(req)
	if err != nil {
		msg := "error sending HTTP request to get files"
		errFields := []any{any("urlPath"), any(source)}
		if resp != nil {
			errFields = append(errFields, any("status"), any(resp.Status))
		}
		errFields = append(errFields, any("error"), any(err))
		funcLogger.Error(msg, errFields...)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}
	defer resp.Body.Close()

	// Check the response status code
	if resp.StatusCode != http.StatusOK {
		b := make([]byte, 0, resp.ContentLength)
		_, err := resp.Body.Read(b)
		msg := string(b)
		if err != nil {
			msg = fmt.Sprintf("failed to read response body: %s", err)
		}
		funcLogger.Error("request failed", "source", source, "status", resp.Status, "message", msg)
		return nil, fmt.Errorf("request failed: %s: %s", resp.Status, msg)
	}

	// TODO check this error case
	var listing dCacheDirListing
	if err = json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		msg := "error reading dCache directory listing response"
		funcLogger.Error(msg, "source", source, "error", err)
		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	// TODO Check this error case
	sourceURL, err := url.Parse(source)
	if err != nil {
		funcLogger.Error("error parsing source URL", "source", source, "error", err)
		return nil, fmt.Errorf("error parsing source URL: %w", err)
	}

	errs := make([]error, 0) // Errors while parsing files from directory

	// Create FileEntry objects from the listing
	// iterate through children and create FileEntry objects
	for _, child := range listing.Children {
		// Create a file entry for the child
		// f, err  := &FileEntry{
		entry, err := d.fileListingToFileEntry(child, func(s string) string {
			return path.Join("/pnfs", d.trimAPIEndpoint(sourceURL.Path), s)
		})
		if errors.Is(err, errFileCountLimitExceeded) {
			// We exceeded our file count limit, so we should stop
			funcLogger.Debug("File count limit exceeded, stopping")
			return dirContents, err
		}
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
			fPath := strings.TrimPrefix(entry.filename, "/pnfs")
			apiPath := d.pathToAPIURLPath(fPath)
			newSource := sourceURL.Scheme + "://" + sourceURL.Host + apiPath
			files, err := d.getFilesList(ctx, newSource, nil, entry)
			if err != nil {
				// Skip the directory
				funcLogger.Error("error getting files in directory. Moving to next entry", "directory", entry.filename, "error", err)
				// TODO Bit about fileCountLImit
				errs = append(errs, err)
				continue
			}
			entry.containsFiles = files
			dirContents = append(dirContents, files...) // Add the entries in this directory to the dirContents list before adding the directory itself
		}

		dirContents = append(dirContents, entry) // Add the file or current-level dir to dirContents
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
	// FileMimeType string   `json:"-"`
	// Labels       []string `json:"-"`
	// Size         int64    `json:"-"`
	// CreationTime int64    `json:"-"`
	FileType string `json:"fileType"`
	// PnfsId       string   `json:"-"`
	// Nlink        int64    `json:"-"`
	Mtime int64 `json:"mtime"`
	// Mode         int64    `json:"-"`
}
type dCacheDirListing struct {
	// FileMimeType string              `json:"-"`
	Children []dCacheFileListing `json:"children"`
	// Labels       []string            `json:"-"`
	// Size         int64               `json:"-"`
	// CreationTime int64               `json:"-"`
	FileType string `json:"fileType"`
	// PnfsId       string              `json:"-"`
	// Nlink        int64               `json:"-"`
	Mtime int64 `json:"mtime"`
	// Mode         int64               `json:"-"`
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
