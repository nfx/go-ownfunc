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

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.Run(t, testdata, analyzer, "a")
}

func TestAllowFunctionValues(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{
		AllowFunctionValues: true,
	}

	analyzer := cfg.Analyzer()
	analysistest.Run(t, testdata, analyzer, "funcval")
}

func TestMinCalls(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{
		MinCalls: 2,
	}

	analyzer := cfg.Analyzer()
	analysistest.Run(t, testdata, analyzer, "mincalls")
}

func TestIgnoredReceiverTypes(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{
		IgnoredReceiverTypes: []string{"BaseModel"},
	}

	analyzer := cfg.Analyzer()
	analysistest.Run(t, testdata, analyzer, "recvignore")
}

func TestSuggestedFixes(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fix")
}

func TestPluginSuggestedFixesTestCalls(t *testing.T) {
	testdata := analysistest.TestData()
	p, err := ownfunc.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	analyzers, err := p.BuildAnalyzers()
	if err != nil {
		t.Fatal(err)
	}
	analysistest.RunWithSuggestedFixes(t, testdata, analyzers[0], "fixgolangci")
}

func TestSuggestedFixesValueReceiver(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixvalue")
}

func TestSuggestedFixesPointerReceiver(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpointer")
}

func TestSuggestedFixesMixedReceiver(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixmixed")
}

func TestSuggestedFixesRecursive(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixrecursive")
}

func TestSuggestedFixesTestCall(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixtestcall")
}

func TestSuggestedFixesPromoted(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpromoted")
}

func TestSuggestedFixesNameCollision(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixnamecollision")
}

func TestSuggestedFixesNilable(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixnilable")
}

func TestSuggestedFixesPromotedRename(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpromotedrename")
}

func TestSuggestedFixesPromotedUnderlying(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixpromotedunderlying")
}

func TestSuggestedFixesNonStructOwner(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixnonstruct")
}

func TestSuggestedFixesGeneric(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "generic")
}

func TestGenericOwnerUnfixable(t *testing.T) {
	testdata := analysistest.TestData()

	cfg := ownfunc.Ownfunc{}

	analyzer := cfg.Analyzer()
	analysistest.Run(t, testdata, analyzer, "genericunfixable")
}

func TestBlankCallerReceiverCollisionUnfixable(t *testing.T) {
	testdata := analysistest.TestData()
	cfg := ownfunc.Ownfunc{}
	analyzer := cfg.Analyzer()
	// RunWithSuggestedFixes (not Run) so a stray SuggestedFix here — no
	// .golden file exists for this package — fails the test loudly instead
	// of passing silently.
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "blankcallercollision")
}

func TestSuggestedFixesBlankCallerReceiverRename(t *testing.T) {
	testdata := analysistest.TestData()
	cfg := ownfunc.Ownfunc{}
	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixblankcallerrename")
}

func TestSuggestedFixesBlankCallerReceiverRenameCounter(t *testing.T) {
	testdata := analysistest.TestData()
	cfg := ownfunc.Ownfunc{}
	analyzer := cfg.Analyzer()
	analysistest.RunWithSuggestedFixes(t, testdata, analyzer, "fixblankcallercollisioncounter")
}
