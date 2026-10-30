// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
)

// A token file holds a token that is safe in a URL fragment and in a header,
// or it is refused; a refused file never counts as a token.
func FuzzReadToken(f *testing.F) {
	f.Add([]byte(newToken() + "\n"))
	f.Add([]byte("short"))
	f.Add([]byte(strings.Repeat("a", 32) + "#x"))
	f.Add([]byte(strings.Repeat("a", 31) + "\n\n\n"))
	f.Add([]byte(strings.Repeat("a", 32) + "\x00"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), TokenFileName)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		token, err := ReadToken(path)
		if err != nil {
			if token != "" {
				t.Fatalf("refused file gave token %q", token)
			}
			return
		}
		if len(token) < 32 || strings.ContainsAny(token, " \t\r\n#&?/") {
			t.Fatalf("token %q was accepted", token)
		}
		if got, created, err := LoadOrCreateToken(path); err != nil || created || got != token {
			t.Fatalf("LoadOrCreateToken = %q, %v, %v", got, created, err)
		}
	})
}

// The state file of a daemon: valid, with a listen address, or refused.
func FuzzReadState(f *testing.F) {
	f.Add([]byte(`{"listen":"127.0.0.1:8765","pid":1,"started_at":"2026-10-30T00:00:00Z"}`))
	f.Add([]byte(`{"listen":""}`))
	f.Add([]byte(`{"listen":1}`))
	f.Add([]byte(`{"listen":"x","started_at":"never"}`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, StateFileName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := ReadState(dir)
		if err != nil {
			return
		}
		if st.Listen == "" {
			t.Fatal("a state without a listen address was accepted")
		}
		// What the url command does with it.
		_, _ = AccessFor(st.Listen, "token", "host")
	})
}

// A listen address, from a flag or from the state file. Without
// --allow-remote-bind only loopback passes; a daemon always answers to the
// loopback names; the URL of a daemon carries the token in the fragment only,
// so that a browser never sends it; and the SSH hint comes for loopback only.
func FuzzListenAddress(f *testing.F) {
	for _, s := range []string{
		"127.0.0.1:8765", "localhost:1", "[::1]:8765", "0.0.0.0:8765", "[::]:0", "192.168.1.2:8765", ":8765",
		"127.1:80", "LOCALHOST:1", "[fe80::1%eth0]:1", "host", "", "a:b:c", "127.0.0.1.nip.io:80",
	} {
		f.Add(s, false)
		f.Add(s, true)
	}

	f.Fuzz(func(t *testing.T, listen string, allowRemote bool) {
		warning, err := CheckListen(listen, allowRemote)
		host, port, splitErr := net.SplitHostPort(listen)
		if (err != nil) != (splitErr != nil || (!isLoopbackHost(host) && !allowRemote)) {
			t.Fatalf("CheckListen(%q, %v) = %v", listen, allowRemote, err)
		}
		if err == nil && (warning != "") != !isLoopbackHost(host) {
			t.Fatalf("CheckListen(%q) warning %q", listen, warning)
		}
		hosts := Hosts(listen, nil)
		if len(hosts) < 3 || hosts[0] != "127.0.0.1" {
			t.Fatalf("Hosts(%q) = %v", listen, hosts)
		}
		const token = "secret-token"
		a, err := AccessFor(listen, token, "gateway")
		if err != nil {
			return
		}
		// A daemon states the address it bound: an IP address and a numeric
		// port. Only the flag can hold other text, and that fails when binding.
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || strings.ContainsFunc(addr.Zone(), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) || r > unicode.MaxASCII
		}) {
			return
		}
		u, err := url.Parse(a.URL)
		if err != nil {
			t.Fatalf("URL %q: %v", a.URL, err)
		}
		if u.Fragment != "token="+token || strings.Contains(u.Host+u.Path+u.RawQuery, token) {
			t.Fatalf("the token is not in the fragment alone: %q", a.URL)
		}
		if a.SSH != "" && (!isLoopbackHost(host) || strings.Contains(a.SSH, token)) {
			t.Fatalf("SSH hint %q for %q", a.SSH, listen)
		}
	})
}

// --allow-target: a list either parses or is refused, and what it then allows
// is what its prefixes contain, with an IPv4-mapped address treated as the
// IPv4 address and a zone ignored. This is what stops a token holder from
// probing other networks through the gateway.
func FuzzTargetAllowList(f *testing.F) {
	f.Add("10.0.0.0/8", "10.1.2.3")
	f.Add("192.168.1.5", "192.168.1.5")
	f.Add("192.168.1.5", "192.168.1.6")
	f.Add("10.0.0.0/8", "[::ffff:10.0.0.1]")
	f.Add("10.0.0.0/8", "[::ffff:127.0.0.1]")
	f.Add("fe80::/10", "[fe80::1%25eth0]")
	f.Add("::ffff:10.0.0.0/104", "10.0.0.1")
	f.Add("0.0.0.0/0", "[::1]")
	f.Add("x", "y")

	f.Fuzz(func(t *testing.T, cidr, host string) {
		l, err := NewTargetAllowList([]string{cidr})
		if err != nil {
			return
		}
		// Names resolve to nothing here: the test uses no network.
		l.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, nil }
		done := make(chan struct{})
		go func() {
			defer close(done)
			u, err := url.Parse("tcp://" + host)
			if err != nil {
				return
			}
			err = l.Allow(u)
			addr, perr := netip.ParseAddr(u.Hostname())
			if perr != nil {
				if err == nil {
					t.Errorf("host %q that does not resolve was allowed", u.Hostname())
				}
				return
			}
			want := addr.Unmap().WithZone("")
			contained := false
			for _, p := range l.prefixes {
				contained = contained || p.Contains(want)
			}
			if (err == nil) != contained {
				t.Errorf("Allow(%s) with %q = %v, contained %v", want, cidr, err, contained)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Allow did not return")
		}
	})
}

// --allow-serial: patterns parse or are refused, and only a match passes.
func FuzzSerialAllowList(f *testing.F) {
	f.Add("/dev/ttyUSB*", "/dev/ttyUSB0")
	f.Add("/dev/ttyUSB*", "/dev/ttyS0")
	f.Add("[", "x")
	f.Add("/dev/tty[", "/dev/tty")
	f.Add("COM?", "COM3")
	f.Fuzz(func(t *testing.T, pattern, device string) {
		l, err := NewSerialAllowList([]string{pattern})
		if err != nil {
			return
		}
		ok, merr := filepath.Match(pattern, device)
		if got := l.Allow(device) == nil; got != (ok && merr == nil) {
			t.Fatalf("Allow(%q) with %q = %v", device, pattern, got)
		}
	})
}
