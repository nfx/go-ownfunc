// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

// Command go-ownfunc runs the ownfunc analyzer standalone, outside golangci-lint,
// for manual testing against real packages (e.g. go run ./cmd/go-ownfunc ./...).
package main

import (
	"github.com/nfx/go-ownfunc"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	cfg := ownfunc.Ownfunc{}
	singlechecker.Main(cfg.Analyzer())
}
