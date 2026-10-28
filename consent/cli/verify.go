// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/echo/consent"
	"github.com/RhythmShahriar/echo/consent/evidence"
	"github.com/RhythmShahriar/echo/report"
)

// msgSigstore points at the Action's keyless signature, which this
// binary doesn't check: cosign does, against the workflow's identity.
const msgSigstore = "Sigstore bundle present: check it with cosign verify-blob --bundle consent.json.sigstore.json " +
	"--certificate-identity-regexp '^https://github.com/<owner>/<repo>/' " +
	"--certificate-oidc-issuer https://token.actions.githubusercontent.com consent.json"

// signatureScope says what a good signature vouches for: consent.json
// and the attachments it lists by hash. The reports and manifest.json
// are written after it, so only their manifest hashes check them and
// nothing checks the manifest: whoever rewrites a report can rewrite
// the manifest too. It names the reports the folder has.
func signatureScope(folder string) string {
	var reports []string
	for _, name := range []string{reportHTML, reportMD, reportJUnit} {
		if _, err := os.Lstat(filepath.Join(folder, name)); err == nil {
			reports = append(reports, name)
		}
	}
	s := "The signature covers consent.json and the files it lists; "
	switch len(reports) {
	case 0:
		return s + "manifest.json is not signed."
	case 1:
		return s + reports[0] + " is checked against manifest.json, which is not signed."
	}
	return s + strings.Join(reports[:len(reports)-1], ", ") + " and " + reports[len(reports)-1] +
		" are checked against manifest.json, which is not signed."
}

// verify is rhythm echo consent verify <folder|file> [--key <file>]:
// it checks an evidence folder's hashes and signature and exits 0 when
// everything matches, 1 when a hash or the signature doesn't and 3
// when a file is missing or the folder can't be read.
func (c *cmd) verify(pos []string) int {
	env := c.env
	if len(pos) != 1 {
		return fail(env, usagef("consent verify needs one evidence folder: rhythm echo consent verify .echo/consent/<id> [--key team.pub]"))
	}
	v, err := evidence.Verify(pos[0], c.o.key)
	if err != nil {
		var ue *evidence.UnreadableError
		if errors.As(err, &ue) {
			fmt.Fprintln(env.Stderr, "rhythm echo:", ue.Msg)
			return exitUnavailable
		}
		return fail(env, err)
	}
	v.Folder = shown(evidence.Folder(pos[0]))
	view := env.Stdout
	if c.o.json {
		view = env.Stderr
	}
	if err := writers.verify(env.Stdout, v, c.o.json, c.textOptions(env.Stdout)); err != nil {
		fmt.Fprintln(env.Stderr, "rhythm echo: printing the result:", err)
	}
	for _, rel := range evidence.Unlisted(pos[0], v) {
		fmt.Fprintln(view, "not in the manifest  "+consent.SafeText(rel, 200))
	}
	if v.Signature.Key != "" && v.Signature.OK && !c.o.quiet {
		fmt.Fprintln(view, signatureScope(evidence.Folder(pos[0])))
	}
	if _, err := os.Lstat(filepath.Join(evidence.Folder(pos[0]), evidence.SigstoreFile)); err == nil && !c.o.quiet {
		fmt.Fprintln(view, msgSigstore)
	}
	return verifyExit(v, c.o.key != "")
}

// verifyExit is consent verify's exit code: 1 for a file whose hash
// differs or a signature that doesn't check (with --key, a missing
// one too), which wins over 3 for a file that is missing or can't be
// read, as findings win over "couldn't check" in the check itself.
func verifyExit(v *report.ConsentVerify, keyGiven bool) int {
	changed, missing, noResult := false, false, false
	for _, f := range v.Files {
		switch {
		case f.Got == "":
			missing = true
			noResult = noResult || f.Path == evidence.ResultFile
		case !f.OK:
			changed = true
		}
	}
	// Without consent.json there is nothing to check a signature over:
	// that is the missing file's exit, not a bad signature.
	if keyGiven && !v.Signature.OK && !noResult {
		changed = true
	}
	switch {
	case changed:
		return exitFindings
	case missing:
		return exitUnavailable
	}
	return exitOK
}
