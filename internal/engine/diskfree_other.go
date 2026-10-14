// SPDX-License-Identifier: MIT

//go:build !unix && !windows

package engine

// freeBytes is not known on this system: the free-space floor does not apply.
func freeBytes(string) (free int64, ok bool) { return 0, false }
