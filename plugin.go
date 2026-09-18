// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc

import (
	"strings"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("ownfunc", New)
}

// New is the module-plugin factory. It parses raw configuration from
// golangci-lint via register.DecodeSettings.
func New(conf any) (register.LinterPlugin, error) {
	mconf, ok := conf.(map[string]any)
	if ok {
		conf = normalizeKeys(mconf)
	}
	cfg, err := register.DecodeSettings[Ownfunc](conf)
	if err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &plugin{config: cfg}, nil
}

// normalizeKeys rewrites hyphens to underscores in top-level map keys so
// kebab-case YAML settings (golangci-lint's convention) match the
// underscore-only json tags on Config.
func normalizeKeys(mconf map[string]any) map[string]any {
	normalized := make(map[string]any, len(mconf))
	for k, v := range mconf {
		normalized[strings.ReplaceAll(k, "-", "_")] = v
	}
	return normalized
}

type plugin struct {
	config Ownfunc
}

var _ register.LinterPlugin = (*plugin)(nil)

func (p *plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{p.config.Analyzer()}, nil
}

func (p *plugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
