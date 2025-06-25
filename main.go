package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"math"
	"net/url"
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

// TODO implement timeout
// TODO implement looking for executables in PATH

var now = time.Now()

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

// Config holders
var (
	k = koanf.New(".") // Config path delimiter is "."
	f = flag.NewFlagSet("jobsub-pnfs-dropbox-cleanup", flag.ContinueOnError)
)

func init() {
	initConfigAndFlags()
	initLogs()
}

func initConfigAndFlags() {
	// Read flags
	f.Usage = func() {
		fmt.Println("Usage: jobsub-pnfs-dropbox-cleanup [options]")
		fmt.Println("Options:")
		f.PrintDefaults()
		os.Exit(0)
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

	if err := k.Load(file.Provider(configFilePath), yaml.Parser()); err != nil {
		panic(fmt.Sprintf("error loading config file: %v", err))
	}

	// Add flags to override config file values
	if err := k.Load(posflag.Provider(f, ".", k), nil); err != nil {
		panic(fmt.Sprintf("error loading config: %v", err))
	}

}

func initLogs() {
	// Set up logging
	if k == nil {
		slog.Warn("koanf instance is nil, using default logging level", "level", "INFO")
		return
	}

	if k.Bool("debug") {
		slog.SetLogLoggerLevel(slog.LevelDebug)
		slog.Debug("Debug logging enabled")
	}
	slog.Info("Initialized logging")
}

func main() {
	s, err := f.GetString("experiment")
	if err != nil || s == "" {
		slog.Error("Experiment flag is required")
		f.Usage()
		os.Exit(1)
	}

	// Set up our context with timeout
	timeout, err := time.ParseDuration(k.String("timeout"))
	if err != nil {
		slog.Error("error getting timeout from config. Using default timeout", "error", err, "defaultTimeout", defaultTimeout)
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Get token
	vaultTokenAgeCutoff, err := time.ParseDuration(k.String("vault.vaultTokenAgeCutoff"))
	if err != nil {
		slog.Error("error parsing vault token age cutoff duration. Using default vault token age cutoff", "error", err, "defaultVaultTokenAgeCutoff", defaultVaultTokenAgeCutoff)
		vaultTokenAgeCutoff = defaultVaultTokenAgeCutoff
	}

	// First, make sure we have a vault token that new enough. Read from /var/lib/jobsub-pnfs-dropbox-cleanup/vt_token
	slog.Debug("Ensuring vault token is available, and new enough", "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", vaultTokenAgeCutoff)
	stat, err := os.Stat(k.String("vault.vaultTokenFile"))
	if err != nil {
		slog.Error("error getting file information about vault token file. Exiting", "error", err)
		return
	}

	if stat.ModTime().Add(vaultTokenAgeCutoff).Before(now) { // File is older than vaultTokenAgeCutoff
		slog.Error("vault token file is older than the time cutoff. Exiting", "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", vaultTokenAgeCutoff)
		return
	}
	slog.Debug("Vault token file exists and is new enough", "vaultTokenFile", k.String("vault.vaultTokenFile"), "vaultTokenAgeCutoff", vaultTokenAgeCutoff)

	minTimeLeft, err := time.ParseDuration(k.String("vault.minVaultTokenTimeLeft"))
	if err != nil {
		slog.Error("error parsing minimum vault token time left duration. Using default value", "error", err)
		minTimeLeft = defaultVaultTokenTimeLeft
	}

	slog.Debug("Getting BEARER token to do cleanup")

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
		h = h.withKerberosKeytabAuth(ctx, k.String("vault.kerberosKeytabPath"), k.String("vault.kerberosPrincipal"))
	}

	tok, err := h.getToken(ctx, k.String("vault.experiment"), k.String("vault.role"))
	if err != nil {
		slog.Error("error getting and validating token", "error", err)
		return
	}

	// Get files
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
		return
	}

	if len(filesList) == 0 {
		slog.Info("No files found in dropbox. Exiting")
		return
	}

	// Create a file map to hold the filenames and quickly eliminate files we don't want to delete
	fileMap := make(fileEntryMap, 0)
	for _, file := range filesList {
		fileMap[file.Name()] = file
		slog.Debug("File entry", "file", file.Name())
	}

	// Now, we need to get the condor job files
	slog.Info("Getting condor job files")
	jobFiles := make(map[string]struct{}, 0)
	schedds, err := getCondorSchedds(ctx, k.String("condor.pool"), k.String("condor.scheddConstraint"))
	if err != nil {
		slog.Error("error getting condor schedds:", "error", err)
		return
	}
	if len(schedds) == 0 {
		slog.Error("no condor schedds found. Exiting")
		return
	}

	if k.Bool("debug") {
		scheddNames := make([]string, 0, len(schedds))
		for _, sch := range schedds {
			scheddNames = append(scheddNames, sch.name)
		}
		slog.Debug("Got condor schedds", "schedds", scheddNames)
	}

	// Set up schedd auth
	_auth := k.String("condor.authMethod")
	authMethod := newCondorAuthMethod(_auth)
	if authMethod == UNSUPPORTED {
		slog.Warn("Unsupported condor authentication method", "method", _auth, "using default", defaultCondorAuthMethod)
		authMethod = newCondorAuthMethod(defaultCondorAuthMethod)
	}

	cleanupEnv := authMethod.setupEnv()
	defer cleanupEnv()

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
		return
	}

	slog.Debug("", "jobFiles", jobFiles)

	// Remove any files from our delete list that are in the list of job files or are recent
	// We are iterating a second time to check if the files are recent, which may not be totally efficient, but it should improve readability
	// Maybe if we have performance problems, we first get the list of job files, then pass in a filter function to our tree-builder that could check
	// for recency or job file membership
	fileAgeCutoff, err := time.ParseDuration(k.String("deleteFilesOlderThan"))
	if err != nil {
		slog.Error("error parsing configured file age cutoff duration", "error", err)
		return
	}

	slog.Debug("Are the files not recent or being used by condor jobs?")
	for _, entry := range fileMap {
		func(file *FileEntry) {
			removeFileAndAncestorsFromDeleteList := func() {
				delete(fileMap, file.Name())
				parent := file.parent
				for parent != nil {
					slog.Debug("Removing parent from deletion list", "fileEntry.parent", parent.Name())
					delete(fileMap, parent.Name())
					parent = parent.parent
				}
			}
			if _, ok := jobFiles[file.Name()]; ok {
				slog.Debug("File is in job files, so we will not delete it:", "filename", file.Name())
				removeFileAndAncestorsFromDeleteList()
				return
			}
			if fileIsRecent(file, fileAgeCutoff) {
				slog.Debug("File is recent, so we will not delete it:", "filename", file.Name())
				removeFileAndAncestorsFromDeleteList()
			}
		}(entry)
	}

	if len(fileMap) == 0 {
		slog.Info("No files to delete. Exiting")
		return
	}

	if k.Bool("test") {
		slog.Info("Running in test mode. No files will be deleted")
		slog.Info("Would have deleted the following files:")
		for name := range fileMap {
			slog.Info("", "filename", name, "created", fileMap[name].created, "isDirectory", fileMap[name].isDirectory)
		}
		slog.Info("Stopping here")
		return
	}

	slog.Debug("Remaining files to delete:")
	for name := range fileMap {
		slog.Debug("", "filename", name, "created", fileMap[name].created, "isDirectory", fileMap[name].isDirectory)
	}

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
		return
	}

	slog.Info("Deleting empty directories")
	deletedFilenames = make([]string, 0)
	// for filename := range maps.Keys(fileMap) {
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

		deletedFilenames = append(deletedFilenames, filename)
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
				// TODO Handle error
				slog.Error("error deleting directory", "error", err)
				break
			}
			deletedFilenames = append(deletedFilenames, filename)
			_parent = _parent.parent
		}
	}
}

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

