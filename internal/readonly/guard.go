// SPDX-License-Identifier: MIT

// Package readonly enforces that the Explorer only ever reads registers.
//
// go-modbus (v1.4.2) has no hook that can reject a request, and go-sunspec
// needs the concrete *modbus.Client, which also has write methods. The lowest
// place a request can be stopped is the metrics callback the client calls
// immediately before it sends: Guard panics there for every function code
// other than FC03 and FC04, before a byte reaches the wire, and Do turns that
// panic into an error. The guard is one of three layers; the others are the
// lint ban on write calls (.golangci.yml) and the wire-capture test
// (tests/wire).
package readonly

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/otfabric/go-modbus"
)

// Allowed reports whether fc is a function code the Explorer may send: read
// holding registers (FC03) or read input registers (FC04).
func Allowed(fc modbus.FunctionCode) bool {
	return fc == modbus.FCReadHoldingRegisters || fc == modbus.FCReadInputRegisters
}

// Violation is the error for an attempt to send a function code that is not
// allowed. The request was not sent.
type Violation struct {
	FunctionCode modbus.FunctionCode
}

// Error describes the violation.
func (v *Violation) Error() string {
	return fmt.Sprintf("readonly: function code 0x%02X blocked: only FC03 and FC04 are allowed", uint8(v.FunctionCode))
}

// Guard is a modbus.ClientMetrics that stops every request that is not FC03
// or FC04. Set it as Config.Metrics of every client; a Guard can be shared.
type Guard struct {
	requests   atomic.Uint64
	violations atomic.Uint64
}

var _ modbus.ClientMetrics = (*Guard)(nil)

// OnRequest panics with a *Violation for a function code that is not allowed.
// go-modbus calls it before it takes any lock and before it sends, so the
// panic leaves the client usable and nothing on the wire.
func (g *Guard) OnRequest(_ uint8, fc modbus.FunctionCode) {
	if !Allowed(fc) {
		g.violations.Add(1)
		panic(&Violation{FunctionCode: fc})
	}
	g.requests.Add(1)
}

// OnResponse does nothing.
func (g *Guard) OnResponse(uint8, modbus.FunctionCode, time.Duration) {}

// OnError does nothing.
func (g *Guard) OnError(uint8, modbus.FunctionCode, time.Duration, error) {}

// OnTimeout does nothing.
func (g *Guard) OnTimeout(uint8, modbus.FunctionCode, time.Duration) {}

// Requests returns the number of requests the guard let through.
func (g *Guard) Requests() uint64 { return g.requests.Load() }

// Violations returns the number of requests the guard stopped.
func (g *Guard) Violations() uint64 { return g.violations.Load() }

// Do runs fn and returns its error. A *Violation raised by a Guard inside fn
// is returned as the error; any other panic continues.
func Do(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			v, ok := r.(*Violation)
			if !ok {
				panic(r)
			}
			err = v
		}
	}()
	return fn()
}
