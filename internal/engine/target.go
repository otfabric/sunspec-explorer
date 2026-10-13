// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/otfabric/go-modbus"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

const defaultPort = "502"

// target is a validated model.TargetConfig.
type target struct {
	url *url.URL
	// display is the URL as stored and shown: scheme, host and port only.
	display   string
	unitID    uint8
	regType   modbus.RegType
	bases     []uint16
	maxModels int
	timeout   time.Duration
}

// parseTarget validates cfg. Only network transports are accepted for now
// (serial and TLS targets arrive with M2), and a URL must not carry
// credentials, a path or a query: nothing of a target is secret, so the URL
// can be stored, shown and exported as it is.
func parseTarget(cfg model.TargetConfig) (*target, error) {
	invalid := func(format string, args ...any) (*target, error) {
		return nil, service.Errorf(service.CodeInvalidArgument, format, args...)
	}

	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return invalid("target URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return invalid("target URL is not valid")
	}
	switch u.Scheme {
	case "tcp", "rtuovertcp":
	default:
		return invalid("unsupported URL scheme %q: use tcp:// or rtuovertcp://", u.Scheme)
	}
	if u.User != nil {
		return invalid("target URL must not contain credentials")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return invalid("target URL must be scheme://host:port only")
	}
	host := u.Hostname()
	if host == "" {
		return invalid("target URL has no host")
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return invalid("target URL has an invalid port %q", port)
	}
	clean := &url.URL{Scheme: u.Scheme, Host: net.JoinHostPort(host, port)}

	t := &target{
		url:       clean,
		display:   clean.String(),
		unitID:    cfg.UnitID,
		regType:   modbus.HoldingRegister,
		maxModels: cfg.MaxModels,
		timeout:   DefaultTimeout,
	}
	if t.unitID == 0 {
		t.unitID = 1
	}
	if t.unitID > 247 && t.unitID != 255 {
		return invalid("unit ID %d is out of range (1..247, or 255)", t.unitID)
	}
	switch cfg.RegType {
	case "", model.RegTypeHolding:
	case model.RegTypeInput:
		t.regType = modbus.InputRegister
	default:
		return invalid("register type %q: use %q or %q", cfg.RegType, model.RegTypeHolding, model.RegTypeInput)
	}
	if cfg.TimeoutMS != 0 {
		t.timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
		if t.timeout < minTimeout || t.timeout > maxTimeout {
			return invalid("timeout %d ms is out of range (%d..%d)", cfg.TimeoutMS,
				minTimeout.Milliseconds(), maxTimeout.Milliseconds())
		}
	}
	if cfg.MaxModels < 0 || cfg.MaxModels > 1024 {
		return invalid("max models %d is out of range (0..1024)", cfg.MaxModels)
	}
	if len(cfg.BaseAddresses) > 16 {
		return invalid("at most 16 base addresses")
	}
	t.bases = append(t.bases, cfg.BaseAddresses...)
	return t, nil
}