// TODO Move this to a different file
type fileEntryMap map[string]*FileEntry

// TODO Move this to a different file
// TODO Write tests for this
func flattenEntryTree(entries []*FileEntry) []*FileEntry {
	flatEntries := make([]*FileEntry, 0)
	for _, entry := range entries {
		flatEntries = append(flatEntries, entry)
		if len(entry.containsFiles) > 0 {
			flatEntries = append(flatEntries, flattenEntryTree(entry.containsFiles)...)
		}
	}
	return flatEntries
}

// TODO move somewhere else

// Iterator to return all directory entries' names
func (f fileEntryMap) AllDirNames() iter.Seq[string] {
	return func(yield func(string) bool) {
		for name := range f {
			if f[name].isDirectory {
				if !yield(name) {
					return
				}
			}
		}
	}
}

// TODO Move somewhere else
// Iterator to return all directory entries
func (f fileEntryMap) AllDirs() iter.Seq2[string, *FileEntry] {
	return func(yield func(string, *FileEntry) bool) {
		for name, entry := range f {
			if entry.isDirectory {
				if !yield(name, entry) {
					return
				}
			}
		}
	}
}

// TODO Move somewhere else
// Iterator to return all non-directory entries
func (f fileEntryMap) AllNonDirFilesNames() iter.Seq[string] {
	return func(yield func(string) bool) {
		for name := range f {
			if !f[name].isDirectory {
				if !yield(name) {
					return
				}
			}
		}
	}
}

