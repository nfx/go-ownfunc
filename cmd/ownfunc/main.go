// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/nfx/go-ownfunc"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	cfg := ownfunc.Ownfunc{}
	singlechecker.Main(cfg.Analyzer())
}
