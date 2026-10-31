// SPDX-License-Identifier: MIT

package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestGoSunspecVersion(t *testing.T) {
	// A test binary does not link go-sunspec here, and before Go 1.26 it
	// carries no dependency information at all: the answer is "unknown". The
	// built programs are checked in CI ("sunspec-explorerd version").
	if v := GoSunspecVersion(); v == "" {
		t.Errorf("GoSunspecVersion = %q", v)
	}
	if Version == "" {
		t.Error("Version is empty")
	}
}

func TestModuleVersion(t *testing.T) {
	info := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "example.com/other", Version: "v9.9.9"},
		{Path: goSunspecPath, Version: "v0.8.0"},
		{Path: "example.com/replaced", Version: "v1.0.0", Replace: &debug.Module{Version: "v1.0.1"}},
	}}
	for path, want := range map[string]string{
		goSunspecPath:          "v0.8.0",
		"example.com/replaced": "v1.0.1",
		"example.com/absent":   "unknown",
	} {
		if got := moduleVersion(info, path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}
