// SPDX-License-Identifier: MIT

package engine

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// LoadTLS reads the identity for tcp+tls:// targets from PEM files named by
// the operator: the certificate this side presents with its private key, and
// the authorities (or the device certificates themselves) a device's
// certificate must chain to. All three are needed: Modbus/TCP Security is
// mutual TLS.
func LoadTLS(certFile, keyFile, caFile string) (*TLSConfig, error) {
	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, errors.New("TLS targets need all three of --tls-cert, --tls-key and --tls-ca")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("TLS certificate: %w", err)
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("TLS CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("TLS CA: %s holds no certificate", caFile)
	}
	return &TLSConfig{Certificate: cert, RootCAs: pool}, nil
}
