// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"net"
	"strings"
)

// DefaultListen is the default listen address: loopback only.
const DefaultListen = "127.0.0.1:8765"

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckListen validates a listen address. An address other than loopback is
// accepted only when allowRemote is set, and then with a warning to print:
// the bearer token and the Host check still apply, but the traffic,
// including the token, is not encrypted.
func CheckListen(listen string, allowRemote bool) (warning string, err error) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("listen address %q: %w", listen, err)
	}
	if isLoopbackHost(host) {
		return "", nil
	}
	if !allowRemote {
		return "", fmt.Errorf("listen address %q is not loopback: the daemon is meant to be reached through an SSH port forward; pass --allow-remote-bind to bind it anyway", listen)
	}
	return fmt.Sprintf("listening on %s, which is not loopback: the API is reachable from the network and is not encrypted. "+
		"The bearer token and the Host check still apply, but anyone on the path can read the token. Prefer 127.0.0.1 and an SSH port forward", listen), nil
}

// Hosts returns the host names a daemon bound to listen answers to (see
// api.Options.Hosts), plus extra.
//
// A loopback daemon answers to the loopback names only, which is what a
// browser sends through an SSH port forward. A daemon bound to a specific
// address answers to that address too. A daemon bound to every address
// (0.0.0.0, ::) cannot know its names: they must be given.
func Hosts(listen string, extra []string) []string {
	hosts := []string{"127.0.0.1", "localhost", "::1"}
	if host, _, err := net.SplitHostPort(listen); err == nil {
		if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) && !isLoopbackHost(host) {
			hosts = append(hosts, host)
		}
	}
	return append(hosts, extra...)
}