// TODO Move somewhere else
// Iterator to return all non-directory entries
func (f fileEntryMap) AllNonDirFiles() iter.Seq2[string, *FileEntry] {
	return func(yield func(string, *FileEntry) bool) {
		for name, entry := range f {
			if !entry.isDirectory {
				if !yield(name, entry) {
					return
				}
			}
		}
	}
}

// TODO Move this to a different file
func urlToFilename(URL string, filenameTransformFunc func(string) string) string {
	sourceURL, err := url.Parse(URL)
	if err != nil {
		// TODO Handle error
		slog.Error("error parsing URL", "error", err)
		return ""
	}
	return filenameTransformFunc(sourceURL.Path)
}

func stripPNFSFromPath(pnfsPath string) string {
	// parts := filepath.SplitList(pnfsPath)
	// fmt.Println("Parts:", parts)
	// if parts[0] != "/pnfs" {
	// 	return ""
	// }
	// return "/" + strings.Join(parts[1:], "/")
	// TODO see if there's a better way than this
	return strings.TrimPrefix(pnfsPath, "/pnfs")
}

// TODO Move this to a different file
func PNFSToHTTPS(pnfsPath string, urlHostPort string, filenameTransformFunc func(string) string) string {
	// u, err := url.Parse("https://fndcadoor.fnal.gov:2880")
	u, err := url.Parse(urlHostPort)
	if err != nil {
		slog.Error("error parsing URL", "error", err)
		return ""
	}
	return u.JoinPath(filenameTransformFunc(pnfsPath)).String()
}

// TODO Move this to a different file
// Returns list of deleted files
// TODO Implement this
// func deleteEmptyDirectoriesAndAncestors(ctx context.Context, client *gfal2Client) error {
// 	// TODO Implement this
// 	return nil
// }

// TODO Move this to a different file
// Check membership in deleteMap.  Maybe we pass this in as a dynamic filter function in a future version to make it more flexible and testable
// func tryDeleteFilesRecursively(ctx context.Context, entry *FileEntry, client *gfal2Client, deleteMap fileEntryMap, prevDeletedFiles []string) ([]string, error) {
// 	// TODO DEBUG
// 	fmt.Println("Trying to delete files recursively.  Entry:", entry.Name())
// 	// END DEBUG

// 	var retErr *errDeleteFiles
// 	errFiles := make([]string, 0)

// 	// Cases
// 	if entry.isDirectory {
// 		// Case: If the entry is a directory and not empty, recursively call this function on each of its children
// 		if len(entry.containsFiles) != 0 {
// 			for _, childFile := range entry.containsFiles {
// 				deletedFiles, err := tryDeleteFilesRecursively(ctx, childFile, client, deleteMap, prevDeletedFiles)
// 				if err != nil {
// 					var testErr *errDeleteFiles
// 					// TODO Handle error properly. Should use errDeleteFiles
// 					fmt.Println("error deleting files recursively. Will continue:", err)
// 					if errors.As(err, &testErr) {
// 						errFiles = append(errFiles, err.(*errDeleteFiles).files...)
// 					}
// 					continue
// 				}

// 				prevDeletedFiles = append(prevDeletedFiles, deletedFiles...)
// 			}

// 			if len(errFiles) != 0 {
// 				retErr = &errDeleteFiles{files: errFiles}
// 			}
// 			if len(entry.containsFiles) != 0 {
// 				fmt.Println("Not all files within this directory were deleted successfully. Will move to next entry:", entry.Name())
// 				return prevDeletedFiles, retErr
// 			}
// 		}

