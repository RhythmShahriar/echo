// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// TLSFiles are where SelfSigned keeps its certificate and key.
func TLSFiles(store string) (cert, key string) {
	return filepath.Join(store, "tls", "cert.pem"), filepath.Join(store, "tls", "key.pem")
}

// LoadTLS returns a server config for the certificate and key files.
func LoadTLS(certFile, keyFile string) (*tls.Config, error) {
	c, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{c}, MinVersion: tls.VersionTLS12}, nil
}

// SelfSigned returns a server config with the hub's own certificate,
// made on first use and kept in the store, so connectors that pinned or
// trusted it keep working across restarts. It names localhost, the
// loopback addresses, this machine's host name and hosts (names or IPs
// the sites reach the hub by). A kept certificate that doesn't cover
// hosts, or that expires within 30 days, is replaced.
func SelfSigned(store string, hosts []string, now time.Time) (*tls.Config, error) {
	certFile, keyFile := TLSFiles(store)
	names := []string{"localhost", "127.0.0.1", "::1"}
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, h)
	}
	for _, h := range hosts {
		if h = strings.TrimSpace(h); h != "" && !slices.Contains(names, h) {
			names = append(names, h)
		}
	}
	if cfg, err := LoadTLS(certFile, keyFile); err == nil && covers(cfg, names, now) {
		return cfg, nil
	}
	if err := writeSelfSigned(certFile, keyFile, names, now); err != nil {
		return nil, err
	}
	return LoadTLS(certFile, keyFile)
}

func covers(cfg *tls.Config, names []string, now time.Time) bool {
	leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil || now.Add(30*24*time.Hour).After(leaf.NotAfter) {
		return false
	}
	for _, n := range names {
		if leaf.VerifyHostname(n) != nil {
			return false
		}
	}
	return true
}

func writeSelfSigned(certFile, keyFile string, names []string, now time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Echo hub", Organization: []string{"Rhythm Echo"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(825 * 24 * time.Hour), // the longest browsers and macOS accept
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644) //nolint:gosec // a certificate is public
}

// Fingerprint is the SHA-256 of cfg's certificate, as AB:CD:… for
// comparing with what a client shows.
func Fingerprint(cfg *tls.Config) (string, error) {
	if cfg == nil || len(cfg.Certificates) == 0 || len(cfg.Certificates[0].Certificate) == 0 {
		return "", errors.New("no certificate")
	}
	sum := sha256.Sum256(cfg.Certificates[0].Certificate[0])
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":"), nil
}
