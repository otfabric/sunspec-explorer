// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func unixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
}

// A16: the data directory is created private and nothing else is accepted.
func TestEnsureDataDir(t *testing.T) {
	unixOnly(t)
	t.Setenv(DataDirEnv, "")
	if def := DefaultDataDir(); filepath.Dir(def) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(def), "sunspec-explorer-") {
		t.Errorf("DefaultDataDir = %s", def)
	}
	t.Setenv(DataDirEnv, "/data")
	if DefaultDataDir() != "/data" {
		t.Errorf("%s ignored: %s", DataDirEnv, DefaultDataDir())
	}
	root := t.TempDir()

	dir := filepath.Join(root, "data")
	if err := EnsureDataDir(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	if mode(t, dir) != 0o700 {
		t.Errorf("mode = %04o, want 0700", mode(t, dir))
	}
	if err := EnsureDataDir(dir); err != nil {
		t.Errorf("existing directory: %v", err)
	}

	for _, perm := range []os.FileMode{0o755, 0o750, 0o701, 0o770} {
		wide := filepath.Join(root, "wide")
		if err := os.Mkdir(wide, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(wide, perm); err != nil {
			t.Fatal(err)
		}
		if err := EnsureDataDir(wide); err == nil || !strings.Contains(err.Error(), "accessible to others") {
			t.Errorf("mode %04o accepted: %v", perm, err)
		}
		_ = os.Remove(wide)
	}

	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDataDir(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("symlink accepted: %v", err)
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDataDir(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file accepted: %v", err)
	}
	if err := EnsureDataDir(filepath.Join(root, "missing", "deep")); err == nil {
		t.Error("a missing parent was created")
	}

	// A directory of another user: /tmp itself, when this is not root.
	if os.Geteuid() != 0 {
		if st, err := os.Stat("/"); err == nil && st.Mode().Perm()&0o077 == 0 {
			t.Skip("unusual root directory")
		}
		if err := EnsureDataDir("/"); err == nil {
			t.Error("/ accepted as data directory")
		}
	}
}

func TestTokenLifecycle(t *testing.T) {
	unixOnly(t)
	path := filepath.Join(t.TempDir(), TokenFileName)

	// First start: generated, stored 0600.
	first, created, err := LoadOrCreateToken(path)
	if err != nil || !created || len(first) < 40 || strings.ContainsAny(first, "+/=#&? ") {
		t.Fatalf("first start = %q, %v, %v", first, created, err)
	}
	if mode(t, path) != 0o600 {
		t.Errorf("token file mode = %04o, want 0600", mode(t, path))
	}
	// Later starts reuse it.
	again, created, err := LoadOrCreateToken(path)
	if err != nil || created || again != first {
		t.Errorf("second start = %q, %v, %v", again, created, err)
	}

	// Rotation replaces it, still 0600, and leaves nothing behind.
	second, err := RotateToken(path)
	if err != nil || second == first || mode(t, path) != 0o600 {
		t.Fatalf("rotate = %q, %v", second, err)
	}
	if got, _ := ReadToken(path); got != second {
		t.Errorf("file holds %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("rotation left files behind: %v", entries)
	}

	// A token others can read, or that is none, is refused, not replaced.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateToken(path); err == nil || !strings.Contains(err.Error(), "owner only") {
		t.Errorf("world-readable token accepted: %v", err)
	}
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0o600)
	if _, _, err := LoadOrCreateToken(path); err == nil || strings.Contains(err.Error(), "short") {
		t.Errorf("invalid token: %v (must be refused without echoing it)", err)
	}
	if _, err := ReadToken(filepath.Dir(path)); err == nil {
		t.Error("a directory was read as a token")
	}
	if _, err := RotateToken(filepath.Join(t.TempDir(), "missing", "token")); err == nil {
		t.Error("rotation into a missing directory succeeded")
	}
}

func TestTokenSourceFollowsRotation(t *testing.T) {
	unixOnly(t)
	path := filepath.Join(t.TempDir(), TokenFileName)
	src, created, err := NewTokenSource(path)
	if err != nil || !created {
		t.Fatalf("NewTokenSource: %v", err)
	}
	src.recheck = 0
	first := src.Token()
	if first == "" || src.Token() != first {
		t.Fatal("token not stable")
	}

	second, err := RotateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := src.Token(); got != second || got == first {
		t.Errorf("after rotation the source returns %q, want the new token", got)
	}

	// A token file that is gone or broken keeps the last good token: nobody
	// gets in without one, and its holder is not locked out.
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := src.Token(); got != second {
		t.Errorf("broken file: %q", got)
	}
	_ = os.Remove(path)
	if got := src.Token(); got != second {
		t.Errorf("missing file: %q", got)
	}

	// Within the recheck window the file is not looked at.
	src.recheck = time.Hour
	if _, err := RotateToken(path); err != nil {
		t.Fatal(err)
	}
	if got := src.Token(); got != second {
		t.Errorf("recheck window ignored: %q", got)
	}
	if _, _, err := NewTokenSource(filepath.Join(t.TempDir(), "no", "dir", "token")); err == nil {
		t.Error("token source in a missing directory")
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadState(dir); err == nil || !strings.Contains(err.Error(), "no daemon is running") {
		t.Errorf("missing state = %v", err)
	}
	want := State{Listen: "127.0.0.1:41234", PID: 42, StartedAt: time.Now().UTC().Truncate(time.Second)}
	if err := WriteState(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(dir)
	if err != nil || got != want {
		t.Errorf("ReadState = %+v, %v", got, err)
	}
	if runtime.GOOS != "windows" && mode(t, filepath.Join(dir, StateFileName)) != 0o600 {
		t.Errorf("state file mode = %04o", mode(t, filepath.Join(dir, StateFileName)))
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(dir); err == nil {
		t.Error("empty state accepted")
	}
	RemoveState(dir)
	if _, err := os.Stat(filepath.Join(dir, StateFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Error("state file not removed")
	}
	if err := WriteState(filepath.Join(dir, "missing"), want); err == nil {
		t.Error("state written into a missing directory")
	}
}

func TestAccessFor(t *testing.T) {
	a, err := AccessFor("127.0.0.1:41234", "tok", "gw-17")
	if err != nil || a.URL != "http://127.0.0.1:41234/#token=tok" || a.SSH != "ssh -N -L 41234:127.0.0.1:41234 <user>@gw-17" {
		t.Errorf("loopback = %+v, %v", a, err)
	}
	// The token is in the fragment, never in the query.
	if u, _ := url.Parse(a.URL); u.RawQuery != "" || u.Fragment != "token=tok" {
		t.Errorf("URL parts = %+v", u)
	}
	if a, _ := AccessFor("127.0.0.1:8765", "tok", ""); a.SSH != "" {
		t.Errorf("SSH hint without a host name: %q", a.SSH)
	}
	if a, _ := AccessFor("[::1]:8765", "tok", "gw"); a.URL != "http://[::1]:8765/#token=tok" || a.SSH == "" {
		t.Errorf("IPv6 loopback = %+v", a)
	}
	// Bound to every address, as in a container: no address to name but
	// loopback, and no forward to suggest.
	for _, listen := range []string{"0.0.0.0:8765", "[::]:8765", ":8765"} {
		if a, _ := AccessFor(listen, "tok", "gw"); a.URL != "http://127.0.0.1:8765/#token=tok" || a.SSH != "" {
			t.Errorf("%s = %+v", listen, a)
		}
	}
	// Not loopback: no forward to suggest.
	if a, _ := AccessFor("192.168.1.10:8765", "tok", "gw"); a.URL != "http://192.168.1.10:8765/#token=tok" || a.SSH != "" {
		t.Errorf("remote bind = %+v", a)
	}
	if _, err := AccessFor("nonsense", "tok", "gw"); err == nil {
		t.Error("bad listen address accepted")
	}
}

func TestCheckListen(t *testing.T) {
	for _, listen := range []string{DefaultListen, "127.0.0.1:0", "localhost:8765", "[::1]:8765", "127.8.9.10:1"} {
		if warning, err := CheckListen(listen, false); err != nil || warning != "" {
			t.Errorf("CheckListen(%s) = %q, %v", listen, warning, err)
		}
	}
	for _, listen := range []string{"0.0.0.0:8765", ":8765", "192.168.1.10:8765", "[::]:8765", "gateway.lan:8765"} {
		if _, err := CheckListen(listen, false); err == nil || !strings.Contains(err.Error(), "--allow-remote-bind") {
			t.Errorf("CheckListen(%s) without the flag = %v", listen, err)
		}
		warning, err := CheckListen(listen, true)
		if err != nil || !strings.Contains(warning, "not encrypted") || !strings.Contains(warning, "token") {
			t.Errorf("CheckListen(%s) with the flag = %q, %v", listen, warning, err)
		}
	}
	if _, err := CheckListen("127.0.0.1", true); err == nil {
		t.Error("address without port accepted")
	}
}

func TestHosts(t *testing.T) {
	loop := []string{"127.0.0.1", "localhost", "::1"}
	if got := Hosts("127.0.0.1:8765", nil); !slices.Equal(got, loop) {
		t.Errorf("loopback = %v", got)
	}
	if got := Hosts("0.0.0.0:8765", nil); !slices.Equal(got, loop) {
		t.Errorf("unspecified = %v", got)
	}
	if got := Hosts(":8765", []string{"gw.lan"}); !slices.Equal(got, append(slices.Clone(loop), "gw.lan")) {
		t.Errorf("empty host = %v", got)
	}
	if got := Hosts("192.168.1.10:8765", []string{"gw.lan"}); !slices.Equal(got, append(slices.Clone(loop), "192.168.1.10", "gw.lan")) {
		t.Errorf("specific = %v", got)
	}
}

// R14: only the serial devices the operator named.
func TestSerialAllowList(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"ttyUSB0", "ttyUSB1", "ttyACM0", "other"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := NewSerialAllowList([]string{filepath.Join(dir, "ttyUSB*"), filepath.Join(dir, "ttyACM0"), filepath.Join(dir, "ttyUSB0")})
	if err != nil {
		t.Fatal(err)
	}
	for device, want := range map[string]bool{
		filepath.Join(dir, "ttyUSB0"): true, filepath.Join(dir, "ttyUSB7"): true, filepath.Join(dir, "ttyACM0"): true,
		filepath.Join(dir, "ttyACM1"): false, filepath.Join(dir, "other"): false, "/etc/passwd": false,
		filepath.Join(dir, "sub", "ttyUSB0"): false, filepath.Join(dir, "ttyUSB0", "..", "other"): false,
	} {
		if err := l.Allow(device); (err == nil) != want {
			t.Errorf("%s: allowed=%v, want %v", device, err == nil, want)
		}
	}
	// What exists and matches, once each, sorted.
	want := []string{filepath.Join(dir, "ttyACM0"), filepath.Join(dir, "ttyUSB0"), filepath.Join(dir, "ttyUSB1")}
	if got := l.Ports(); !slices.Equal(got, want) {
		t.Errorf("Ports = %v", got)
	}

	none, err := NewSerialAllowList(nil)
	if err != nil || none.Allow("/dev/ttyUSB0") == nil || len(none.Ports()) != 0 {
		t.Errorf("an empty list allows something: %v", err)
	}
	if _, err := NewSerialAllowList([]string{"/dev/tty[USB"}); err == nil {
		t.Error("a malformed pattern was accepted")
	}
	// Whatever this machine has, the call works.
	_ = LocalSerialPorts()
}

func TestIsTemporary(t *testing.T) {
	for dir, want := range map[string]bool{
		filepath.Join(os.TempDir(), "sunspec-explorer-501"): true, os.TempDir(): true,
		"/tmp/x": true, "/var/tmp/x": true, "/var/lib/sunspec-explorer": false, "/data": false, "/tmpfoo": false,
	} {
		if got := IsTemporary(dir); got != want {
			t.Errorf("IsTemporary(%s) = %v, want %v", dir, got, want)
		}
	}
}

// A13: the daemon only connects where it is allowed to.
func TestTargetAllowList(t *testing.T) {
	target := func(host string) *url.URL { return &url.URL{Scheme: "tcp", Host: net.JoinHostPort(host, "502")} }

	def, err := NewTargetAllowList(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"10.1.2.3", "172.16.0.1", "172.31.255.254", "192.168.1.100", "169.254.10.10", "fe80::1", "fd00::1", "::ffff:192.168.1.5"} {
		if err := def.Allow(target(host)); err != nil {
			t.Errorf("%s refused by default: %v", host, err)
		}
	}
	for _, host := range []string{"8.8.8.8", "127.0.0.1", "172.32.0.1", "192.169.0.1", "::1", "2001:db8::1", "0.0.0.0"} {
		if err := def.Allow(target(host)); err == nil || !strings.Contains(err.Error(), "--allow-target") {
			t.Errorf("%s allowed by default: %v", host, err)
		}
	}

	custom, err := NewTargetAllowList([]string{"127.0.0.0/8", "203.0.113.7", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"127.0.0.1": true, "203.0.113.7": true, "203.0.113.8": false, "2001:db8::5": true, "10.0.0.1": false} {
		if err := custom.Allow(target(host)); (err == nil) != want {
			t.Errorf("%s: allowed=%v, want %v", host, err == nil, want)
		}
	}
	if _, err := NewTargetAllowList([]string{"not-a-cidr"}); err == nil {
		t.Error("bad CIDR accepted")
	}

	// A name is allowed only when every address behind it is.
	names := map[string][]netip.Addr{
		"inverter.lan": {netip.MustParseAddr("192.168.1.50")},
		"mixed.lan":    {netip.MustParseAddr("192.168.1.50"), netip.MustParseAddr("8.8.8.8")},
	}
	def.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		if addrs, ok := names[host]; ok {
			return addrs, nil
		}
		return nil, errors.New("no such host")
	}
	if err := def.Allow(target("inverter.lan")); err != nil {
		t.Errorf("inverter.lan: %v", err)
	}
	if err := def.Allow(target("mixed.lan")); err == nil {
		t.Error("a name with a public address was allowed")
	}
	if err := def.Allow(target("unknown.lan")); err == nil || !strings.Contains(err.Error(), "cannot be resolved") {
		t.Errorf("unknown name: %v", err)
	}
}

// The URL of a daemon on a link-local address is one a browser can open.
func TestAccessForZone(t *testing.T) {
	a, err := AccessFor("[fe80::1%eth0]:8765", "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://[fe80::1%25eth0]:8765/#token=tok"; a.URL != want {
		t.Errorf("URL = %q, want %q", a.URL, want)
	}
}
