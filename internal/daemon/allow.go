// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"time"
)

// DefaultAllowedTargets are the networks the daemon connects to when none
// are configured: the private IPv4 ranges (RFC 1918), IPv4 and IPv6
// link-local, and IPv6 unique local addresses. Loopback and public addresses
// are not among them.
var DefaultAllowedTargets = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fe80::/10", "fc00::/7",
}

// TargetAllowList decides which devices the daemon may connect to, so that
// whoever holds the token cannot use a gateway to probe arbitrary networks.
type TargetAllowList struct {
	prefixes []netip.Prefix
	// resolve looks a host name up; tests replace it.
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

// NewTargetAllowList parses CIDRs (or single addresses). None selects
// DefaultAllowedTargets.
func NewTargetAllowList(cidrs []string) (*TargetAllowList, error) {
	if len(cidrs) == 0 {
		cidrs = DefaultAllowedTargets
	}
	l := &TargetAllowList{resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}}
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			addr, aerr := netip.ParseAddr(c)
			if aerr != nil {
				return nil, fmt.Errorf("allowed target %q is not a CIDR or an address", c)
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		l.prefixes = append(l.prefixes, p.Masked())
	}
	return l, nil
}

// Allow reports whether the host of u is an allowed target. A host name is
// allowed when every address it resolves to is.
func (l *TargetAllowList) Allow(u *url.URL) error {
	host := u.Hostname()
	var addrs []netip.Addr
	if addr, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{addr}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		addrs, err = l.resolve(ctx, host)
		if err != nil || len(addrs) == 0 {
			return fmt.Errorf("host %q cannot be resolved", host)
		}
	}
	for _, addr := range addrs {
		if !l.contains(addr.Unmap().WithZone("")) {
			return fmt.Errorf("%s is outside the daemon's allowed targets (--allow-target)", addr)
		}
	}
	return nil
}

func (l *TargetAllowList) contains(addr netip.Addr) bool {
	for _, p := range l.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
