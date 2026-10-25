// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	if a.Cert.Subject.CommonName != "rhythm-echo CA" || !a.Cert.IsCA || a.Cert.NotAfter.Sub(a.Cert.NotBefore) > caValidity+2*time.Hour || time.Until(a.Cert.NotAfter) < caValidity-time.Hour {
		t.Errorf("CA = %v %v %v", a.Cert.Subject, a.Cert.IsCA, a.Cert.NotAfter)
	}
	st, err := os.Stat(filepath.Join(dir, caKeyFile))
	if err != nil || runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
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

func TestLoadCARotatesWithOverlap(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	now := func() time.Time { return clock }
	first, err := loadCA(dir, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Notices) != 0 || len(parseCerts(first.PEM)) != 1 {
		t.Errorf("new CA: notices %q, %d certs", first.Notices, len(parseCerts(first.PEM)))
	}

	// 40 days before expiry: kept as is.
	clock = first.Cert.NotAfter.Add(-40 * 24 * time.Hour)
	same, err := loadCA(dir, "", now)
	if err != nil || !same.Cert.Equal(first.Cert) || len(same.Notices) != 0 {
		t.Fatalf("40 days out: rotated (%v, %q)", err, same.Notices)
	}

	// 20 days before expiry: a new CA, the old one still signs.
	clock = first.Cert.NotAfter.Add(-20 * 24 * time.Hour)
	rot, err := loadCA(dir, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if rot.Cert.Equal(first.Cert) || !rot.prev.Equal(first.Cert) || len(rot.Notices) != 1 {
		t.Fatalf("20 days out: not rotated (notices %q)", rot.Notices)
	}
	if !strings.Contains(rot.Notices[0], "made a new CA") || !strings.Contains(rot.Notices[0], "trust "+rot.CertPath+" again") {
		t.Errorf("notice = %q", rot.Notices[0])
	}
	if got := rot.Cert.NotAfter.Sub(clock); got < caValidity-time.Minute || got > caValidity+time.Minute {
		t.Errorf("new CA lasts %v", got)
	}
	if certs := parseCerts(rot.PEM); len(certs) != 2 || !certs[0].Equal(rot.Cert) || !certs[1].Equal(first.Cert) {
		t.Errorf("ca.pem does not hold the new CA then the old one")
	}
	if on, _ := os.ReadFile(rot.CertPath); !bytes.Equal(on, rot.PEM) {
		t.Error("ca.pem on disk differs from PEM")
	}
	if st, err := os.Stat(filepath.Join(dir, caPrevKeyFile)); err != nil || runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("previous key: %v %v", st, err)
	}
	oldOnly := x509.NewCertPool()
	oldOnly.AddCert(first.Cert)
	leaf, err := rot.Leaf("api.twilio.com")
	if err != nil {
		t.Fatal(err)
	}
	opts := x509.VerifyOptions{DNSName: "api.twilio.com", Roots: oldOnly, CurrentTime: clock}
	if _, err := leaf.Leaf.Verify(opts); err != nil {
		t.Errorf("during overlap a client that trusts only the old CA fails: %v", err)
	}

	// Reloaded during the overlap: same pair, no new notice.
	again, err := loadCA(dir, "", now)
	if err != nil || !again.Cert.Equal(rot.Cert) || !again.prev.Equal(first.Cert) || len(again.Notices) != 0 {
		t.Fatalf("reload in overlap: %v %q", err, again.Notices)
	}

	// After the overlap a running proxy switches to the new CA.
	clock = rot.overlapEnd().Add(time.Hour)
	leaf2, err := rot.Leaf("api.twilio.com")
	if err != nil {
		t.Fatal(err)
	}
	if leaf2 == leaf || !bytes.Equal(leaf2.Certificate[1], rot.Cert.Raw) {
		t.Error("leaf still signed by the old CA after the overlap")
	}
	newOnly := x509.NewCertPool()
	newOnly.AddCert(rot.Cert)
	opts.Roots, opts.CurrentTime = newOnly, clock
	if _, err := leaf2.Leaf.Verify(opts); err != nil {
		t.Error(err)
	}

	// And a fresh load drops the old CA from disk.
	done, err := loadCA(dir, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if done.prev != nil || len(parseCerts(done.PEM)) != 1 {
		t.Error("old CA kept after the overlap")
	}
	if _, err := os.Stat(filepath.Join(dir, caPrevCertFile)); !os.IsNotExist(err) {
		t.Errorf("ca-prev.pem left: %v", err)
	}
}

// TestLoadCAResumesAnInterruptedRotation: a rotation that stopped after
// the new key was written, before ca.pem, is redone on the next load. A
// mismatched key with nothing saved to fall back on is still an error.
func TestLoadCAResumesAnInterruptedRotation(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	first, err := loadCA(dir, "", now)
	if err != nil {
		t.Fatal(err)
	}
	clock = first.Cert.NotAfter.Add(-20 * 24 * time.Hour)
	// What loadCA leaves when it stops just before rewriting ca.pem.
	if err := writeKey(filepath.Join(dir, caPrevKeyFile), first.Key); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(dir, caPrevCertFile), certPEM(first.Cert), 0o644); err != nil {
		t.Fatal(err)
	}
	half, err := newCA(clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeKey(filepath.Join(dir, caKeyFile), half.Key); err != nil {
		t.Fatal(err)
	}

	rot, err := loadCA(dir, "", now)
	if err != nil {
		t.Fatalf("interrupted rotation: %v", err)
	}
	if rot.Cert.Equal(first.Cert) || !rot.prev.Equal(first.Cert) || len(rot.Notices) != 1 {
		t.Fatalf("not rotated again: notices %q", rot.Notices)
	}
	if certs := parseCerts(rot.PEM); len(certs) != 2 || !certs[0].Equal(rot.Cert) {
		t.Error("ca.pem does not hold the new CA first")
	}
	if again, err := loadCA(dir, "", now); err != nil || !again.Cert.Equal(rot.Cert) {
		t.Errorf("reload: %v", err)
	}

	// Negative: a stray key with no saved pair is not guessed around.
	other := t.TempDir()
	if _, err := loadCA(other, "", now); err != nil {
		t.Fatal(err)
	}
	if err := writeKey(filepath.Join(other, caKeyFile), half.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCA(other, "", now); err == nil || !strings.Contains(err.Error(), "delete the directory") {
		t.Errorf("mismatched key: %v", err)
	}
}

// legacyCA writes a ten-year CA the way earlier versions did, with a
// key that is wrongly world-readable.
func legacyCA(t *testing.T, dir string) *x509.Certificate {
	t.Helper()
	ca, err := newCA(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tmpl := *ca.Cert
	tmpl.NotAfter = time.Now().Add(10 * 365 * 24 * time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &ca.Key.PublicKey, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeKey(filepath.Join(dir, caKeyFile), ca.Key); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, caKeyFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, caCertFile), certPEM(cert), 0o644); err != nil { //nolint:gosec // a certificate
		t.Fatal(err)
	}
	return cert
}

func TestLoadCAMovesTheOldCacheCA(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "AppData", "LocalAppData"} {
		t.Setenv(v, filepath.Join(home, v))
	}
	legacy, err := legacyCADir()
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := DefaultCADir()
	if !strings.HasPrefix(dir, home) || !strings.HasPrefix(legacy, home) {
		t.Skipf("Echo's directories %s and %s are outside the test's", dir, legacy)
	}
	old := legacyCA(t, legacy)
	if got := Installed(); len(got) != 1 || !got[0].Equal(old) {
		t.Errorf("Installed before the move = %d certs", len(got))
	}

	ca, err := LoadCA("")
	if err != nil {
		t.Fatal(err)
	}
	if len(ca.Notices) != 2 || !strings.Contains(ca.Notices[0], "moved Echo's CA from "+legacy+" to "+dir) || !strings.Contains(ca.Notices[1], "made a new CA") {
		t.Errorf("notices = %q", ca.Notices)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("old cache directory left: %v", err)
	}
	for _, f := range []string{caKeyFile, caPrevKeyFile} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
	// The ten-year CA is retired: replaced at once, signing only for the overlap.
	if !ca.prev.Equal(old) || ca.Cert.NotAfter.Sub(ca.Cert.NotBefore) > caValidity+2*time.Hour {
		t.Error("ten-year CA not replaced by a one-year one")
	}
	if time.Until(ca.overlapEnd()) > caRenew+2*time.Hour {
		t.Errorf("overlap ends %v", ca.overlapEnd())
	}
	if got := Installed(); len(got) != 2 {
		t.Errorf("Installed after the move = %d certs", len(got))
	}

	// A second start moves nothing.
	again, err := LoadCA("")
	if err != nil || len(again.Notices) != 0 || !again.Cert.Equal(ca.Cert) {
		t.Errorf("second load: %v %q", err, again.Notices)
	}
}

func TestTrustedBy(t *testing.T) {
	a, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !TrustedBy(a.Pool(), a.Cert) || !TrustedBy(a.Pool(), b.Cert, a.Cert) {
		t.Error("a pool holding the CA does not trust it")
	}
	if TrustedBy(b.Pool(), a.Cert) || TrustedBy(x509.NewCertPool(), a.Cert, nil) {
		t.Error("a pool without the CA trusts it")
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
