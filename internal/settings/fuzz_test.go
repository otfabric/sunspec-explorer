// SPDX-License-Identifier: MIT

package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/otfabric/sunspec-explorer/internal/engine"
)

// A settings file of any content is the same as an empty one or names three
// paths: no identity comes out of it unless all three load, and what is
// written back is a settings file.
func FuzzSettingsFile(f *testing.F) {
	f.Add([]byte(`{"tls":{"cert_file":"/a","key_file":"/b","ca_file":"/c"}}`))
	f.Add([]byte(`{"tls":{"cert_file":"","key_file":"\u0000"}}`))
	f.Add([]byte(`{"tls":null}`))
	f.Add([]byte(`{"tls":{"cert_file":1}}`))
	f.Add([]byte(`[]`))
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xfe})

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, FileName)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		var applied *engine.TLSConfig
		calls := 0
		s := NewTLS(path, func(c *engine.TLSConfig) { applied = c; calls++ })
		st := s.Settings()
		if calls != 1 {
			t.Fatalf("the identity was applied %d times", calls)
		}
		if st.Active || applied != nil {
			t.Fatalf("an identity came out of a settings file without real files: %+v", st)
		}
		if _, err := s.Clear(); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var back file
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("written back: %v\n%s", err, b)
		}
	})
}
