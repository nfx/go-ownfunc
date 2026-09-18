// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc

import "testing"

func TestApplyDefaults(t *testing.T) {
	t.Parallel()

	var cfg Ownfunc
	cfg.applyDefaults()

	if cfg.MinCalls != 1 {
		t.Fatalf("MinCalls default = %d, want 1", cfg.MinCalls)
	}
	if cfg.IgnoreTestFiles == nil || !*cfg.IgnoreTestFiles {
		t.Fatal("IgnoreTestFiles default must be true")
	}
	if len(cfg.IgnoredFunctions) != 1 || cfg.IgnoredFunctions[0] != "^init$" {
		t.Fatalf("IgnoredFunctions default = %#v, want [^init$]", cfg.IgnoredFunctions)
	}
}

func TestApplyDefaultsPreservesExplicitFalse(t *testing.T) {
	t.Parallel()

	f := false
	cfg := Ownfunc{
		MinCalls:         3,
		IgnoreTestFiles:  &f,
		IgnoredFunctions: []string{"^setup$"},
	}
	cfg.applyDefaults()

	if cfg.MinCalls != 3 {
		t.Fatalf("MinCalls = %d, want 3", cfg.MinCalls)
	}
	if cfg.IgnoreTestFiles == nil || *cfg.IgnoreTestFiles {
		t.Fatal("explicit IgnoreTestFiles=false was overwritten")
	}
	if len(cfg.IgnoredFunctions) != 1 || cfg.IgnoredFunctions[0] != "^setup$" {
		t.Fatalf("IgnoredFunctions = %#v, want [^setup$]", cfg.IgnoredFunctions)
	}
}

func TestNewPluginRejectsBadSettings(t *testing.T) {
	t.Parallel()

	p, err := New("not-a-map")
	if err == nil {
		t.Fatal("expected error for invalid settings")
	}
	if p != nil {
		t.Fatal("plugin must be nil on error")
	}
}

func TestNewPluginNilAndEmptySettings(t *testing.T) {
	t.Parallel()

	for _, conf := range []any{nil, map[string]any{}} {
		p, err := New(conf)
		if err != nil {
			t.Fatalf("New(%v) error: %v", conf, err)
		}
		if p == nil {
			t.Fatalf("New(%v) returned nil plugin", conf)
		}
	}
}

func TestNewPluginDefaults(t *testing.T) {
	t.Parallel()
	p, err := New(map[string]any{
		"min-calls": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin, ok := p.(*plugin)
	if !ok {
		t.Fatalf("unexpected plugin type %T", p)
	}
	if plugin.config.MinCalls != 2 {
		t.Fatalf("MinCalls = %d, want 2", plugin.config.MinCalls)
	}
	if plugin.GetLoadMode() != "typesinfo" {
		t.Fatalf("GetLoadMode = %q, want typesinfo", plugin.GetLoadMode())
	}
	analyzers, err := plugin.BuildAnalyzers()
	if err != nil {
		t.Fatal(err)
	}
	if len(analyzers) != 1 || analyzers[0].Name != "ownfunc" {
		t.Fatalf("unexpected analyzers: %#v", analyzers)
	}
}
