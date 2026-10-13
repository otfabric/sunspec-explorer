// SPDX-License-Identifier: MIT

package service

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/otfabric/sunspec-explorer/internal/model"
)

func TestError(t *testing.T) {
	e := Errorf(CodeNotFound, "no session %q", "abc")
	if e.Error() != `no session "abc"` || CodeOf(e) != CodeNotFound || e.Unwrap() != nil {
		t.Errorf("Errorf = %+v", e)
	}
	w := Wrap(CodeConnectFailed, io.ErrUnexpectedEOF, "cannot connect to %s", "tcp://h:502")
	if w.Error() != "cannot connect to tcp://h:502: unexpected EOF" || !errors.Is(w, io.ErrUnexpectedEOF) {
		t.Errorf("Wrap = %v", w)
	}
	if CodeOf(nil) != "" || CodeOf(io.EOF) != CodeInternal || CodeOf(errors.Join(io.EOF, w)) != CodeConnectFailed {
		t.Error("CodeOf")
	}

	// The wire form carries code, message and attempts, not the wrapped error.
	w.Attempts = []model.ProbeAttempt{{BaseAddress: 40000, RegType: model.RegTypeHolding, Error: "timeout"}}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"connect_failed","message":"cannot connect to tcp://h:502: unexpected EOF",` +
		`"attempts":[{"base_address":40000,"reg_type":"holding","matched":false,"error":"timeout"}]}`
	if string(b) != want {
		t.Errorf("JSON = %s", b)
	}
}
