package main

import (
	"bytes"
	"io"
)

type JobLister interface {
	queryJobsList(attributes []string, constraint []string) (jobs []map[string][]byte, err error)
	getDropboxFilesFromJob(job map[string]io.Reader) (files []string, err error)
}

func GetActiveFiles(j JobLister, attributes []string, constraints []string) ([]string, error) {
	activeFiles := make([]string, 0)
	// Run Query
	// QueryJobsList(attribute string, ...constraints []string) ([]map[string][]byte, error)
	jobs, err := j.queryJobsList(attributes, constraints)
	if err != nil {
		return activeFiles, err
	}
	for _, job := range jobs {
		readerJob := make(map[string]io.Reader)
		for k, v := range job {
			readerJob[k] = bytes.NewReader(v)
		}

		files, err := j.getDropboxFilesFromJob(readerJob)
		if err != nil {
			// log error
			continue
		}

		activeFiles = append(activeFiles, files...)
	}
	return activeFiles, nil
}
