// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package proxy

import (
	"bytes"
	"container/list"
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

	"github.com/RhythmShahriar/rhythm/kit/config"
)

// File names inside the CA directory. ca.pem holds the current CA and,
// while the two overlap, the one it replaced: clients that re-read it
// trust both.
const (
	caCertFile     = "ca.pem"
	caKeyFile      = "ca-key.pem"
	caPrevCertFile = "ca-prev.pem"
	caPrevKeyFile  = "ca-prev-key.pem"
)

// A CA lasts a year so a leaked key stops working soon. It is replaced
// caRenew before it expires and Echo keeps signing with the old one for
// caRenew after that, which gives a month to trust the new one before
// anything breaks. Leaves are made on demand and never outlive their CA.
const (
	caValidity   = 365 * 24 * time.Hour
	caRenew      = 30 * 24 * time.Hour
	leafValidity = 365 * 24 * time.Hour
	leafCacheMax = 1024
)

// CA is Echo's local certificate authority. Clients that should be
// intercepted trust its certificate (CertPath); Echo signs a leaf for each
// host they connect to. The key never leaves the machine.
type CA struct {
	Cert     *x509.Certificate
	Key      *ecdsa.PrivateKey
	PEM      []byte // the certificates to trust, PEM-encoded: what `rhythm echo ca --pem` prints
	CertPath string // where PEM is stored, for NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE, curl --cacert

	// Notices says what LoadCA changed on disk (a move or a rotation), in
	// sentences for the user. Empty when nothing changed.
	Notices []string

	prev    *x509.Certificate // the CA this one replaced, while they overlap
	prevKey *ecdsa.PrivateKey
	now     func() time.Time

	leafKey *ecdsa.PrivateKey // one key for every leaf, so a new leaf costs a signature and no keygen

	mu    sync.Mutex
	order *list.List // most recently used first; values are *leafEntry
	cache map[string]*list.Element
}

type leafEntry struct {
	host   string
	cert   *tls.Certificate
	signer *x509.Certificate
}

// DefaultCADir is where LoadCA keeps the CA when given no directory:
// echo/ca under the Rhythm config directory. Earlier versions kept it in
// the cache directory, which backups sync and cleaners wipe.
func DefaultCADir() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "echo", "ca"), nil
}

// legacyCADir is where earlier versions kept the CA.
func legacyCADir() (string, error) {
	d, err := config.CacheDir("echo")
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "ca"), nil
}

// DefaultCAPath is where LoadCA("") keeps the certificate, whether or not
// it has been created yet: what `rhythm echo ca` prints.
func DefaultCAPath() (string, error) {
	d, err := DefaultCADir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, caCertFile), nil
}

// LoadCA loads the CA from dir, creating it on first use. An empty dir
// means DefaultCADir, which first takes over a CA left in the old cache
// directory. A CA within 30 days of expiry, or one made to last longer
// than a year, is replaced by a new one; Notices says so, since clients
// must trust the new one within the month.
func LoadCA(dir string) (*CA, error) {
	legacy := ""
	if dir == "" {
		d, err := DefaultCADir()
		if err != nil {
			return nil, err
		}
		dir = d
		if l, err := legacyCADir(); err == nil {
			legacy = l
		}
	}
	return loadCA(dir, legacy, time.Now)
}

