package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	flag "github.com/spf13/pflag"
)

var now = time.Now()
var exeMap = map[string]string{} // Map of all the executables we will need to find in PATH

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

// Config
var k = koanf.New(".") // Config path delimiter is "."

func main() {
	// Read flags
	f := flag.NewFlagSet("jobsub-pnfs-dropbox-cleanup", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println("Usage: jobsub-pnfs-dropbox-cleanup [options]")
		fmt.Println("Options:")
		f.PrintDefaults()
	}
	f.StringP("experiment", "e", "", "Experiment name to use for dropbox cleanup")
	f.StringP("config", "c", defaultConfigFilePath, "Config file to load (default: /etc/jobsub-pnfs-dropbox-cleanup.yml)")
	f.BoolP("debug", "d", false, "Enable debug logging")
	f.BoolP("test", "t", false, "Run in test mode (no actual deletions)")

	f.Parse(os.Args[1:])

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

	// Set up logging
	if k.Bool("debug") {
		slog.SetLogLoggerLevel(slog.LevelDebug)
		slog.Debug("Debug logging enabled")
	}
	slog.Info("Initialized logging")

	// Set up our context with timeout
	timeout, err := time.ParseDuration(k.String("timeout"))
	if err != nil {
		slog.Error("error getting timeout from config. Using default timeout", "error", err, "defaultTimeout", defaultTimeout)
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err = run(ctx, k)
	if err != nil {
		switch {
		case errors.Is(err, errUsage):
			f.Usage()
			os.Exit(1)
		case errors.Is(err, errVaultTokenTooOld):
			slog.Error(err.Error(), "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", k.String("vault.vaultTokenAgeCutoff"))
			os.Exit(2)
		case errors.Is(err, errNoFilesInDropbox):
			slog.Info("No files found in dropbox. Nothing to delete.")
			os.Exit(0)
		case errors.Is(err, errNoSchedds):
			slog.Error("No condor schedds found. Please check your condor pool configuration.")
			os.Exit(3)
		case errors.Is(err, errScheddQueryFailed):
			slog.Error("No condor schedds were queried successfully. Please check your condor pool configuration or this script's configuration")
			os.Exit(4)
		case errors.Is(err, errNoFilesToDelete):
			slog.Info("No files to delete. Exiting")
			os.Exit(0)
		}
	}
}

func run(ctx context.Context, k *koanf.Koanf) error {
	// 0. Checks and setup
	// 0a. Check for --e/--experiment flag
	s := k.String("experiment")
	if s == "" {
		slog.Error("Experiment flag is required")
		return errUsage
	}

	// 0b. Check Vault Token
	vaultTokenAgeCutoff, err := time.ParseDuration(k.String("vault.vaultTokenAgeCutoff"))
	if err != nil {
		slog.Error("error parsing vault token age cutoff duration. Using default vault token age cutoff", "error", err, "defaultVaultTokenAgeCutoff", defaultVaultTokenAgeCutoff)
		vaultTokenAgeCutoff = defaultVaultTokenAgeCutoff
	}

	// 0ba. Is vault token file new enough?
	slog.Debug("Ensuring vault token is available, and new enough", "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", vaultTokenAgeCutoff)
	stat, err := os.Stat(k.String("vault.vaultTokenFile"))
	if err != nil {
		slog.Error("error getting file information about vault token file. Exiting", "error", err)
		return err
	}
	if stat.ModTime().Add(vaultTokenAgeCutoff).Before(now) { // File is older than vaultTokenAgeCutoff
		slog.Error("vault token file is older than the time cutoff. Exiting", "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", vaultTokenAgeCutoff)
		return errVaultTokenTooOld
	}
	slog.Debug("Vault token file exists and is new enough", "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", vaultTokenAgeCutoff)

	// 0bb. Make sure that vault token has enough time left before expiration
	minTimeLeft, err := time.ParseDuration(k.String("vault.minVaultTokenTimeLeft"))
	if err != nil {
		slog.Error("error parsing minimum vault token time left duration. Using default value", "error", err)
		minTimeLeft = defaultVaultTokenTimeLeft
	}

	// 0c. Get our BEARER token to obtain files
	slog.Debug("Getting BEARER token to get files list from dCache")
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
		h = h.withKerberosKeytabAuth(k.String("vault.kerberosKeytabPath"), k.String("vault.kerberosPrincipal"))
	}

	tok, err := h.getToken(ctx, k.String("vault.experiment"), k.String("vault.role"))
	if err != nil {
		slog.Error("error getting and validating token", "error", err)
		return err
	}

	// 1. Get files list from pnfs dropbox
	addedEnvironment := []string{"BEARER_TOKEN=" + string(tok)}
	var retryDuration time.Duration
	retryDuration, err = time.ParseDuration(k.String("gfal2.retrySleep"))
	if err != nil {
		slog.Error("error parsing gfal2 retry sleep duration. Will use default", "error", err)
		retryDuration = 0
	}

	gClient := newGfal2Client(k.Int("totalFileCountLimit"), uint(k.Int("gfal2.retryCount")), retryDuration, addedEnvironment)
	dClient := newDCacheClient(string(tok), true)

	exptNameOverride := k.StringMap("exptNameOverride")
	exptArea := k.String("experiment") + "/resilient/jobsub_stage/"
	if override, ok := exptNameOverride[k.String("experiment")]; ok {
		exptArea = override
	}

	dCacheHostPort := strings.TrimRight(k.String("dCacheHostPort"), "/")
	source := dCacheHostPort + "/" + exptArea
	slog.Info("Looking for files to delete in path", "dir", source, "experiment", k.String("experiment"))

	filesList, err := gClient.getFilesList(ctx, source, nil, nil)
	switch {
	case errors.Is(err, errFileCountLimitExceeded):
		slog.Error("file count limit exceeded. Stopping collecting files now")
	case err != nil:
		slog.Error("error getting files list", "error", err)
		return err
	}

	if len(filesList) == 0 {
		slog.Info("No files found in dropbox")
		return errNoFilesInDropbox
	}

	// Create a file map to hold the filenames and quickly eliminate files we don't want to delete
	fileMap := make(fileEntryMap, 0)
	for _, file := range filesList {
		fileMap[file.Name()] = file
		slog.Debug("File entry", "file", file.Name())
	}

	// 2. Get job files from condor schedds
	// 2a. Find our schedds
	schedds, err := getCondorSchedds(ctx, k.String("condor.pool"), k.String("condor.scheddConstraint"))
	if err != nil {
		slog.Error("error getting condor schedds:", "error", err)
		return err
	}
	if len(schedds) == 0 {
		slog.Error("no condor schedds found. Exiting")
		return errNoSchedds
	}

	if k.Bool("debug") {
		scheddNames := make([]string, 0, len(schedds))
		for _, sch := range schedds {
			scheddNames = append(scheddNames, sch.name)
		}
		slog.Debug("Got condor schedds", "schedds", scheddNames)
	}

	// 2b. Set up schedd auth
	authMethod := newCondorAuthMethod(k.String("condor.authMethod"))
	if authMethod == UNSUPPORTED {
		slog.Warn("Unsupported condor authentication method. Using default", "method", k.String("condor.authMethod"), "default", defaultCondorAuthMethod)
		authMethod = newCondorAuthMethod(defaultCondorAuthMethod)
	}

	cleanupEnv := authMethod.setupEnv()
	defer cleanupEnv()

	// 2c. Get the condor job files
	slog.Info("Getting condor job files")
	jobFiles := make(map[string]struct{}, 0)

	// queriedScheddSuccessfully will be true if we successfully queried at least one schedd. If it remains false, we will not delete
	// any files
	queriedScheddSuccessfully := false

	// Now query our schedds for the files in use by condor jobs
	for _, sch := range schedds {
		func() {
			sch.cmdEnv = os.Environ()

			err := sch.verify(ctx, authMethod)
			if err != nil {
				slog.Error("error verifying authorization to condor schedd:", "error", err, "schedd", sch.name, "authMethod", authMethod.String())
				return
			}

			ads, err := sch.getPNFSJobsForExperiment(ctx, k.String("experiment"), k.String("condor.jobConstraint"))
			if err != nil {
				slog.Error("error getting PNFS jobs:", "error", err, "schedd", sch.name)
				return
			}
			queriedScheddSuccessfully = true
			for _, ad := range ads {
				scheddFiles, err := sch.getDropboxFilesFromJob(ad)
				if err != nil {
					slog.Error("error getting dropbox files from job:", "error", err, "schedd", sch.name) // TODO Get job ID?
					continue
				}
				slog.Debug("Got schedd dropbox files", "schedd", sch.name, "files", scheddFiles)
				for _, file := range scheddFiles {
					jobFiles[file] = struct{}{}
				}
			}
		}()
	}
	if !queriedScheddSuccessfully {
		slog.Error("No condor schedds were queried successfully. Exiting")
		return errScheddQueryFailed
	}

	slog.Debug("", "jobFiles", jobFiles)

	// 3. Remove any files from our delete list that are in the list of job files or are recent
	// We are iterating a second time to check if the files are recent, which may not be totally efficient, but it should improve readability
	// Maybe if we have performance problems, we first get the list of job files, then pass in a filter function to our tree-builder that could check
	// for recency or job file membership
	fileAgeCutoff, err := time.ParseDuration(k.String("deleteFilesOlderThan"))
	if err != nil {
		slog.Error("error parsing configured file age cutoff duration", "error", err)
		return err
	}

	slog.Debug("Are the files not recent or being used by condor jobs?")
	for _, entry := range fileMap {
		func(e *FileEntry) {
			removeFileAndAncestorsFromDeleteList := func() {
				delete(fileMap, e.Name())
				_parent := e.parent
				for _parent != nil {
					slog.Debug("Removing parent from deletion list", "fileEntry.parent", _parent.Name())
					delete(fileMap, _parent.Name())
					_parent = _parent.parent
				}
			}
			if _, ok := jobFiles[e.Name()]; ok {
				slog.Debug("File is in job files, so we will not delete it:", "filename", e.Name())
				removeFileAndAncestorsFromDeleteList()
				return
			}
			if fileIsRecent(e, fileAgeCutoff) {
				slog.Debug("File is recent, so we will not delete it:", "filename", e.Name())
				removeFileAndAncestorsFromDeleteList()
			}
		}(entry)
	}

	if len(fileMap) == 0 {
		slog.Info("No files to delete. Exiting")
		return errNoFilesToDelete
	}
	if k.Bool("test") {
		slog.Info("Running in test mode. No files will be deleted")
		slog.Info("Would have deleted the following files:")
		for name := range fileMap {
			slog.Info("", "filename", name, "created", fileMap[name].created, "isDirectory", fileMap[name].isDirectory)
		}
		slog.Info("Stopping here")
		return nil
	}

	slog.Debug("Remaining files to delete:")
	for name := range fileMap {
		slog.Debug("", "filename", name, "created", fileMap[name].created, "isDirectory", fileMap[name].isDirectory)
	}

	// 4. Delete files and directories
	// 4a. Delete files in our delete list
	// Note:  This isn't as slick as recursion, but the former used way more memory, and actually made the program get killed by the OOM killer
	// Do a pass where we start with deleting files, then their parents if they're empty
	slog.Info("Deleting files")
	deletedFilenames := make([]string, 0)
	for filename := range fileMap.AllNonDirFilesNames() {
		slog.Debug("Deleting file:", "filename", filename)
		// Remove the file
		err := dClient.removeFile(ctx, PNFSToHTTPS(filename, dCacheHostPort, stripPNFSFromPath))
		if err != nil {
			slog.Error("error deleting file", "error", err)
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
		slog.Info("File deleted", "filename", filename, "dateCreated", fileMap[filename].created)
	}

	for _, filename := range deletedFilenames {
		delete(fileMap, filename)
	}

	if len(fileMap) == 0 {
		slog.Info("No files left to delete. Exiting")
		return errNoFilesToDelete
	}

	// 4b. Delete empty directories
	slog.Info("Deleting empty directories")
	// deletedFilenames = make([]string, 0)
	for filename := range fileMap.AllDirNames() {
		if len(fileMap[filename].containsFiles) != 0 {
			slog.Info("Directory is not empty, so we will not delete it:", "dirName", filename)
			continue
		}

		slog.Debug("Deleting directory", "dirName", filename)
		// Remove the file
		err := dClient.removeFile(ctx, PNFSToHTTPS(filename, dCacheHostPort, stripPNFSFromPath))
		if err != nil {
			slog.Error("error deleting directory", "error", err)
			continue
		}

		// deletedFilenames = append(deletedFilenames, filename)
		slog.Info("Empty directory deleted", "dirName", filename, "dateCreated", fileMap[filename].created)

		// Keep walking up the tree and deleting empty directories recursively
		// Remove the file from our map and from its parent's containsFiles slice
		_parent := fileMap[filename].parent
		for _parent != nil {
			_parent.containsFiles = slices.DeleteFunc(
				_parent.containsFiles,
				func(f *FileEntry) bool {
					return f.Name() == filename
				},
			)
			// Check if the parent is empty. If not, we can stop
			if len(_parent.containsFiles) != 0 {
				slog.Debug("Parent is not empty, so we will not delete it", "dirName", _parent.Name())
				break
			}
			// Delete parent directory, since we've established that it's empty
			slog.Debug("Parent is empty, so we will delete it", "dirName", _parent.Name())
			err := dClient.removeFile(ctx, PNFSToHTTPS(filename, dCacheHostPort, stripPNFSFromPath))
			if err != nil {
				slog.Error("error deleting directory", "error", err)
				break
			}
			// deletedFilenames = append(deletedFilenames, filename)
			_parent = _parent.parent
		}
	}

	return nil
}

var (
	errUsage             = errors.New("command malformed")
	errVaultTokenTooOld  = errors.New("vault token file is older than the time cutoff")
	errNoFilesInDropbox  = errors.New("no files found in dropbox")
	errNoSchedds         = errors.New("no condor schedds found")
	errScheddQueryFailed = errors.New("no condor schedds were queried successfully")
	errNoFilesToDelete   = errors.New("no files to delete")
)
