// SPDX-License-Identifier: MIT

//go:build !unix

package daemon

import (
	"os"
	"os/user"
	"strings"
)

// userID names the current user in a form that is safe in a file name.
func userID() string {
	u, err := user.Current()
	if err != nil {
		return "user"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, u.Uid)
}

// On Windows, where the daemon runs for development only, access is governed
// by ACLs that the file mode does not show: the per-user temporary directory
// is private already.
func checkOwnerAndMode(string, os.FileInfo) error { return nil }

func checkPrivateFile(string, os.FileInfo) error { return nil }
