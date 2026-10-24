// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCAPersistsWithPrivateKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	a, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Cert.Equal(b.Cert) {
		t.Error("second load made a new CA")
	}
	if a.Cert.Subject.CommonName != "rhythm-echo CA" || !a.Cert.IsCA || a.Cert.NotAfter.Sub(a.Cert.NotBefore).Hours() < 24*365*9 {
		t.Errorf("CA = %v %v %v", a.Cert.Subject, a.Cert.IsCA, a.Cert.NotAfter)
	}
	st, err := os.Stat(filepath.Join(dir, caKeyFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, %v", st.Mode(), err)
	}
	if a.CertPath != filepath.Join(dir, caCertFile) {
		t.Errorf("CertPath = %q", a.CertPath)
	}
	if err := os.WriteFile(filepath.Join(dir, caKeyFile), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCA(dir); err == nil {
		t.Error("a broken key was accepted")
	}
}

func TestLeafVerifiesForNamesAndIPs(t *testing.T) {
	ca, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.twilio.com", "10.0.0.7", "::1"} {
		c, err := ca.Leaf(host)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			t.Errorf("%s: %v", host, err)
		}
		if ip := net.ParseIP(host); ip != nil && (len(c.Leaf.IPAddresses) != 1 || len(c.Leaf.DNSNames) != 0) {
			t.Errorf("%s: SANs %v %v", host, c.Leaf.DNSNames, c.Leaf.IPAddresses)
		}
	}
	a, _ := ca.Leaf("API.Twilio.com.")
	b, _ := ca.Leaf("api.twilio.com")
	if a != b {
		t.Error("leaf not cached across case and trailing dot")
	}
}

func TestLeafCacheEvictsOldest(t *testing.T) {
	ca, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := ca.Leaf("h0.test")
	for i := 1; i <= leafCacheMax; i++ {
		if _, err := ca.Leaf("h" + itoa(i) + ".test"); err != nil {
			t.Fatal(err)
		}
	}
	if ca.order.Len() != leafCacheMax {
		t.Errorf("cache holds %d", ca.order.Len())
	}
	again, _ := ca.Leaf("h0.test")
	if again == first {
		t.Error("oldest leaf was not evicted")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, _ = io.WriteString(zw, s)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func bufioReader(c net.Conn) *bufio.Reader { return bufio.NewReader(c) }

// recorder is a minimal ResponseWriter.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(s int)           { r.status = s }