// 		entry.containsFiles = nil // Nil this out so that the GC can clean it up and reclaim memory

// 		// Case: If the entry is a directory and empty, delete it if it is in the deleteMap. This case also covers if we had a non-empty directory
// 		// that we deleted all the files from
// 		if _, ok := deleteMap[entry.Name()]; !ok {
// 			fmt.Println("Directory is not in deleteMap, so we will not delete it:", entry.Name())
// 			return prevDeletedFiles, nil
// 		}

// 		fmt.Println("Deleting empty directory:", entry.Name())
// 		err := client.removeFile(ctx, PNFSToHTTPS(entry.Name(),  stripPNFSFromPath), true)
// 		if err != nil {
// 			// TODO Handle error
// 			fmt.Println("error deleting empty directory:", err)
// 			errFiles = append(errFiles, entry.Name())
// 			return prevDeletedFiles, &errDeleteFiles{files: errFiles}
// 		}
// 		fmt.Println("Deleted empty directory:", entry.Name())
// 		// Remove the directory from parent's containsFiles slice
// 		// TODO maybe make this a function or method
// 		if entry.parent != nil {
// 			entry.parent.containsFiles = slices.DeleteFunc(
// 				entry.parent.containsFiles,
// 				func(f *FileEntry) bool {
// 					return f.Name() == entry.Name()
// 				},
// 			)
// 		}
// 		if len(errFiles) != 0 {
// 			return prevDeletedFiles, &errDeleteFiles{files: errFiles}
// 		}
// 		return prevDeletedFiles, nil
// 	}

// 	// Base case - if the entry is a file, delete it

// 	// Don't delete the file if it's not in the deleteMap
// 	if _, ok := deleteMap[entry.Name()]; !ok {
// 		fmt.Println("Directory is not in deleteMap, so we will not delete it:", entry.Name())
// 		return prevDeletedFiles, nil
// 	}

// 	fmt.Println("Deleting file:", entry.Name())
// 	err := client.removeFile(ctx, PNFSToHTTPS(entry.Name(), stripPNFSFromPath), false)
// 	if err != nil {
// 		// TODO Handle error
// 		fmt.Println("error deleting file:", err)
// 		return nil, &errDeleteFiles{files: []string{entry.Name()}}
// 	}
// 	// File was deleted successfully.  Remove it from parent's containsFiles slice
// 	if entry.parent != nil {
// 		slices.DeleteFunc(
// 			entry.parent.containsFiles,
// 			func(f *FileEntry) bool {
// 				return f.Name() == entry.Name()
// 			},
// 		)
// 	}

// 	prevDeletedFiles = append(prevDeletedFiles, entry.Name())
// 	return prevDeletedFiles, nil
// }

type errDeleteFiles struct {
	files []string
}

func (e *errDeleteFiles) Error() string {
	return fmt.Sprintf("Could not delete files: %v", e.files)
}

func userConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		slog.Error("error getting user config directory", "error", err)
		return ""
	}
	return dir
}

func getConfigFilePath(configFileFlagVal string, checkDirs []string) (string, error) {
	// Check configFileFlag first
	if configFileFlagVal != "" {
		_, err := os.Stat(configFileFlagVal)
		if err == nil {
			// Config file exists, return it
			return configFileFlagVal, nil
		}
	}

	// Now check all of our checkDirs for the file
	for _, dir := range checkDirs {
		configFilePath := filepath.Join(dir, defaultConfigFileName)
		_, err := os.Stat(configFilePath)
		if err == nil {
			// Config file exists in this directory, return it
			slog.Info("Found config file", "configFilePath", configFilePath)
			return configFilePath, nil
		}
	}
	return "", errNoConfigFileFound
}

type stringGetter interface {
	String(string) string
}

func getTimeoutFromConfig(s stringGetter) (time.Duration, error) {
	timeoutStr := s.String("timeout")
	if timeoutStr == "" {
		return time.Duration(0), errNoTimeoutConfigured
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return time.Duration(0), fmt.Errorf("error parsing timeout duration: %w", err)
	}
	return timeout, nil
}

var (
	errNoConfigFileFound   = errors.New("no config file found in any of the specified directories")
	errNoTimeoutConfigured = errors.New("no timeout configured in the configuration")
)
