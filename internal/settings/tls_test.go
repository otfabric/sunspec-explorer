// SPDX-License-Identifier: MIT

package settings

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/sunspec-explorer/internal/engine"
)

// selfSigned writes a self-signed certificate and its key as PEM files.
func selfSigned(t *testing.T, dir, name string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestTLSSettings(t *testing.T) {
	dir := t.TempDir()
	cert, key := selfSigned(t, dir, "client")
	other, _ := selfSigned(t, dir, "other")
	path := filepath.Join(dir, "config", "sunspec-explorer", FileName)

	var applied []*engine.TLSConfig
	apply := func(cfg *engine.TLSConfig) { applied = append(applied, cfg) }

	// A fresh installation: nothing chosen, nothing active, the engine told so.
	tls := NewTLS(path, apply)
	if st := tls.Settings(); st != (TLSSettings{}) || len(applied) != 1 || applied[0] != nil {
		t.Fatalf("fresh = %+v, applied %v", st, applied)
	}

	// Files are chosen one at a time; the identity becomes active with the
	// third, and an incomplete set is not an error.
	st, err := tls.Set(TLSCert, cert)
	if err != nil || st.Active || st.Error != "" || st.CertFile != cert {
		t.Errorf("after the certificate = %+v, %v", st, err)
	}
	if st, err = tls.Set(TLSCA, other); err != nil || st.Active || st.Error != "" {
		t.Errorf("after the CA = %+v, %v", st, err)
	}
	// A key that does not belong to the certificate: said, not active.
	if st, err = tls.Set(TLSKey, cert); err != nil || st.Active || st.Error == "" {
		t.Errorf("with a wrong key = %+v, %v", st, err)
	}
	if applied[len(applied)-1] != nil {
		t.Error("a broken identity was handed to the engine")
	}
	st, err = tls.Set(TLSKey, key)
	if err != nil || !st.Active || st.Error != "" || st.KeyFile != key || st.FromFlags {
		t.Fatalf("complete = %+v, %v", st, err)
	}
	if got := applied[len(applied)-1]; got == nil || got.RootCAs == nil || len(got.Certificate.Certificate) != 1 {
		t.Errorf("the engine got %+v", got)
	}
	if _, err := tls.Set("password", cert); err == nil {
		t.Error("an unknown kind was accepted")
	}

	// The file holds paths only, and is the user's alone.
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), cert) || strings.Contains(string(saved), "PRIVATE KEY") {
		t.Errorf("settings file = %s", saved)
	}
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("settings file mode = %v", info.Mode().Perm())
	}

	// The next start finds the identity again.
	applied = nil
	again := NewTLS(path, apply)
	if st := again.Settings(); !st.Active || st.CertFile != cert || st.KeyFile != key || st.CAFile != other {
		t.Errorf("after a restart = %+v", st)
	}
	if len(applied) != 1 || applied[0] == nil {
		t.Errorf("after a restart the engine got %v", applied)
	}

	// A remembered file that is gone: reported, and the engine has none.
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	applied = nil
	broken := NewTLS(path, apply)
	if st := broken.Settings(); st.Active || st.Error == "" || st.KeyFile != key {
		t.Errorf("with a missing file = %+v", st)
	}
	if len(applied) != 1 || applied[0] != nil {
		t.Errorf("with a missing file the engine got %v", applied)
	}

	// Clear forgets everything.
	st, err = broken.Clear()
	if err != nil || st != (TLSSettings{}) {
		t.Errorf("Clear = %+v, %v", st, err)
	}
	if st := NewTLS(path, apply).Settings(); st != (TLSSettings{}) {
		t.Errorf("after Clear and a restart = %+v", st)
	}

	// Nowhere to remember: it works for this run.
	nowhere := NewTLS("", nil)
	if _, err := nowhere.Set(TLSCert, cert); err != nil {
		t.Errorf("without a settings file = %v", err)
	}
	// A settings file that cannot be written is reported, the choice stands.
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unwritable := NewTLS(filepath.Join(blocked, "x", FileName), nil)
	if st, err := unwritable.Set(TLSCert, cert); err == nil || st.CertFile != cert {
		t.Errorf("unwritable settings = %+v, %v", st, err)
	}
	// Garbage in the settings file is an empty one.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := NewTLS(path, nil).Settings(); st != (TLSSettings{}) {
		t.Errorf("garbage settings = %+v", st)
	}
	if p := DefaultPath(); p != "" && filepath.Base(p) != FileName {
		t.Errorf("DefaultPath = %s", p)
	}
}

// An identity from the command line is shown and cannot be changed.
func TestTLSFromFlags(t *testing.T) {
	tls := NewTLSFromFlags("a.crt", "a.key", "ca.crt")
	st := tls.Settings()
	if !st.Active || !st.FromFlags || st.CertFile != "a.crt" {
		t.Errorf("settings = %+v", st)
	}
	if _, err := tls.Set(TLSCert, "b.crt"); err == nil {
		t.Error("changed an identity from the command line")
	}
	if _, err := tls.Clear(); err == nil || !tls.Settings().Active {
		t.Error("cleared an identity from the command line")
	}
}
