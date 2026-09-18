// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc

// Ownfunc holds settings supplied under settings.custom.ownfunc in .golangci.yml.
//
// register.DecodeSettings uses JSON unmarshaling, so fields use json tags.
// New normalizes hyphens to underscores in the raw settings keys before
// decoding, so both kebab-case and snake_case YAML keys resolve to these
// underscore tags. IgnoreTestFiles is a pointer so an explicit false can be
// distinguished from a missing value (the default is true).
type Ownfunc struct {
	MinCalls             int      `json:"min_calls"`
	IgnoreTestFiles      *bool    `json:"ignore_test_files"`
	AllowFunctionValues  bool     `json:"allow_function_values"`
	IgnoredFunctions     []string `json:"ignored_functions"`
	IgnoredReceiverTypes []string `json:"ignored_receiver_types"`
}

// IgnoreTestFilesEnabled reports whether test files should be skipped.
func (c *Ownfunc) IgnoreTestFilesEnabled() bool {
	return c.IgnoreTestFiles != nil && *c.IgnoreTestFiles
}

// applyDefaults fills zero-value knobs with the documented defaults.
func (c *Ownfunc) applyDefaults() {
	if c.MinCalls <= 0 {
		c.MinCalls = 1
	}
	if c.IgnoreTestFiles == nil {
		t := true
		c.IgnoreTestFiles = &t
	}
	if len(c.IgnoredFunctions) == 0 {
		c.IgnoredFunctions = []string{"^init$"}
	}
}
