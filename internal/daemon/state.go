// SPDX-License-Identifier: MIT

package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// StateFileName is the name of the state file in the data directory.
const StateFileName = "daemon.json"

// State is what a running daemon writes down for the url command: the
// address it actually bound, which --listen does not tell when the port was
// 0. It holds no secret.
type State struct {
	Listen    string    `json:"listen"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// WriteState writes the state file in dir, mode 0600, atomically.
func WriteState(dir string, st State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".daemon-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, StateFileName))
}

// ReadState reads the state file in dir. A missing file means that no daemon
// is running with this data directory.
func ReadState(dir string) (State, error) {
	var st State
	b, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return st, fmt.Errorf("no daemon is running with data directory %s (start sunspec-explorerd, or pass its --data-dir)", dir)
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil || st.Listen == "" {
		return st, fmt.Errorf("state file in %s is not valid", dir)
	}
	return st, nil
}

// RemoveState removes the state file in dir.
func RemoveState(dir string) { _ = os.Remove(filepath.Join(dir, StateFileName)) }

// Access describes how to reach a daemon.
type Access struct {
	// URL is the address of the web UI with the token in the fragment, ready
	// to paste into a browser.
	URL string
	// SSH is the command that forwards the daemon's port from a workstation,
	// or "" when the daemon is not bound to loopback or the host name is not
	// known.
	SSH string
}

// AccessFor builds the URL and the SSH command for a daemon bound to listen
// (host:port) on the machine called hostname.
//
// For a loopback daemon the URL says 127.0.0.1: that is what the browser on
// the workstation opens, through the forward. A daemon bound to every
// address (0.0.0.0 or ::, as in a container whose port is published) has no
// address of its own to name, and gets 127.0.0.1 too: the URL then works on
// the machine itself, or where the port is published or forwarded to. The
// token goes in the fragment, which a browser never sends to a server.
func AccessFor(listen, token, hostname string) (Access, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return Access{}, fmt.Errorf("listen address %q: %w", listen, err)
	}
	shown := host
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		shown = "127.0.0.1"
	}
	a := Access{URL: fmt.Sprintf("http://%s/#token=%s", net.JoinHostPort(shown, port), token)}
	if isLoopbackHost(host) && hostname != "" {
		a.SSH = fmt.Sprintf("ssh -N -L %s:127.0.0.1:%s <user>@%s", port, port, hostname)
	}
	return a, nil
}
