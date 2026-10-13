// SPDX-License-Identifier: MIT

// Package frontend embeds the two builds of the user interface: the Wails
// build for the desktop application and the web build the daemon serves.
// Run "make frontend" before building a binary; a fresh checkout holds only
// placeholders, which is enough to compile and test the Go code.
//
// This package must not import Wails: the daemon links it.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist/wails
var wails embed.FS

//go:embed all:dist/web
var web embed.FS

// Wails returns the build for the desktop application, rooted at its
// index.html.
func Wails() fs.FS { return sub(wails, "dist/wails") }

// Web returns the build served by the daemon, rooted at its index.html. It
// contains no Wails runtime.
func Web() fs.FS { return sub(web, "dist/web") }

// Built reports whether assets holds a real build rather than the
// placeholder of a fresh checkout.
func Built(assets fs.FS) bool {
	_, err := fs.Stat(assets, "index.html")
	return err == nil
}

func sub(fsys embed.FS, dir string) fs.FS {
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err) // the directory is embedded above
	}
	return s
}
