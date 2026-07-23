package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/grafana/loki-client-go/loki"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	slogloki "github.com/samber/slog-loki/v3"
	slogmulti "github.com/samber/slog-multi"
	flag "github.com/spf13/pflag"
)

var (
	now            = time.Now()
	exeMap         = map[string]string{} // Map of all the executables we will need to find in PATH
	version        string                // Should be injected at build time with something like go build -ldflags="-X main.version=$VERSION"
	buildTimestamp string                // Should be injected at build time with something like go build -ldflags="-X main.buildTimeStamp=$BUILDTIMESTAMP"

	k               = koanf.New(".")           // Config. Path delimiter is "."
	logger          *slog.Logger               // Global logger instance
	pusher          *push.Pusher               // Prometheus push gateway client
	metricsRegistry = prometheus.NewRegistry() // Prometheus metrics registry to gather metrics

)

// Defaults
var (
	// Config file defaults
	configDirName         = "jobsub-pnfs-dropbox-cleanup" // Directory name for config files
	defaultConfigFileName = "jobsub-pnfs-dropbox-cleanup.yml"
	defaultConfigFilePath = filepath.Join("/etc", configDirName, defaultConfigFileName)
	// Directories to check for config files
	configDirsToCheck = []string{
		".",
		filepath.Join(userConfigDir(), configDirName),
		filepath.Join("/etc", configDirName),
	}

	// Other defaults
	defaultTimeout             = time.Duration(30 * time.Minute)   // Default timeout for the program
	defaultVaultTokenTimeLeft  = time.Duration(3 * 24 * time.Hour) // 3 days
	defaultVaultTokenAgeCutoff = time.Duration(7 * 24 * time.Hour) // 7 days
	defaultCondorAuthMethod    = "IDTOKENS"                        // Default condor authentication method
)

// Metrics
var (
	promDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "stage_duration_seconds",
		Help:      "The amount of time it took to run a stage of the cleanup",
	},
		[]string{"stage"},
	)
	getDropboxFilesListByExptDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "get_files_list_by_expt_duration_seconds",
		Help:      "The duration of gfal2Client.getFilesList operations, by experiment",
	},
		[]string{"experiment"},
	)
	numFilesDeleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "files_deleted_total",
		Help:      "The number of files deleted by the cleanup",
	},
		[]string{"experiment"},
	)
	numErrorsDeletingFiles = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "jobsub_pnfs_dropbox_cleanup",
		Name:      "errors_deleting_files_total",
		Help:      "The number of errors encountered while deleting files",
	},
		[]string{"experiment"},
	)
)

func init() {
	// Register metrics
	metricsRegistry.MustRegister(promDuration)
	metricsRegistry.MustRegister(getDropboxFilesListByExptDuration)
	metricsRegistry.MustRegister(numFilesDeleted)
	metricsRegistry.MustRegister(numErrorsDeletingFiles)
}

func setFlags() *flag.FlagSet {
	f := flag.NewFlagSet("jobsub-pnfs-dropbox-cleanup", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println("Usage: jobsub-pnfs-dropbox-cleanup [options]")
		fmt.Println("Options:")
		f.PrintDefaults()
	}
	f.StringP("experiment", "e", "", "Experiment name to use for dropbox cleanup")
	f.StringP("config", "c", defaultConfigFilePath, fmt.Sprintf("Config file to load (default: %s)", defaultConfigFilePath))
	f.BoolP("debug", "d", false, "Enable debug logging")
	f.BoolP("help", "h", false, "Print help and exit")
	f.BoolP("test", "t", false, "Run in test mode (no actual deletions)")
	f.Bool("version", false, "Print version information and exit")

	return f
}

func setLoggingWithLoki(logLevel slog.Leveler) (initLogger *slog.Logger, cleanup func()) {
	// Set up fanout logger that logs to stdout and loki
	lokiConfig, _ := loki.NewDefaultConfig(k.String("loki.url"))
	lokiClient, _ := loki.New(lokiConfig)
	// We don't need to wrap this in a sync.Once to handle the error condition when run() is called below, because Stop()
	// already has its own sync.Once.  Thus, we can safely call or defer the call to Stop() as many times as we want
	cleanup = func() {
		lokiClient.Stop() // Stop the Loki client to send logs
	}

	initLogger = slog.New(slogmulti.Fanout(
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}),
		slogloki.Option{Level: logLevel, Client: lokiClient}.NewLokiHandler()),
	)
	initLogger = initLogger.
		With("environment", k.String("loki.environment")).
		With("service", k.String("service"))

	return initLogger, cleanup
}

