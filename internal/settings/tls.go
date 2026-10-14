// SPDX-License-Identifier: MIT

// Package settings holds what the desktop application remembers between
// runs. It must not import Wails: it is plain Go, tested without a window.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/otfabric/sunspec-explorer/internal/engine"
)

// FileName is the name of the settings file in the application's
// configuration directory.
const FileName = "settings.json"

// Kinds of file that make up a TLS identity.
const (
	TLSCert = "cert"
	TLSKey  = "key"
	TLSCA   = "ca"
)

// TLSSettings is the TLS identity of the desktop application as its user
// sees it: the three files and whether the engine uses them.
type TLSSettings struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	CAFile   string `json:"ca_file"`
	// Active is true when the three files are loaded and the engine opens
	// tcp+tls:// targets with them.
	Active bool `json:"active"`
	// Error says why a complete set of files is not active.
	Error string `json:"error"`
	// FromFlags is true when the identity was given on the command line; it
	// then cannot be changed from the user interface.
	FromFlags bool `json:"from_flags"`
}

// file is the settings file. Only paths are stored, never key material.
type file struct {
	TLS struct {
		CertFile string `json:"cert_file,omitempty"`
		KeyFile  string `json:"key_file,omitempty"`
		CAFile   string `json:"ca_file,omitempty"`
	} `json:"tls"`
}

// TLS manages the TLS identity: it remembers the chosen files in the
// settings file and hands the loaded identity to the engine.
type TLS struct {
	path  string
	apply func(*engine.TLSConfig)

	mu    sync.Mutex
	state TLSSettings
}

// DefaultPath returns the settings file of the current user, or "" when the
// system names no configuration directory (nothing is remembered then).
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sunspec-explorer", FileName)
}

// NewTLS reads the settings file at path and applies the identity it names.
// apply receives the identity whenever it changes, nil when there is none. A
// settings file that is missing or unreadable is the same as an empty one.
func NewTLS(path string, apply func(*engine.TLSConfig)) *TLS {
	t := &TLS{path: path, apply: apply}
	var f file
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &f) == nil {
		t.state.CertFile, t.state.KeyFile, t.state.CAFile = f.TLS.CertFile, f.TLS.KeyFile, f.TLS.CAFile
	}
	t.load()
	return t
}

// NewTLSFromFlags is the identity given on the command line: it is in use
// as it is and not stored.
func NewTLSFromFlags(certFile, keyFile, caFile string) *TLS {
	return &TLS{state: TLSSettings{CertFile: certFile, KeyFile: keyFile, CAFile: caFile, Active: true, FromFlags: true}}
}

// Settings returns the current state.
func (t *TLS) Settings() TLSSettings {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Set chooses the file of one kind, tries the identity and remembers the
// choice. The path comes from a native file dialog, never from the page.
func (t *TLS) Set(kind, path string) (TLSSettings, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.FromFlags {
		return t.state, errors.New("the TLS identity was given on the command line and cannot be changed here")
	}
	switch kind {
	case TLSCert:
		t.state.CertFile = path
	case TLSKey:
		t.state.KeyFile = path
	case TLSCA:
		t.state.CAFile = path
	default:
		return t.state, fmt.Errorf("unknown kind of TLS file %q", kind)
	}
	t.load()
	return t.state, t.save()
}

// Clear forgets the identity.
func (t *TLS) Clear() (TLSSettings, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.FromFlags {
		return t.state, errors.New("the TLS identity was given on the command line and cannot be changed here")
	}
	t.state = TLSSettings{}
	t.load()
	return t.state, t.save()
}

// load tries the three files and tells the engine. The caller holds t.mu,
// or is the constructor.
func (t *TLS) load() {
	t.state.Active, t.state.Error = false, ""
	var cfg *engine.TLSConfig
	if t.state.CertFile != "" && t.state.KeyFile != "" && t.state.CAFile != "" {
		loaded, err := engine.LoadTLS(t.state.CertFile, t.state.KeyFile, t.state.CAFile)
		if err != nil {
			t.state.Error = err.Error()
		} else {
			cfg, t.state.Active = loaded, true
		}
	}
	if t.apply != nil {
		t.apply(cfg)
	}
}

// save writes the settings file, readable by its owner only.
func (t *TLS) save() error {
	if t.path == "" {
		return nil
	}
	var f file
	f.TLS.CertFile, f.TLS.KeyFile, f.TLS.CAFile = t.state.CertFile, t.state.KeyFile, t.state.CAFile
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return fmt.Errorf("settings are not remembered: %w", err)
	}
	if err := os.WriteFile(t.path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("settings are not remembered: %w", err)
	}
	return nil
}