// loadCA is LoadCA with the legacy directory to migrate from (none when
// empty) and the clock, so tests can step past a rotation.
func loadCA(dir, legacy string, now func() time.Time) (*CA, error) {
	var notices []string
	if legacy != "" {
		moved, err := migrateCA(legacy, dir)
		if err != nil {
			return nil, fmt.Errorf("moving Echo's CA from %s to %s: %w", legacy, dir, err)
		}
		if moved {
			notices = append(notices, "moved Echo's CA from "+legacy+" to "+dir+", out of the cache folder that cleaners wipe; point NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE, SSL_CERT_FILE or --cacert at "+filepath.Join(dir, caCertFile))
		}
	}
	certPath, keyPath := filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile)
	ca, err := readCA(certPath, keyPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if old := interruptedRotation(dir, certPath); old != nil {
			ca, err = old, nil // rotate again, below
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading Echo's CA in %s: %w (delete the directory to make a new one)", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	t := now()
	switch {
	case ca == nil:
		if ca, err = newCA(t); err != nil {
			return nil, err
		}
		if err := writeKey(keyPath, ca.Key); err != nil {
			return nil, err
		}
	case needsRotation(ca.Cert, t):
		old := ca
		if ca, err = newCA(t); err != nil {
			return nil, err
		}
		ca.prev, ca.prevKey = old.Cert, old.Key
		// The old CA is saved first. ca.pem gets the new certificate only
		// at the end, after its key, so a stop in between leaves the old
		// ca.pem next to the new key: interruptedRotation spots that on
		// the next start, which then rotates again from the saved pair.
		if err := writeKey(filepath.Join(dir, caPrevKeyFile), old.Key); err != nil {
			return nil, err
		}
		if err := writeAtomic(filepath.Join(dir, caPrevCertFile), certPEM(old.Cert), 0o644); err != nil {
			return nil, err
		}
		if err := writeKey(keyPath, ca.Key); err != nil {
			return nil, err
		}
		notices = append(notices, fmt.Sprintf("made a new CA, valid until %s, to replace the one that expires on %s: trust %s again; Echo keeps signing with the old CA until %s",
			day(ca.Cert.NotAfter), day(old.Cert.NotAfter), certPath, day(ca.overlapEnd())))
	default:
		ca.prev, ca.prevKey = readPrev(dir, ca.Cert)
	}
	ca.now = now
	if ca.prev != nil && !ca.overlapping(t) {
		ca.prev, ca.prevKey = nil, nil
		_ = os.Remove(filepath.Join(dir, caPrevKeyFile))
		_ = os.Remove(filepath.Join(dir, caPrevCertFile))
	}
	ca.PEM = certPEM(ca.Cert)
	if ca.prev != nil {
		ca.PEM = append(ca.PEM, certPEM(ca.prev)...)
	}
	if on, err := os.ReadFile(certPath); err != nil || !bytes.Equal(on, ca.PEM) { //nolint:gosec // our own config directory
		if err := writeAtomic(certPath, ca.PEM, 0o644); err != nil {
			return nil, err
		}
	}
	ca.CertPath, ca.Notices = certPath, notices
	return ca.init()
}

// needsRotation reports whether cert is close to expiry at t, or was
// made by a version that gave CAs ten years.
func needsRotation(cert *x509.Certificate, t time.Time) bool {
	return cert.NotAfter.Sub(t) <= caRenew || cert.NotAfter.Sub(cert.NotBefore) > caValidity+48*time.Hour
}

// overlapEnd is when Echo stops signing with the previous CA: caRenew
// after this one was made (NotBefore is an hour early for clock skew).
func (ca *CA) overlapEnd() time.Time { return ca.Cert.NotBefore.Add(time.Hour + caRenew) }

func (ca *CA) overlapping(t time.Time) bool {
	return ca.prev != nil && t.Before(ca.overlapEnd()) && ca.prev.NotAfter.Sub(t) > 24*time.Hour
}

// signer is the CA that signs new leaves at t: the previous one while
// the two overlap, so clients that only trust it keep working.
func (ca *CA) signer(t time.Time) (*x509.Certificate, *ecdsa.PrivateKey) {
	if ca.overlapping(t) {
		return ca.prev, ca.prevKey
	}
	return ca.Cert, ca.Key
}

// interruptedRotation returns the saved previous CA when ca.pem still
// holds it (only as its current certificate) but ca-key.pem doesn't
// match: a rotation that stopped after the new key was written and
// before ca.pem was. It returns nil for any other broken state.
func interruptedRotation(dir, certPath string) *CA {
	prev, err := readCA(filepath.Join(dir, caPrevCertFile), filepath.Join(dir, caPrevKeyFile))
	if err != nil {
		return nil
	}
	on, err := os.ReadFile(certPath) //nolint:gosec // our own config directory
	if err != nil {
		return nil
	}
	b, _ := pem.Decode(on)
	if b == nil {
		return nil
	}
	cur, err := x509.ParseCertificate(b.Bytes)
	if err != nil || !cur.Equal(prev.Cert) {
		return nil
	}
	prev.CertPath = certPath
	return prev
}

// readPrev loads the previous CA kept for overlap, or nothing when it is
// missing, broken or not the one cur replaced.
func readPrev(dir string, cur *x509.Certificate) (*x509.Certificate, *ecdsa.PrivateKey) {
	p, err := readCA(filepath.Join(dir, caPrevCertFile), filepath.Join(dir, caPrevKeyFile))
	if err != nil || p.Cert.Equal(cur) {
		return nil, nil
	}
	return p.Cert, p.Key
}

// migrateCA moves the CA files from the old directory to dir, unless dir
// already has a CA. Each file is rewritten rather than renamed, so the
// move works across file systems and the keys end up 0600 whatever they
// were before.
func migrateCA(from, to string) (bool, error) {
	if _, err := os.Stat(filepath.Join(to, caCertFile)); err == nil {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(from, caCertFile)); err != nil {
		return false, nil
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return false, err
	}
	// Keys first, then the certificates: ca.pem appearing is what marks
	// the move done.
	files := []struct {
		name string
		perm os.FileMode
	}{{caKeyFile, 0o600}, {caPrevKeyFile, 0o600}, {caPrevCertFile, 0o644}, {caCertFile, 0o644}}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(from, f.name)) //nolint:gosec // Echo's own old cache directory
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if err := writeAtomic(filepath.Join(to, f.name), b, f.perm); err != nil {
			return false, err
		}
	}
	for _, f := range files {
		_ = os.Remove(filepath.Join(from, f.name))
	}
	_ = os.Remove(from) // only when empty
	return true, nil
}

