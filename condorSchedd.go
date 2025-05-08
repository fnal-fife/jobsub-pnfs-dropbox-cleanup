package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	condor "github.com/retzkek/htcondor-go"
	classad "github.com/retzkek/htcondor-go/classad"
)

func getCondorSchedds(ctx context.Context, constraint string) ([]*CondorSchedd, error) {
	cmd := condor.NewCommand("/usr/bin/condor_status").WithPool("gpcollector04.fnal.gov").WithConstraint(constraint).WithArg("-schedd")
	fmt.Println("Running command:", cmd.MakeArgs())
	ads, err := cmd.RunWithContext(ctx)
	if err != nil {
		// TODO Handle Error
		return nil, err
	}
	schedds := make([]*CondorSchedd, 0, len(ads))
	for _, ad := range ads {
		name, ok := ad["Name"]
		if !ok {
			// TODO Handle error
			continue
		}
		schedd := &CondorSchedd{
			name: name.String(),
		}
		schedds = append(schedds, schedd)
	}
	return schedds, nil
}

type CondorSchedd struct {
	name string
}

func (c *CondorSchedd) getPNFSJobsForExperiment(ctx context.Context, experiment string) ([]classad.ClassAd, error) {
	constraint := "Jobsub_Group==\"" + experiment + "\"" + " && !IsUndefined(PNFS_INPUT_FILES)"

	cmd := condor.NewCommand("/usr/bin/condor_q").WithName(c.name).WithConstraint(constraint)
	fmt.Println("Running command:", cmd.MakeArgs())
	ads, err := cmd.RunWithContext(ctx)
	if err != nil {
		// Handle Error
		return nil, err
	}
	return ads, nil
}

func (c *CondorSchedd) getDropboxFilesFromJob(jobAd classad.ClassAd) ([]string, error) {
	attribute := "PNFS_INPUT_FILES"
	val, ok := jobAd[attribute]
	if !ok {
		return nil, ErrMissingJobDropboxFiles
	}

	// b := new(strings.Builder)
	// _, err := io.Copy(b, val)
	// if err != nil {
	// 	return nil, err
	// }
	rawSlice := strings.Split((val.String()), ",")
	finalSlice := make([]string, 0, len(rawSlice))

	for _, elt := range rawSlice {
		finalSlice = append(finalSlice, strings.TrimSpace(elt))
	}
	return finalSlice, nil

}

var ErrMissingJobDropboxFiles = errors.New("required job attribute is missing to get job dropbox files")
