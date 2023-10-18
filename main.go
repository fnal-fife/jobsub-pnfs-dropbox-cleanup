package main

import (
	"time"
)

var now = time.Now()

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
