#!/bin/sh

fname="$(mktemp)"

if [ -z ${DCACHE_HOST+x} ]
then
	echo "Please set and export the environment variable DCACHE_HOST before running this script."
	exit 1
fi

export BEARER_TOKEN=`cat $BEARER_TOKEN_FILE` 

echo "Setup"
/usr/bin/gfal-rm https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/file_x
/usr/bin/gfal-rm -r https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/filedir_x


echo "Making dirs"
/usr/bin/gfal-mkdir -p https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir1
/usr/bin/gfal-mkdir -p https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir2
/usr/bin/gfal-mkdir -p https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir3
/usr/bin/gfal-mkdir -p https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/filedir_x

echo "putting files in place"
# dir1 has file1a and file1b
echo "file1a" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir1/file1a
echo "file1b" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir1/file1b

# dir2 has file2
echo "file2" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir2/file2

# dir3 is empty

#filedir_x has files, which we expect will not be deleted
echo "file_x" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/filedir_x/x1
echo "file_x" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/filedir_x/x2

# file4 is at top level
echo "file4" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/file4
# file5 is at top level
echo "file4" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/file5
# file6 is at top level
echo "file4" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/file6
# file_x is at top level
echo "file_x" > $fname
/usr/bin/gfal-copy $fname https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/file_x

echo "check dirs"
# Show all dirs
set -x
/usr/bin/gfal-ls -l https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/
/usr/bin/gfal-ls -l https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir1
/usr/bin/gfal-ls -l https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir2
/usr/bin/gfal-ls -l https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/dir3
/usr/bin/gfal-ls -l https://${DCACHE_HOST}:2880/mu2e/scratch/users/${USER}/fake_resilient/jobsub_stage/filedir_x

rm $fname
