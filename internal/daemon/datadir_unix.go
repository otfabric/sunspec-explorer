// SPDX-License-Identifier: MIT

//go:build unix

package daemon

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func userID() string { return strconv.Itoa(os.Geteuid()) }

func checkOwnerAndMode(dir string, info os.FileInfo) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("data directory %s belongs to another user (uid %d): refusing to use it", dir, st.Uid)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("data directory %s has mode %04o: it must not be accessible to others (chmod 700)", dir, perm)
	}
	return nil
}

// checkPrivateFile refuses a secret file that others can read.
func checkPrivateFile(path string, info os.FileInfo) error {
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s has mode %04o: it must be readable by its owner only (chmod 600)", path, perm)
	}
	return nil
}
