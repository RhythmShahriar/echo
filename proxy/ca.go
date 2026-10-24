package proxy

import (
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

// File names inside the CA directory.
const (
	caCertFile = "ca.pem"
	caKeyFile  = "ca-key.pem"
)

// caValidity is long so a trusted CA keeps working across staging
// rebuilds; leaves are short and made on demand.
const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 365 * 24 * time.Hour
	leafCacheMax = 1024
)

// CA is Echo's local certificate authority. Clients that should be
// intercepted trust its certificate (CertPath); Echo signs a leaf for each
// host they connect to. The key never leaves the machine.
type CA struct {
	Cert     *x509.Certificate
	Key      *ecdsa.PrivateKey
	PEM      []byte // the certificate, PEM-encoded: what `rhythm echo ca` prints
	CertPath string // where PEM is stored, for NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE, curl --cacert

	leafKey *ecdsa.PrivateKey // one key for every leaf: signing is the slow part, not keygen, but this keeps it cheap

	mu    sync.Mutex
	order *list.List // most recently used first; values are *leafEntry
	cache map[string]*list.Element
}

type leafEntry struct {
	host string
	cert *tls.Certificate
}

// DefaultCADir is where LoadCA keeps the CA when given no directory:
// the Rhythm cache directory for echo, under ca/.
func DefaultCADir() (string, error) {
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
// means DefaultCADir. A CA close to expiry is replaced, which means
// clients must trust the new one.
func LoadCA(dir string) (*CA, error) {
	if dir == "" {
		d, err := DefaultCADir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	certPath, keyPath := filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile)
	ca, err := readCA(certPath, keyPath)
	if err == nil && time.Until(ca.Cert.NotAfter) > 30*24*time.Hour {
		return ca.init()
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading Echo's CA in %s: %w (delete the directory to make a new one)", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ca, err = newCA()
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(ca.Key)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	if err := writeAtomic(certPath, ca.PEM, 0o644); err != nil {
		return nil, err
	}
	ca.CertPath = certPath
	return ca.init()
}

func readCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath) //nolint:gosec // our own cache directory
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath) //nolint:gosec // our own cache directory
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
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
	return &CA{Cert: cert, Key: key, PEM: certPEM, CertPath: certPath}, nil
}

func newCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
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
	return &CA{Cert: cert, Key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

func (ca *CA) init() (*CA, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	ca.leafKey, ca.order, ca.cache = k, list.New(), map[string]*list.Element{}
	return ca, nil
}

// Pool returns a cert pool holding just this CA, for Go clients.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
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
	if el, ok := ca.cache[host]; ok {
		e := el.Value.(*leafEntry)
		if time.Until(e.cert.Leaf.NotAfter) > 24*time.Hour {
			ca.order.MoveToFront(el)
			return e.cert, nil
		}
		ca.order.Remove(el)
		delete(ca.cache, host)
	}
	c, err := ca.sign(host)
	if err != nil {
		return nil, err
	}
	ca.cache[host] = ca.order.PushFront(&leafEntry{host: host, cert: c})
	for ca.order.Len() > leafCacheMax {
		old := ca.order.Back()
		ca.order.Remove(old)
		delete(ca.cache, old.Value.(*leafEntry).host)
	}
	return c, nil
}

func (ca *CA) sign(host string) (*tls.Certificate, error) {
	now := time.Now()
	notAfter := now.Add(leafValidity)
	if notAfter.After(ca.Cert.NotAfter) {
		notAfter = ca.Cert.NotAfter
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
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &ca.leafKey.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, ca.Cert.Raw}, PrivateKey: ca.leafKey, Leaf: leaf}, nil
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
