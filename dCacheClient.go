package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
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

func init() {
	// Register the metrics
	metricsRegistry.MustRegister(dCacheClientRemoveFileHistogram)
	slog.Debug("Registered dCache client metrics")
}

// dCacheClient is a client for interacting with dCache via HTTP API. It uses a token for authentication.
type dCacheClient struct {
	client *http.Client
	token  string
}

// newDCacheClient creates a new dCacheClient instance. It sets up the HTTP client with TLS configuration
func newDCacheClient(token string, skipTlsVerify bool) *dCacheClient {
	return &dCacheClient{
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: skipTlsVerify, // We're running onsite, with token auth, so it may be OK to skip TLS verification
				},
			},
		},
		token: strings.TrimSpace(token),
	}
}

func (d *dCacheClient) removeFile(ctx context.Context, urlPath string) error {
	start := time.Now()
	funcLogger := slog.With("caller", "dCacheClient.removeFile")
	// Create the request
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, urlPath, nil)
	if err != nil {
		msg := "error creating HTTP request to delete file"
		funcLogger.Error(msg, "urlPath", urlPath, "error", err)
		return fmt.Errorf("%s: %w", msg, err)
	}

	// Set the authorization header
	req.Header.Set("Authorization", "Bearer "+d.token)

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

	slog.Debug("File deleted successfully", "urlPath", urlPath)
	dCacheClientRemoveFileHistogram.Observe(time.Since(start).Seconds())
	return nil
}
