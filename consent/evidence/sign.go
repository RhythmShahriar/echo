// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	kitcli "github.com/RhythmShahriar/rhythm/kit/cli"
)

// The signature files of an evidence folder: consent.json.sig from
// --sign-key, consent.json.sigstore.json from the Action's keyless
// signing (cosign sign-blob), which this binary only points at.
const (
	SignatureFile = "consent.json.sig"
	SigstoreFile  = "consent.json.sigstore.json"
)

// SignatureAlgorithm is the only algorithm consent.json.sig uses.
const SignatureAlgorithm = "ed25519"

// maxKeyFile caps a key file read: a PEM ed25519 key is under 200 bytes.
const maxKeyFile = 16 << 10

// Signature is consent.json.sig: an ed25519 signature over the exact
// bytes of consent.json.
type Signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`    // KeyID of the public key
	Signature string `json:"signature"` // standard base64
}

// msgWantPrivate is the refusal of a --sign-key that isn't an ed25519
// private key in PEM.
const msgWantPrivate = "--sign-key: want an ed25519 private key in PEM (openssl genpkey -algorithm ed25519 -out team.key)"

// LoadSignKey reads the --sign-key file at path: a PEM "PRIVATE KEY"
// block holding a PKCS#8 ed25519 key, as openssl genpkey -algorithm
// ed25519 writes it. On Unix the file must be 0600 or stricter, since
// whoever can read it can sign evidence in the team's name. Every
// refusal is a usage error.
func LoadSignKey(path string) (ed25519.PrivateKey, error) {
	data, err := readKeyFile(path, "--sign-key", true)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, kitcli.Usagef("%s", msgWantPrivate)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, kitcli.Usagef("%s", msgWantPrivate)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, kitcli.Usagef("%s", msgWantPrivate)
	}
	return priv, nil
}

// LoadPublicKey reads the --key file of consent verify: a PEM "PUBLIC
// KEY" block holding an ed25519 key (openssl pkey -pubout writes one).
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	const want = "--key: want an ed25519 public key in PEM (openssl pkey -in team.key -pubout -out team.pub)"
	data, err := readKeyFile(path, "--key", false)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, kitcli.Usagef("%s", want)
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, kitcli.Usagef("%s", want)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, kitcli.Usagef("%s", want)
	}
	return pub, nil
}

// readKeyFile reads a key file named by flag, capped; private keys must
// not be readable by group or others on Unix (Windows has no such mode
// bits, so it isn't checked there).
func readKeyFile(path, flag string, private bool) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, kitcli.Usagef("%s %s: %v", flag, path, unwrapPath(err))
	}
	if !fi.Mode().IsRegular() {
		return nil, kitcli.Usagef("%s %s: not a file", flag, path)
	}
	if private && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return nil, kitcli.Usagef("%s %s is readable by others; chmod 600 it", flag, path)
	}
	if fi.Size() > maxKeyFile {
		return nil, kitcli.Usagef("%s %s: too big for a key", flag, path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the key file the user named
	if err != nil {
		return nil, kitcli.Usagef("%s %s: %v", flag, path, unwrapPath(err))
	}
	return data, nil
}

// unwrapPath drops the path an *os.PathError repeats, since the
// message names the file already.
func unwrapPath(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// KeyID names a public key: the first 16 hex digits of the SHA-256 of
// its PKIX DER form, which `openssl pkey -in team.key -pubout -outform
// DER | sha256sum` prints in full.
func KeyID(pub ed25519.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		// An ed25519.PublicKey always marshals; fall back to the raw key.
		der = pub
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])[:16]
}

// sign writes dir's consent.json.sig for data, the bytes of
// consent.json just written.
func sign(dir string, key ed25519.PrivateKey, data []byte) error {
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return errors.New("--sign-key: not an ed25519 key")
	}
	s := Signature{Algorithm: SignatureAlgorithm, KeyID: KeyID(pub), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := cache.WriteFile(filepath.Join(dir, SignatureFile), append(out, '\n')); err != nil {
		return fmt.Errorf("writing %s: %w", SignatureFile, err)
	}
	return nil
}

// checkSignature reports whether sig (the bytes of consent.json.sig) is
// a good signature by pub over data.
func checkSignature(sig []byte, pub ed25519.PublicKey, data []byte) bool {
	var s Signature
	if err := json.Unmarshal(sig, &s); err != nil || s.Algorithm != SignatureAlgorithm {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, data, raw)
}
