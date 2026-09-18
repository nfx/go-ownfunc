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
func (o *Ownfunc) IgnoreTestFilesEnabled() bool {
	return o.IgnoreTestFiles != nil && *o.IgnoreTestFiles
}

// applyDefaults fills zero-value knobs with the documented defaults.
func (o *Ownfunc) applyDefaults() {
	if o.MinCalls <= 0 {
		o.MinCalls = 1
	}
	if o.IgnoreTestFiles == nil {
		t := true
		o.IgnoreTestFiles = &t
	}
	if len(o.IgnoredFunctions) == 0 {
		o.IgnoredFunctions = []string{"^init$"}
	}
}
