package testserver

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// StartServer starts a simple HTTP server for testing purposes. It runs on loacalhost:8080.
// The server has a readiness endpoint at /ready and the following test endpoints:
//
//	/testexperiment/resilient/jobsub_stage/ (GET, DELETE (should return 500))
//	/testexperiment/resilient/jobsub_stage/file1 (DELETE)
//	/testexperiment/resilient/jobsub_stage/dir1 (GET, DELETE)
//	/testexperiment/resilient/jobsub_stage/dir2 (empty) (GET, DELETE)
//	/api/testexperiment/resilient/jobsub_stage/ (GET, DELETE (should return 500))
//	/api/testexperiment/resilient/jobsub_stage/file1 (DELETE)
//	/api/testexperiment/resilient/jobsub_stage/dir1 (GET, DELETE)
//	/api/testexperiment/resilient/jobsub_stage/dir2 (empty) (GET, DELETE)
//	/api/testexperiment/resilient/jobsub_stage/invalidfiledir (GET)
//
// To shut down the server, the caller should cancel the input context ctx. StartServer
// returns a channel, shutdown, that is closed when the server is shut down,
// and an error if the server did not start up correctly.
func StartServer(ctx context.Context) (shutdown chan struct{}, startupErr error) {
	shutdown = make(chan struct{}) // Channel to signal to the caller that the server is shutdown
	server := &http.Server{
		Addr: ":8080",
	}
	// Server Mux
	mux := http.NewServeMux()
	server.Handler = mux

	// Handlers
	mux.HandleFunc("/ready", handleReady)

	// Handlers for the test endpoints in the case of direct WebDAV access
	mux.HandleFunc("/testexperiment/resilient/jobsub_stage/", handleJobsubStage)
	mux.HandleFunc("/testexperiment/resilient/jobsub_stage/file1", handleFile1)
	mux.HandleFunc("/testexperiment/resilient/jobsub_stage/dir1", handleDir1)
	mux.HandleFunc("/testexperiment/resilient/jobsub_stage/dir2", handleDir2)

	// REST API endpoints that just do the same as the handlers above
	mux.HandleFunc("/api/testexperiment/resilient/jobsub_stage/", handleJobsubStage)
	mux.HandleFunc("/api/testexperiment/resilient/jobsub_stage/file1", handleFile1)
	mux.HandleFunc("/api/testexperiment/resilient/jobsub_stage/dir1", handleDir1)
	mux.HandleFunc("/api/testexperiment/resilient/jobsub_stage/dir2", handleDir2)
	mux.HandleFunc("/api/testexperiment/resilient/jobsub_stage/invalidfiledir", handleDirInvalidFileDir)
	mux.HandleFunc("/api/testexperiment/resilient/jobsub_stage/emptyPage", handleEmptyPage)

	// Start the server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server failed", "error", err)
		}
		slog.Info("Server closed")
		close(shutdown)
	}()

	// Wait for the server to be ready
	ready := false
	for i := 0; i < 10; i++ {
		slog.Info("checking if server is ready", "try", i+1, "totalTries", 10)
		func() {
			defer time.Sleep(500 * time.Millisecond)
			r, err := http.Get("http://localhost:8080/ready")
			if err != nil {
				slog.Error("Failed to send GET request", "error", err)
				return
			}
			defer r.Body.Close()
			if r.StatusCode != http.StatusOK {
				slog.Error("Unexpected response status", "expected", http.StatusOK, "got", r.Status)
				return
			}
			ready = true
		}()
		if ready {
			slog.Info("Server is ready")
			break
		}
	}
	if !ready {
		// If the server is not ready after 10 retries, shut it down
		c, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(c); err != nil {
			slog.Error("Server shutdown failed", "error", err)
		}
		return nil, errors.New("server did not become ready after 10 retries")
	}

	// Shutdown the server when the context is cancelled
	go func() {
		<-ctx.Done()
		if err := server.Shutdown(context.Background()); err != nil {
			slog.Error("Server shutdown failed", "error", err)
		}
	}()

	return shutdown, nil
}

func handleReady(w http.ResponseWriter, r *http.Request) {
	slog.Info("Received GET request for /ready")
	fmt.Fprintln(w, "ready")
}

func handleEmptyPage(w http.ResponseWriter, r *http.Request) {
	slog.Info("Received GET request for /api/testexperiment/resilient/jobsub_stage/emptyPage")
	w.WriteHeader(http.StatusOK)
}

//go:embed data/jobsub_stage.json
var getJobsubStageResponse string

func handleJobsubStage(w http.ResponseWriter, r *http.Request) {
	// /testexperiment/resilient/jobsub_stage/
	if r.Method == http.MethodGet {
		slog.Info("Received GET request for /testexperiment/resilient/jobsub_stage/")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, getJobsubStageResponse)
		return
	}

	if r.Method == http.MethodDelete {
		slog.Error("Received DELETE request for /testexperiment/resilient/jobsub_stage/file1")
		slog.Error("Should not delete this directory")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func handleFile1(w http.ResponseWriter, r *http.Request) {
	// /testexperiment/resilient/jobsub_stage/file1
	if r.Method == http.MethodDelete {
		slog.Info("Received DELETE request for /testexperiment/resilient/jobsub_stage/file1")
		w.WriteHeader(http.StatusNoContent)
	}
}

//go:embed data/dir1.json
var getDir1Response string

func handleDir1(w http.ResponseWriter, r *http.Request) {
	// /testexperiment/resilient/jobsub_stage/dir1
	if r.Method == http.MethodGet {
		slog.Info("Received GET request for /testexperiment/resilient/jobsub_stage/dir1")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, getDir1Response)
		return
	}

	if r.Method == http.MethodDelete {
		slog.Info("Received DELETE request for /testexperiment/resilient/jobsub_stage/dir1")
		w.WriteHeader(http.StatusNoContent)
		return
	}
}

//go:embed data/dir2.json
var getDir2Response string

func handleDir2(w http.ResponseWriter, r *http.Request) {
	// /testexperiment/resilient/jobsub_stage/dir1
	if r.Method == http.MethodGet {
		slog.Info("Received GET request for /testexperiment/resilient/jobsub_stage/dir2")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, getDir2Response)
		return
	}

	if r.Method == http.MethodDelete {
		slog.Info("Received DELETE request for /testexperiment/resilient/jobsub_stage/dir1")
		w.WriteHeader(http.StatusNoContent)
		return
	}
}

//go:embed data/invalid_file.json
var getInvalidFileDirResponse string

func handleDirInvalidFileDir(w http.ResponseWriter, r *http.Request) {
	// /testexperiment/resilient/jobsub_stage/dir1
	if r.Method == http.MethodGet {
		slog.Info("Received GET request for /testexperiment/resilient/jobsub_stage/invalidfiledir")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, getInvalidFileDirResponse)
		return
	}

}
