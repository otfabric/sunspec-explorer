// SPDX-License-Identifier: MIT

//go:build unix

package engine

import "syscall"

// freeBytes returns the space an unprivileged user can still write under
// dir. ok is false when the file system does not say.
func freeBytes(dir string) (free int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	// The field types differ between systems; the values fit in int64.
	return int64(st.Bavail) * int64(st.Bsize), true
}
