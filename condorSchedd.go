package main

import (
	"errors"
	"io"
	"strings"
)

type CondorSchedd struct {
}

func (c *CondorSchedd) getDropboxFilesFromJob(j map[string]io.Reader) ([]string, error) {
	attribute := "PNFS_INPUT_FILES"
	val, ok := j[attribute]
	if !ok {
		return nil, ErrMissingJobDropboxFiles
	}

	b := new(strings.Builder)
	_, err := io.Copy(b, val)
	if err != nil {
		return nil, err
	}
	rawSlice := strings.Split((b.String()), ",")
	finalSlice := make([]string, 0, len(rawSlice))

	for _, elt := range rawSlice {
		finalSlice = append(finalSlice, strings.TrimSpace(elt))
	}
	return finalSlice, nil

}

var ErrMissingJobDropboxFiles = errors.New("required job attribute is missing to get job dropbox files")
