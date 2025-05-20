package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO have an HtgettokenClient type that implements getTokener

// func TestNewHtgettokenClient(t *testing.T) {
// 	assert.Equal(t, NewHtgettokenClient(), &HtgettokenClient{})
// }

func TestPrepareHtgettokenopts(t *testing.T) {
	type testCase struct {
		description string
		options     []string
		expected    string
	}

	testCases := []testCase{
		{
			"Option with value",
			[]string{"--option=value"},
			"--option=value",
		},
		{
			"Option without value",
			[]string{"--option"},
			"--option",
		},
		{
			"Single-dashed option without value",
			[]string{"-o"},
			"-o",
		},
		{
			"Option with space and value",
			[]string{"--option", "value"},
			"--option=value",
		},
		{
			"Single-dashed option with value",
			[]string{"-o", "value"},
			"-o=value",
		},
		// These are malformed, but we should handle them
		{
			"Option with space and value",
			[]string{"--option value"},
			"--option=value",
		},
		{
			"Single-dashed option with value",
			[]string{"-o value"},
			"-o=value",
		},
		{
			"Extra spaces",
			[]string{"--option  value ", "--option2=value2"},
			"--option=value --option2=value2",
		},
		{
			"Mix of options with and without values, with malformed options mixed in",
			[]string{"--option=value", "--option2=value2", "--option3", "value3", "--option4  value4 "},
			"--option=value --option2=value2 --option3=value3 --option4=value4",
		},
	}

	for _, test := range testCases {
		t.Run(
			test.description,
			func(t *testing.T) {
				result := prepareHtgettokenopts(test.options)
				assert.Equal(t, test.expected, result)
			},
		)
	}
}