func main() {
	// Read flags
	f := setFlags()
	f.Parse(os.Args[1:])

	// Check for help flag
	if helpCalled, _ := f.GetBool("help"); helpCalled {
		f.Usage()
		os.Exit(0)
	}

	// Check for version flag
	if versionCalled, _ := f.GetBool("version"); versionCalled {
		fmt.Printf("jobsub-pnfs-dropbox-cleanup version %s, build %s\n", version, buildTimestamp)
		os.Exit(0)
	}

	// Load Config
	configFileFlagVal, _ := f.GetString("config") // If we fail to get this value, we'll just use the default value
	configFilePath, err := getConfigFilePath(configFileFlagVal, configDirsToCheck)
	if err != nil {
		panic(fmt.Sprintf("error getting config file path: %v", err))
	}

	// Load configfile values into koanf instance
	if err := k.Load(file.Provider(configFilePath), yaml.Parser()); err != nil {
		panic(fmt.Sprintf("error loading config file: %v", err))
	}

	// Flags override config file values in koanf instance
	if err := k.Load(posflag.Provider(f, ".", k), nil); err != nil {
		panic(fmt.Sprintf("error loading config: %v", err))
	}

	// Everything here is running in a func so that the deferred actions take place before os.Exit
	// is called
	var exitCode int
	func() {
		// Set up logging
		var logCleanup func()
		logLevel := slog.LevelInfo
		if k.Bool("debug") {
			logLevel = slog.LevelDebug
		}
		logger, logCleanup = setLoggingWithLoki(logLevel) // The returned *slog.Logger gets assigned to our global logger

		funcLogger := logger.With("caller", "main.main")
		funcLogger.Info("Initialized logging")
		funcLogger.Debug("Debug logging enabled")
		defer logCleanup()

		// Set up metrics pusher
		pusher = push.New(k.String("prometheus.pushURL"), k.String("service")).Gatherer(metricsRegistry)
		defer func() {
			// Push metrics to the Prometheus push gateway
			if err := pusher.Push(); err != nil {
				funcLogger.Error("error pushing metrics to Prometheus push gateway", "error", err)
			}
			funcLogger.Debug("Pushed metrics to Prometheus push gateway", "pushURL", k.String("prometheus.pushURL"))
		}()

		// Set up our context with timeout
		timeout, err := time.ParseDuration(k.String("timeout"))
		if err != nil {
			funcLogger.Error("error getting timeout from config. Using default timeout", "error", err, "defaultTimeout", defaultTimeout)
			timeout = defaultTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		var e1 *errDeletingFiles
		err = run(ctx, k)
		if err != nil {
			errMsg := "error running dropbox cleanup: "
			switch {
			// All the OK cases: exit 0
			case errors.Is(err, errNoFilesInDropbox), errors.Is(err, errNoFilesToDelete):
				funcLogger.With("experiment", k.String("experiment")).Info(err.Error())
				exitCode = 0
			// Other cases
			case errors.Is(err, errUsage):
				f.Usage()
				exitCode = 1
			case errors.Is(err, errNoVaultTokenFile), errors.Is(err, errVaultTokenTooOld):
				funcLogger.Error(err.Error(), "vaultTokenFile", k.String("vault.vaultTokenFile"))
				exitCode = 2
			case errors.Is(err, errNoSchedds):
				funcLogger.Error(errMsg + "No condor schedds found. Please check your condor pool configuration.")
				exitCode = 3
			case errors.Is(err, errScheddQueryFailed):
				funcLogger.Error(errMsg + "No condor schedds were queried successfully. Please check your condor pool configuration or this script's configuration")
				exitCode = 4
			case errors.As(err, &e1):
				funcLogger.Error(err.Error())
				exitCode = 5
			default:
				funcLogger.With("experiment", k.String("experiment")).Error(errMsg + err.Error())
				exitCode = 1
			}
		}
	}()
	os.Exit(exitCode)
}

func run(ctx context.Context, k *koanf.Koanf) error {
	funcLogger := logger.With("caller", "main.run")
	startSetup := time.Now()
	// 0. Checks and setup
	// 0a. Check for --e/--experiment flag
	s := k.String("experiment")
	if s == "" {
		funcLogger.Error("Experiment flag is required")
		return errUsage
	}

	// 0b. Parse all the configured durations to make sure they are valid.  If not, we will either
	// use the default values or return an error
	minTimeLeft, err := time.ParseDuration(k.String("vault.minVaultTokenTimeLeft"))
	if err != nil {
		funcLogger.Error("error parsing minimum vault token time left duration. Using default value", "error", err)
		minTimeLeft = defaultVaultTokenTimeLeft
	}
	fileAgeCutoff, err := time.ParseDuration(k.String("deleteFilesOlderThan"))
	if err != nil {
		funcLogger.Error("error parsing configured file age cutoff duration", "error", err)
		return err
	}

	// 0c. Check Vault Token
	funcLogger.Debug("Checking vault token file", "vaultTokenFile", k.String("vault.vaultTokenFile"))
	if err := checkVaultTokenFile(k.String("vault.vaultTokenFile"), k.String("vault.vaultTokenAgeCutoff")); err != nil {
		return fmt.Errorf("error checking vault token file: %w", err)
	}
	funcLogger.Debug("Vault token file exists and is new enough", "vaultTokenFile", k.String("vault.vaultTokenFile"))

	// 0d. Get our BEARER token to obtain files
	// We make sure that the vault token has enough time left before expiration (minTimeLeft).
	// We will pass this value to the htgettokenClient, which will run this check for us
	startGetBearerToken := time.Now()

	funcLogger.Debug("Getting BEARER token to get files list from dCache")
	h := newHtgettokenClient(
		k.String("vault.server"),
		k.String("vault.vaultTokenFile"),
		k.String("vault.bearerTokenFile"),
		fmt.Sprintf("--vaulttokenminttl=%ds", int(math.Round(minTimeLeft.Seconds()))),
	)
	if k.Bool("debug") {
		h = h.withDebug()
	}
	if k.String("vault.authMethod") == "kerberos" {
		funcLogger.Debug("Using kerberos authentication for htgettokenClient")
		h = h.withKerberosKeytabAuth(k.String("vault.kerberosKeytabPath"), k.String("vault.kerberosPrincipal"))
	}

	deleteBearerTokenAfterRun := false // We only want to delete a bearer token if we created it
	if _, err := os.Stat(h.outFile); err != nil && errors.Is(err, fs.ErrNotExist) {
		deleteBearerTokenAfterRun = true
	}
	tok, err := h.getToken(ctx, k.String("vault.experiment"), k.String("vault.role"))
	if err != nil {
		return fmt.Errorf("error getting and validating token: %w", err)
	}
	if deleteBearerTokenAfterRun {
		defer func() {
			// Clean up the token file after we're done
			if err = os.Remove(h.outFile); err != nil {
				funcLogger.Warn("Could not remove bearer token file", "file", h.outFile)
			}
			funcLogger.Debug("Removed bearer token file", "file", h.outFile)
		}()
	}

	promDuration.WithLabelValues("getBearerToken").Set(time.Since(startGetBearerToken).Seconds())
	promDuration.WithLabelValues("setup").Set(time.Since(startSetup).Seconds())

	// 1. Get files list from pnfs dropbox
	startGetDropboxFiles := time.Now()
	apiEndpoint := fixAPIEndpoint(k.String("dCache.apiEndpoint"))

	dClient := newDCacheClient(
		string(tok),
		apiEndpoint,
		k.Int("dCache.totalFileCountLimit"),
		uint(k.Int("dCache.retryCount")),
		k.Duration("dCache.retrySleep"),
		true,
	)

	exptNameOverride := k.StringMap("exptNameOverride")
	exptArea := k.String("experiment") + "/resilient/jobsub_stage/"
	if override, ok := exptNameOverride[k.String("experiment")]; ok {
		exptArea = override
	}

	dCacheHostPort := strings.TrimRight(k.String("dCache.hostPort"), "/")
	source := dCacheHostPort + apiEndpoint + exptArea

	funcLogger.Info("Looking for files to delete in path", "dir", source, "experiment", k.String("experiment"))
	fileIsTooNew := func(f *FileEntry) bool {
		return fileIsRecent(f, fileAgeCutoff)
	}
	filesList, err := dClient.getFilesList(ctx, source, nil, nil, fileIsTooNew)
	var lastFileProcessed string
	if err2, ok := errors.AsType[*errFileCountLimitExceeded](err); ok {
		funcLogger.Warn("file count limit exceeded. Stopping collecting files now")
		funcLogger.With("file", (*err2).filename).Debug("Last processed file")
		lastFileProcessed = (*err2).filename
	} else if err2, ok := errors.AsType[*errProcessingFiles](err); ok {
		errsLeft := make([]error, 0, len(err2.errors))
		// Files flagged for exclusion
		for _, e := range err2.errors {
			if err3, ok := errors.AsType[*errFileFlaggedToExclude](e); ok {
				funcLogger.Warn("file excluded by excludeFunc", "file", err3.filename)
				continue
			}
			errsLeft = append(errsLeft, e) // Keep the other errors to warn about later
		}
		if len(errsLeft) > 0 {
			funcLogger.Warn("partial success occurred while collecting files", "errors", errsLeft)
		}
	} else if err != nil {
		return fmt.Errorf("error getting dropbox files list: %w", err)
	}

	if len(filesList) == 0 {
		return errNoFilesInDropbox
	}

	// Create a file map to hold the filenames and quickly eliminate files we don't want to delete
	fileMap := make(fileEntryMap, 0)
	for _, file := range filesList {
		fileMap[file.Name()] = file
		funcLogger.Debug("File entry", "file", file.Name())
	}
	funcLogger.Debug("Got dropbox files successfully")
	getDropboxFilesListByExptDuration.WithLabelValues(k.String("experiment")).Set(time.Since(startGetDropboxFiles).Seconds())
	promDuration.WithLabelValues("getDropboxFiles").Set(time.Since(startGetDropboxFiles).Seconds())

	// 2. Get job files from condor schedds
	startGetCondorFiles := time.Now()
	// 2a. Find our schedds
	funcLogger.Debug("Finding condor cluster schedds")
	schedds, err := getCondorSchedds(ctx, k.String("condor.pool"), k.String("condor.scheddConstraint"))
	if err != nil {
		return fmt.Errorf("error getting condor schedds: %w", err)
	}
	if len(schedds) == 0 {
		return errNoSchedds
	}

	if k.Bool("debug") {
		scheddNames := make([]string, 0, len(schedds))
		for _, sch := range schedds {
			scheddNames = append(scheddNames, sch.name)
		}
		funcLogger.Debug("Got condor schedds", "schedds", scheddNames)
	}

	// 2b. Set up schedd auth
	authMethod := newCondorAuthMethod(k.String("condor.authMethod"))
	if authMethod == UNSUPPORTED {
		funcLogger.Warn("Unsupported condor authentication method. Using default", "method", k.String("condor.authMethod"), "default", defaultCondorAuthMethod)
		authMethod = newCondorAuthMethod(defaultCondorAuthMethod)
	}

	cleanupEnv := authMethod.setupEnv()
	defer cleanupEnv()

	// 2c. Get the condor job files
	funcLogger.Info("Getting condor job files")
	jobFiles := make(map[string]struct{}, 0)

	// queriedScheddSuccessfully will be true if we successfully queried at least one schedd. If it remains false, we will not delete
	// any files
	queriedScheddSuccessfully := false

	// Now query our schedds for the files in use by condor jobs
	for _, sch := range schedds {
		scheddFiles, err := getScheddFiles(ctx, sch, authMethod, k.String("experiment"), k.String("condor.jobConstraint"))
		if err != nil {
			funcLogger.Error("error getting condor job files from schedd", "error", err, "schedd", sch.name)
			continue
		}
		queriedScheddSuccessfully = true
		for _, file := range scheddFiles {
			jobFiles[file] = struct{}{}
		}
	}

	if !queriedScheddSuccessfully {
		funcLogger.Error("No condor schedds were queried successfully")
		return errScheddQueryFailed
	}

	if k.Bool("debug") {
		for file := range jobFiles {
			funcLogger.Debug("Job file", "file", file)
		}
	}

	promDuration.WithLabelValues("getCondorFiles").Set(time.Since(startGetCondorFiles).Seconds())

	// 3. Remove any files from our delete list that are being used by condor jobs
	startFilterFiles := time.Now()

	funcLogger.Debug("Are the files being used by condor jobs?")
	// We can safely delete keys and future iterations' keys while ranging over the map
	// See https://stackoverflow.com/a/23230406
	for _, entry := range fileMap {
		if _, ok := jobFiles[entry.Name()]; ok {
			funcLogger.Debug("File is in job files, so we will not delete it:", "filename", entry.Name())
			removeFileAndAncestorsFromDeleteList(entry, fileMap)
		}
	}

	// If we have no files left to delete, or if we're in test mode, we can stop here
	if len(fileMap) == 0 {
		promDuration.WithLabelValues("filterFiles").Set(time.Since(startFilterFiles).Seconds())
		return errNoFilesToDelete
	}
	if k.Bool("test") {
		funcLogger.Info("Running in test mode. No files will be deleted")
		funcLogger.Info("Would have deleted the following files:")
		for name := range fileMap {
			funcLogger.Info(name, "filename", name, "modified", fileMap[name].modified, "isDirectory", fileMap[name].isDirectory)
		}
		funcLogger.Info("Stopping here")
		promDuration.WithLabelValues("filterFiles").Set(time.Since(startFilterFiles).Seconds())
		return nil
	}

	funcLogger.Debug("Remaining files to delete:")
	for name := range fileMap {
		funcLogger.Debug("", "filename", name, "modified", fileMap[name].modified, "isDirectory", fileMap[name].isDirectory)
	}

	promDuration.WithLabelValues("filterFiles").Set(time.Since(startFilterFiles).Seconds())

	// 4. Delete files and directories
	stripPNFSFromPath := func(pnfsPath string) string { return strings.TrimPrefix(pnfsPath, "/pnfs") }
	startDeleteFiles := time.Now()
	deleteFilesErrs := &errDeletingFiles{files: make([]string, 0, len(fileMap))}
	// 4a. Delete files in our delete list
	// Note:  This isn't as slick as recursion, but the former used way more memory, and actually made the program get killed by the OOM killer
	// Do a pass where we start with deleting files, then their parents if they're empty
	funcLogger.Info("Deleting files")
	deletedFilenames := make([]string, 0)
	for filename := range fileMap.AllNonDirFilesNames() {
		funcLogger.Debug("Deleting file:", "filename", filename)
		// Remove the file
		err := dClient.removeFile(ctx, PNFSToHTTPS(filename, dCacheHostPort, apiEndpoint, stripPNFSFromPath))
		if err != nil {
			funcLogger.Error("error deleting file", "error", err)
			deleteFilesErrs.files = append(deleteFilesErrs.files, filename)
			numErrorsDeletingFiles.WithLabelValues(k.String("experiment")).Inc()
			continue
		}
		// Remove the file from our map and from its parent's containsFiles slice
		if fileMap[filename].parent != nil {
			fileMap[filename].parent.containsFiles = slices.DeleteFunc(
				fileMap[filename].parent.containsFiles,
				func(f *FileEntry) bool {
					return f.Name() == filename
				},
			)
		}
		deletedFilenames = append(deletedFilenames, filename)
		numFilesDeleted.WithLabelValues(k.String("experiment")).Inc()
		funcLogger.Info("File deleted", "filename", filename, "dateModified", fileMap[filename].modified)
	}

	for _, filename := range deletedFilenames {
		delete(fileMap, filename)
	}

	if len(fileMap) == 0 {
		promDuration.WithLabelValues("deleteFiles").Set(time.Since(startDeleteFiles).Seconds())
		return errNoFilesToDelete
	}

	// 4b. Delete empty directories
	funcLogger.Info("Deleting empty directories")
	for filename := range fileMap.AllDirNames() {
		if len(fileMap[filename].containsFiles) != 0 {
			funcLogger.Info("Directory is not empty, so we will not delete it:", "dirName", filename)
			continue
		}

		// If we stopped processing files because we hit the file count limit mid-directory, then we don't know for sure
		// if we saw all the files in this directory; thus we can stop and leave cleanup of the directory to a future run.
		if lastFileProcessed != "" && path.Dir(lastFileProcessed) == filename {
			funcLogger.Debug("Directory did not get all files processed, so we will not delete it", "dirName", filename)
			break
		}

		// Don't delete the experiment area. This should never happen, but the safeguard is here
		// just in case
		if filename == path.Join("/pnfs/", exptArea) {
			funcLogger.Info("Skipping experiment area", "dirName", filename)
			continue
		}

		funcLogger.Debug("Deleting directory", "dirName", filename)
		// Remove the file
		err := dClient.removeFile(ctx, PNFSToHTTPS(filename, dCacheHostPort, apiEndpoint, stripPNFSFromPath))
		if err != nil {
			funcLogger.Error("error deleting directory", "error", err)
			deleteFilesErrs.files = append(deleteFilesErrs.files, filename)
			numErrorsDeletingFiles.WithLabelValues(k.String("experiment")).Inc()
			continue
		}

		numFilesDeleted.WithLabelValues(k.String("experiment")).Inc()
		funcLogger.Info("Empty directory deleted", "dirName", filename, "dateModified", fileMap[filename].modified)

		// Make sure we don't delete root
		// Keep walking up the tree and deleting empty directories recursively
		_parent := fileMap[filename].parent
		for _parent != nil {
			// Remove the file from our map and from its parent's containsFiles slice
			_parent.containsFiles = slices.DeleteFunc(
				_parent.containsFiles,
				func(f *FileEntry) bool {
					return f.Name() == filename
				},
			)

			// If we stopped processing files because we hit the file count limit mid-directory, then we don't know for sure
			// if we saw all the files in _parent; thus we can stop and leave cleanup of _parent to a future run.
			if lastFileProcessed != "" && path.Dir(lastFileProcessed) == _parent.Name() {
				funcLogger.Debug("Parent did not get all files processed, so we will not delete it", "dirName", _parent.Name())
				break
			}

			// Check if the parent is empty. If not, we can stop
			if len(_parent.containsFiles) != 0 {
				funcLogger.Debug("Parent is not empty, so we will not delete it", "dirName", _parent.Name())
				break
			}
			// Don't delete the experiment area. This should never happen, but the safeguard is here
			// just in case
			if _parent.Name() == path.Join("/pnfs/", exptArea) {
				funcLogger.Info("Skipping experiment area", "dirName", _parent.Name())
				continue
			}

			// Delete parent directory, since we've established that it's empty
			funcLogger.Debug("Parent is empty, so we will delete it", "dirName", _parent.Name())
			err := dClient.removeFile(ctx, PNFSToHTTPS(_parent.Name(), dCacheHostPort, apiEndpoint, stripPNFSFromPath))
			if err != nil {
				funcLogger.Error("error deleting directory", "error", err)
				deleteFilesErrs.files = append(deleteFilesErrs.files, _parent.Name())
				numErrorsDeletingFiles.WithLabelValues(k.String("experiment")).Inc()
				break
			}
			numFilesDeleted.WithLabelValues(k.String("experiment")).Inc()
			funcLogger.Info("Empty directory deleted", "dirName", _parent.Name(), "dateModified", fileMap[filename].modified)
			_parent = _parent.parent
		}
	}

	if len(deleteFilesErrs.files) > 0 {
		funcLogger.Error(deleteFilesErrs.Error())
		return deleteFilesErrs
	}

	promDuration.WithLabelValues("deleteFiles").Set(time.Since(startDeleteFiles).Seconds())
	return nil
}

var (
	errUsage             = errors.New("command malformed")
	errNoFilesInDropbox  = errors.New("no files found in dropbox")
	errNoSchedds         = errors.New("no condor schedds found")
	errScheddQueryFailed = errors.New("no condor schedds were queried successfully")
	errNoFilesToDelete   = errors.New("no more files to delete")
)

// errDeletingFiles is an error type that holds a list of files that could not be deleted
type errDeletingFiles struct {
	files []string
}

func (e *errDeletingFiles) Error() string {
	return fmt.Sprintf("error deleting files: %v", e.files)
}
