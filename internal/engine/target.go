// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/otfabric/go-modbus"

	"github.com/otfabric/sunspec-explorer/internal/model"
	"github.com/otfabric/sunspec-explorer/internal/service"
)

const defaultPort = "502"

// Defaults of a serial target: what most SunSpec devices ship with.
const (
	DefaultBaud     = 9600
	DefaultDataBits = 8
	DefaultStopBits = 1
)

// URL schemes of a target.
const (
	schemeTCP        = "tcp"
	schemeRTUOverTCP = "rtuovertcp"
	schemeTLS        = "tcp+tls"
	schemeRTU        = "rtu"
)

// target is a validated model.TargetConfig.
type target struct {
	scheme string
	// url is the target as a URL, for the network allow-list. It is nil for a
	// serial target.
	url *url.URL
	// device is the serial device of an rtu:// target.
	device string
	// display is the URL as stored and shown: scheme, host and port, or
	// scheme and device. Nothing in it is secret.
	display   string
	unitID    uint8
	regType   modbus.RegType
	bases     []uint16
	maxModels int
	timeout   time.Duration

	baud     uint
	dataBits uint
	parity   modbus.Parity
	stopBits uint
}

// parseTarget validates cfg. A network URL must not carry credentials, a
// path or a query, and a serial URL names a device and nothing else: nothing
// of a target is secret, so it can be stored, shown and exported as it is.
func parseTarget(cfg model.TargetConfig) (*target, error) {
	invalid := func(format string, args ...any) (*target, error) {
		return nil, service.Errorf(service.CodeInvalidArgument, format, args...)
	}

	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return invalid("target URL is empty")
	}
	scheme, rest, found := strings.Cut(raw, "://")
	if !found {
		return invalid("target URL is not valid: use tcp://host:port, rtuovertcp://host:port, tcp+tls://host:port or rtu://<device>")
	}

	t := &target{
		scheme:    scheme,
		unitID:    cfg.UnitID,
		regType:   modbus.HoldingRegister,
		maxModels: cfg.MaxModels,
		timeout:   DefaultTimeout,
	}
	switch scheme {
	case schemeTCP, schemeRTUOverTCP, schemeTLS:
		u, err := url.Parse(raw)
		if err != nil {
			return invalid("target URL is not valid")
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
		t.url = &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
		t.display = t.url.String()
		if cfg.Serial != nil {
			return invalid("serial settings apply to rtu:// targets only")
		}
	case schemeRTU:
		if rest == "" || strings.ContainsAny(rest, "?#") ||
			strings.ContainsFunc(rest, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
			return invalid("serial target must be rtu://<device>, for example rtu:///dev/ttyUSB0 or rtu://COM3")
		}
		t.device = rest
		t.display = schemeRTU + "://" + rest
	default:
		return invalid("unsupported URL scheme %q: use tcp://, rtuovertcp://, tcp+tls:// or rtu://", scheme)
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

	if scheme == schemeRTU {
		if err := t.serial(cfg.Serial); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// serial validates the settings of a serial line. What is not given is
// 9600 baud, 8 data bits, no parity, 1 stop bit.
func (t *target) serial(cfg *model.SerialConfig) error {
	t.baud, t.dataBits, t.parity, t.stopBits = DefaultBaud, DefaultDataBits, modbus.ParityNone, DefaultStopBits
	if cfg == nil {
		return nil
	}
	if cfg.Baud != 0 {
		if cfg.Baud < 300 || cfg.Baud > 1000000 {
			return service.Errorf(service.CodeInvalidArgument, "baud rate %d is out of range (300..1000000)", cfg.Baud)
		}
		t.baud = uint(cfg.Baud)
	}
	if cfg.DataBits != 0 {
		if cfg.DataBits < 7 || cfg.DataBits > 8 {
			return service.Errorf(service.CodeInvalidArgument, "data bits %d: use 7 or 8", cfg.DataBits)
		}
		t.dataBits = uint(cfg.DataBits)
	}
	switch cfg.Parity {
	case "", model.ParityNone:
	case model.ParityEven:
		t.parity = modbus.ParityEven
	case model.ParityOdd:
		t.parity = modbus.ParityOdd
	default:
		return service.Errorf(service.CodeInvalidArgument, "parity %q: use %q, %q or %q",
			cfg.Parity, model.ParityNone, model.ParityEven, model.ParityOdd)
	}
	if cfg.StopBits != 0 {
		if cfg.StopBits < 1 || cfg.StopBits > 2 {
			return service.Errorf(service.CodeInvalidArgument, "stop bits %d: use 1 or 2", cfg.StopBits)
		}
		t.stopBits = uint(cfg.StopBits)
	}
	return nil
}
