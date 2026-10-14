// SPDX-License-Identifier: MIT

// Package readonly holds what makes the Explorer read-only at the Modbus
// level.
//
// go-sunspec needs the concrete *modbus.Client, which also has write
// methods. Every client the engine creates is therefore restricted with
// go-modbus's allow-list (Config.AllowedFunctionCodes, go-modbus v1.5.0) to
// FunctionCodes: the client refuses any other request before it reaches the
// transport, with modbus.ErrFunctionCodeNotAllowed. This is one of three
// layers; the others are the lint ban on write calls (.golangci.yml) and the
// wire-capture tests.
package readonly

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/otfabric/go-modbus"
)

// FunctionCodes returns the function codes the Explorer may send: read
// holding registers (FC03) and read input registers (FC04). It is the value
// for modbus.Config.AllowedFunctionCodes of every client.
func FunctionCodes() []modbus.FunctionCode {
	return []modbus.FunctionCode{modbus.FCReadHoldingRegisters, modbus.FCReadInputRegisters}
}

// Allowed reports whether fc is one of FunctionCodes.
func Allowed(fc modbus.FunctionCode) bool {
	return fc == modbus.FCReadHoldingRegisters || fc == modbus.FCReadInputRegisters
}

// Guard counts what the clients were asked to send: the requests that went
// out and the ones the allow-list refused. It stops nothing itself; a
// violation count above zero means that some code tried to send something
// the Explorer must never send. Set it as Config.Metrics; a Guard can be
// shared by all clients.
type Guard struct {
	requests   atomic.Uint64
	violations atomic.Uint64
}

var _ modbus.ClientMetrics = (*Guard)(nil)

// OnRequest counts a request with an allowed function code. go-modbus calls
// it for a refused request too, followed by OnError.
func (g *Guard) OnRequest(_ uint8, fc modbus.FunctionCode) {
	if Allowed(fc) {
		g.requests.Add(1)
	}
}

// OnResponse does nothing.
func (g *Guard) OnResponse(uint8, modbus.FunctionCode, time.Duration) {}

// OnError counts a request the allow-list refused.
func (g *Guard) OnError(_ uint8, _ modbus.FunctionCode, _ time.Duration, err error) {
	if errors.Is(err, modbus.ErrFunctionCodeNotAllowed) {
		g.violations.Add(1)
	}
}

// OnTimeout does nothing.
func (g *Guard) OnTimeout(uint8, modbus.FunctionCode, time.Duration) {}

// Requests returns the number of allowed requests the clients sent.
func (g *Guard) Requests() uint64 { return g.requests.Load() }

// Violations returns the number of requests the allow-list refused.
func (g *Guard) Violations() uint64 { return g.violations.Load() }
