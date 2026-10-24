// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

// runEnv carries the run value to the commands the runner starts. It is
// wire.RunEnv from connectors K0 on.
const runEnv = "RHYTHM_ECHO_RUN"

// signRun returns the X-Echo-Run value for run and action: signed as
// "<run>/<action>.<sig>" when token is set, so a connector that knows the
// token can tell Echo's runner from a visitor who sends the header to
// poison a snapshot. sig is the first 16 bytes of HMAC-SHA256(token,
// "<run>/<action>") in unpadded base64url. It is a copy of wire.SignRun
// (connectors K0), kept here until that merges; the K0 test vector in
// sign_test.go keeps the two the same.
func signRun(token, run, action string) string {
	v := run + "/" + action
	if token == "" {
		return v
	}
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(v))
	return v + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}