// Installed returns the certificates of every Echo CA on this machine,
// in the config directory and the old cache directory, without creating,
// moving or rotating anything: what `rhythm echo ca --untrust` lists.
func Installed() []*x509.Certificate {
	var out []*x509.Certificate
	seen := map[string]bool{}
	for _, f := range []func() (string, error){DefaultCADir, legacyCADir} {
		d, err := f()
		if err != nil {
			continue
		}
		for _, name := range []string{caCertFile, caPrevCertFile} {
			b, err := os.ReadFile(filepath.Join(d, name)) //nolint:gosec // Echo's own directories
			if err != nil {
				continue
			}
			for _, c := range parseCerts(b) {
				if !seen[string(c.Raw)] {
					seen[string(c.Raw)] = true
					out = append(out, c)
				}
			}
		}
	}
	return out
}

// Dirs returns the directories that hold an Echo CA: the current one,
// the old cache directory or both, for removal steps.
func Dirs() []string {
	var out []string
	for _, f := range []func() (string, error){DefaultCADir, legacyCADir} {
		if d, err := f(); err == nil {
			if _, err := os.Stat(d); err == nil {
				out = append(out, d)
			}
		}
	}
	return out
}

func parseCerts(b []byte) []*x509.Certificate {
	var out []*x509.Certificate
	for {
		var blk *pem.Block
		if blk, b = pem.Decode(b); blk == nil {
			return out
		}
		if c, err := x509.ParseCertificate(blk.Bytes); err == nil && blk.Type == "CERTIFICATE" {
			out = append(out, c)
		}
	}
}

// TrustedBySystem reports whether this machine's own trust store accepts
// Echo's CA (the current one or the one it replaced). Then every program
// here accepts what Echo signs, not just the containers meant to.
func (ca *CA) TrustedBySystem() bool {
	roots, err := x509.SystemCertPool()
	if err != nil {
		return false
	}
	return TrustedBy(roots, ca.Cert, ca.prev)
}

// TrustedBy reports whether roots accepts any of certs, as a trust anchor.
func TrustedBy(roots *x509.CertPool, certs ...*x509.Certificate) bool {
	for _, c := range certs {
		if c == nil {
			continue
		}
		if _, err := c.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err == nil {
			return true
		}
	}
	return false
}

func readCA(certPath, keyPath string) (*CA, error) {
	certFile, err := os.ReadFile(certPath) //nolint:gosec // our own config directory
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath) //nolint:gosec // our own config directory
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certFile) // the first certificate is the current CA
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("key does not match certificate")
	}
	return &CA{Cert: cert, Key: key, CertPath: certPath}, nil
}

func newCA(now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "rhythm-echo CA", Organization: []string{"Rhythm"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true, // it signs leaves only
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key}, nil
}

func certPEM(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writeAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600)
}

func day(t time.Time) string { return t.UTC().Format("2 Jan 2006") }

func (ca *CA) init() (*CA, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	if ca.now == nil {
		ca.now = time.Now
	}
	ca.leafKey, ca.order, ca.cache = k, list.New(), map[string]*list.Element{}
	return ca, nil
}

// Pool returns a cert pool holding just this CA (and the one it
// replaced, while they overlap), for Go clients.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	if ca.prev != nil {
		p.AddCert(ca.prev)
	}
	return p
}

// Leaf returns a certificate for host (a name or an IP), from the cache
// when it is still fresh.
func (ca *CA) Leaf(host string) (*tls.Certificate, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return nil, errors.New("no host for certificate")
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	now := ca.now()
	signer, key := ca.signer(now)
	if el, ok := ca.cache[host]; ok {
		e := el.Value.(*leafEntry)
		if e.signer == signer && e.cert.Leaf.NotAfter.Sub(now) > 24*time.Hour {
			ca.order.MoveToFront(el)
			return e.cert, nil
		}
		ca.order.Remove(el)
		delete(ca.cache, host)
	}
	c, err := ca.sign(host, now, signer, key)
	if err != nil {
		return nil, err
	}
	ca.cache[host] = ca.order.PushFront(&leafEntry{host: host, cert: c, signer: signer})
	for ca.order.Len() > leafCacheMax {
		old := ca.order.Back()
		ca.order.Remove(old)
		delete(ca.cache, old.Value.(*leafEntry).host)
	}
	return c, nil
}

func (ca *CA) sign(host string, now time.Time, signer *x509.Certificate, signerKey *ecdsa.PrivateKey) (*tls.Certificate, error) {
	notAfter := now.Add(leafValidity)
	if notAfter.After(signer.NotAfter) {
		notAfter = signer.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: host, Organization: []string{"rhythm-echo"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &ca.leafKey.PublicKey, signerKey)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, signer.Raw}, PrivateKey: ca.leafKey, Leaf: leaf}, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// writeAtomic writes through a temporary file so a second Echo starting at
// the same moment never reads half a key.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
