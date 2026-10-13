// SPDX-License-Identifier: MIT

package daemon

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TokenFileName is the name of the token file in the data directory.
const TokenFileName = "token"

// tokenBytes is the size of a token before encoding: 256 bits.
const tokenBytes = 32

// newToken returns a random token that is safe in a URL fragment.
func newToken() string {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b) // never fails (crypto/rand, Go 1.24+)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ReadToken returns the token in the file at path. It refuses a file that
// others can read, and a file that holds no usable token.
func ReadToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s is not a regular file", path)
	}
	if err := checkPrivateFile(path, info); err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n#&?/") {
		return "", fmt.Errorf("token file %s does not hold a valid token: delete it or run \"sunspec-explorerd token rotate\"", path)
	}
	return token, nil
}

// LoadOrCreateToken returns the token in the file at path, creating the file
// with a new token and mode 0600 when it does not exist. created tells which.
func LoadOrCreateToken(path string) (token string, created bool, err error) {
	token, err = ReadToken(path)
	if err == nil {
		return token, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	token, err = RotateToken(path)
	return token, err == nil, err
}

// RotateToken replaces the token file at path with a new token and returns
// it. The file is written under another name with mode 0600 and renamed, so
// that a reader sees the old token or the new one, never part of one. A
// running daemon picks the new token up by itself (see TokenSource).
func RotateToken(path string) (string, error) {
	token := newToken()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("token file: %w", err)
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	return token, nil
}

// TokenSource hands out the current token of a token file and follows its
// rotation: when the file changes, the next call returns the new token.
type TokenSource struct {
	path string
	// recheck is how long a result is trusted before the file is looked at
	// again, so that a busy daemon does not stat it for every request.
	recheck time.Duration

	mu    sync.Mutex
	token string
	// file is the token file as it was when token was read. Rotation renames
	// a new file into place, so the file is then another one; its
	// modification time alone would not tell, because a file system may give
	// two files written within a few milliseconds the same time.
	file    os.FileInfo
	checked time.Time
}

// NewTokenSource loads the token file at path, creating it when missing.
func NewTokenSource(path string) (src *TokenSource, created bool, err error) {
	token, created, err := LoadOrCreateToken(path)
	if err != nil {
		return nil, false, err
	}
	s := &TokenSource{path: path, recheck: time.Second, token: token, checked: time.Now()}
	s.file, _ = os.Stat(path)
	return s, created, nil
}

// Token returns the token that is valid now. When the token file is gone or
// unusable, the last good token stays valid: deleting a file must not open
// the daemon, and must not lock its user out either.
func (s *TokenSource) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.checked) < s.recheck {
		return s.token
	}
	s.checked = time.Now()
	info, err := os.Stat(s.path)
	if err != nil || (s.file != nil && os.SameFile(s.file, info) &&
		info.ModTime().Equal(s.file.ModTime()) && info.Size() == s.file.Size()) {
		return s.token
	}
	if token, err := ReadToken(s.path); err == nil {
		s.token, s.file = token, info
	}
	return s.token
}
