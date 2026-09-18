// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc_test

import (
	"testing"

	"github.com/nfx/go-ownfunc"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestOwnFunc(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.Run(t, testdata, analyzer, "a")
}

func TestAllowFunctionValues(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{
		AllowFunctionValues: true,
	}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.Run(t, testdata, analyzer, "funcval")
}

func TestMinCalls(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{
		MinCalls: 2,
	}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.Run(t, testdata, analyzer, "mincalls")
}

func TestIgnoredReceiverTypes(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{
		IgnoredReceiverTypes: []string{"BaseModel"},
	}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.Run(t, testdata, analyzer, "recvignore")
}

func TestSuggestedFixes(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fix")
}

func TestSuggestedFixesValueReceiver(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixvalue")
}

func TestSuggestedFixesPointerReceiver(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpointer")
}

func TestSuggestedFixesMixedReceiver(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixmixed")
}

func TestSuggestedFixesRecursive(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixrecursive")
}

func TestSuggestedFixesTestCall(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixtestcall")
}

func TestSuggestedFixesPromoted(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpromoted")
}

func TestSuggestedFixesNameCollision(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixnamecollision")
}

func TestSuggestedFixesNilable(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixnilable")
}

func TestSuggestedFixesPromotedRename(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpromotedrename")
}

func TestSuggestedFixesPromotedUnderlying(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpromotedunderlying")
}

func TestSuggestedFixesNonStructOwner(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixnonstruct")
}

func TestSuggestedFixesGeneric(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "generic")
}

func TestGenericOwnerUnfixable(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()

	analyzer := ownfunc.NewAnalyzer(cfg)
	analysistest.Run(t, testdata, analyzer, "genericunfixable")
}

func TestIgnoreTestFilesToggle(t *testing.T) {
	// analysistest.Run loads the library package only (no *_test.go),
	// so filename filtering is asserted through the config knob.
	cfg := ownfunc.Config{}
	cfg.ApplyDefaults()
	if !cfg.IgnoreTestFilesEnabled() {
		t.Fatal("expected ignore-test-files default true")
	}

	cfg.IgnoreTestFiles = new(false)
	if cfg.IgnoreTestFilesEnabled() {
		t.Fatal("explicit false must disable test-file ignoring")
	}
}
