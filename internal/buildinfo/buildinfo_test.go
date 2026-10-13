// SPDX-License-Identifier: MIT

package buildinfo

import "testing"

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
