// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package server

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	"strings"
	"sync"
	"time"
)

// The testbed CA's files in its folder (testbed/consent/ca, git-ignored).
const (
	CACertFile = "ca.pem"
	CAKeyFile  = "ca-key.pem"
)

// SelfSignedHost is the scenario whose certificate is self-signed rather
// than issued by the testbed CA, for the probe that checks Echo refuses
// an untrusted certificate without -k.
const SelfSignedHost = "self-signed" + SiteSuffix

// MakeCA writes a new testbed CA into dir: a P-256 key and a certificate
// valid for two years, named "Rhythm Echo consent testbed CA". It
// refuses to overwrite one that exists.
func MakeCA(dir string) error {
	certPath, keyPath := filepath.Join(dir, CACertFile), filepath.Join(dir, CAKeyFile)
	if _, err := os.Stat(certPath); err == nil {
		return fmt.Errorf("%s exists; delete the folder to make a new CA", certPath)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Rhythm Echo consent testbed CA", Organization: []string{"Rhythm testbed"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// The CA may only sign the testbed's own names, so trusting it
		// on a machine can never vouch for a real site.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         append([]string{".test", "localhost"}, TagHosts()...),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
}

// CA is a loaded testbed CA that issues a certificate per host name on
// demand.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// leafKey signs every leaf: one key keeps issuing cheap while the
	// leaves are only ever trusted through the CA.
	leafKey *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
	self   *tls.Certificate
}

// LoadCA reads the CA that MakeCA wrote into dir.
func LoadCA(dir string) (*CA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, CACertFile)) //nolint:gosec // the testbed's own CA folder
	if err != nil {
		return nil, fmt.Errorf("testbed CA: %w (make ca creates it)", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, CAKeyFile)) //nolint:gosec // the testbed's own CA folder
	if err != nil {
		return nil, fmt.Errorf("testbed CA: %w (make ca creates it)", err)
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("testbed CA: not PEM; delete the folder and run make ca")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("testbed CA: %w", err)
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("testbed CA key: %w", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, leafKey: leafKey, leaves: map[string]*tls.Certificate{}}, nil
}

// Pool is a certificate pool holding only this CA, for clients that
// must trust the testbed.
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.cert)
	return p
}

// TLSConfig serves a certificate for whatever name the client asks for:
// one issued by the CA, or a self-signed one for SelfSignedHost.
func (c *CA) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
			if name == "" {
				name = "shop.test"
			}
			return c.leaf(name)
		},
	}
}

// leaf returns the certificate for name, issuing it on first use.
func (c *CA) leaf(name string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if name == SelfSignedHost {
		if c.self == nil {
			cert, err := issue(name, c.leafKey, nil, nil)
			if err != nil {
				return nil, err
			}
			c.self = cert
		}
		return c.self, nil
	}
	if cert, ok := c.leaves[name]; ok {
		return cert, nil
	}
	cert, err := issue(name, c.leafKey, c.cert, c.key)
	if err != nil {
		return nil, err
	}
	c.leaves[name] = cert
	return cert, nil
}

// issue makes a server certificate for name signed by parent, or
// self-signed when parent is nil.
func issue(name string, key *ecdsa.PrivateKey, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*tls.Certificate, error) {
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 3, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	signer, signKey := tmpl, key
	if parent != nil {
		signer, signKey = parent, parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signKey)
	if err != nil {
		return nil, err
	}
	chain := [][]byte{der}
	if parent != nil {
		chain = append(chain, parent.Raw)
	}
	return &tls.Certificate{Certificate: chain, PrivateKey: key}, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// sniffListener serves HTTP and HTTPS on one port. Chrome's
// --host-resolver-rules and Echo's --map-host send every name and port
// of a run to one address while HSTS-preloaded tracker hosts are always
// asked for over https while a plain site may be http, so each
// connection is told apart by its first byte: 0x16 starts a TLS
// handshake.
type sniffListener struct {
	net.Listener
	tls   *tls.Config // nil serves plain HTTP only
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newSniffListener(inner net.Listener, cfg *tls.Config) *sniffListener {
	l := &sniffListener{Listener: inner, tls: cfg, conns: make(chan net.Conn), done: make(chan struct{})}
	go l.loop()
	return l
}

func (l *sniffListener) loop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.once.Do(func() { close(l.done) })
			return
		}
		// Peeking waits for the client to speak, so it runs beside the
		// accept loop: a silent client can't hold up the others.
		go l.sniff(c)
	}
}

func (l *sniffListener) sniff(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		_ = c.Close()
		return
	}
	var conn net.Conn = &peekedConn{Conn: c, r: br}
	if first[0] == 0x16 {
		if l.tls == nil {
			_ = c.Close()
			return
		}
		conn = tls.Server(conn, l.tls)
	}
	select {
	case l.conns <- conn:
	case <-l.done:
		_ = c.Close()
	}
}

// Accept returns the next connection, TLS or plain.
func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close stops accepting.
func (l *sniffListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { close(l.done) })
	return err
}

// peekedConn reads first what the sniffer peeked.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
