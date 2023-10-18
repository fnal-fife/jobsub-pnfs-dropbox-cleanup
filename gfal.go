package main

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

var lineRegex = regexp.MustCompile(`((?:\w|-)+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\w+\s+\d+\s+(?:(?:\d+:\d+)|\d+))\s+(.+)`)

var (
	dateWithTimeNoYearLayout string = "Jan  2 15:04"
	dateWithYearLayout       string = "Jan 2 2006"
)

func scanDropboxLineToFileEntry(line string) (*FileEntry, error) {
	var err error
	lineParts := lineRegex.FindStringSubmatch(line)
	if lineParts == nil {
		return nil, ErrParseLine
	}

	f := &FileEntry{filename: lineParts[7]}
	perms := lineParts[1]
	dateString := lineParts[6]

	f.isDirectory, err = parsePermsToDirectoryFlag(perms)
	if err != nil {
		return nil, ErrParseLine
	}

	f.created, err = parseDateStampToTime(dateString)
	if err != nil {
		return nil, ErrParseLine
	}

	return f, nil
}

func parsePermsToDirectoryFlag(perms string) (bool, error) {
	if len(perms) != 10 {
		return false, ErrMalformedPerms
	}

	validPrefixes := []string{"d", "-"}
	if !slices.Contains(validPrefixes, string(perms[0])) {
		return false, ErrMalformedPerms
	}

	if strings.HasPrefix(perms, "d") {
		return true, nil
	}
	return false, nil
}

func parseDateStampToTime(dateString string) (time.Time, error) {
	var rawDateStamp time.Time
	var err error
	// See if our dateString matches the "Jan  2 15:04 format"
	rawDateStamp, err = time.ParseInLocation(dateWithTimeNoYearLayout, dateString, time.Local)
	if err == nil {
		// We succeeded at parsing this time, so the year will be 0000.  Add the current year on.
		yearDateStamp := rawDateStamp.AddDate(now.Year(), 0, 0)
		if yearDateStamp.After(now) {
			// We're in the future, so subtract a year
			return yearDateStamp.AddDate(-1, 0, 0), nil
		}
		return yearDateStamp, nil
	}
	// The previous parsing attempt failed, so we must be in the "Jan 2 2006" format
	rawDateStamp, err = time.ParseInLocation(dateWithYearLayout, dateString, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	return rawDateStamp, nil
}

// TODO rename this
var (
	ErrParseLine      = errors.New("could not parse line")
	ErrMalformedPerms = errors.New("perms string is malformed")
)
