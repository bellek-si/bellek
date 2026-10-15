package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildVersionNamesAGoInstallBuild(t *testing.T) {
	module := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: v}}, true }
	}
	checkout := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: v}, Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}}}, true
		}
	}
	tests := []struct {
		name, stamped string
		read          func() (*debug.BuildInfo, bool)
		want          string
	}{
		{"the Makefile stamp wins", "v0.1.0", module("v0.2.0"), "v0.1.0"},
		{"go install of a tag", "dev", module("v0.1.0"), "v0.1.0"},
		{"go install of a commit", "dev", module("v0.1.1-0.20261015184400-9d3e2d4c1a2b"), "v0.1.1-0.20261015184400-9d3e2d4c1a2b"},
		{"a build of a checkout with no tag", "dev", checkout("v0.0.0-20261015184432-9d3e2d4a1519+dirty"), "dev"},
		{"a build with no module version", "dev", module("(devel)"), "dev"},
		{"a binary with no build info", "dev", func() (*debug.BuildInfo, bool) { return nil, false }, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, buildVersion(tt.stamped, tt.read))
		})
	}
}
