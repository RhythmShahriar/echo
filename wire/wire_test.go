// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/json"
	"os"
	"testing"
)

// runVector is a fixed signature the PHP and Node ports must reproduce:
// HMAC-SHA256("secret", "r1/checkout"), first 16 bytes, unpadded base64url.
const runVector = "r1/checkout.NhOp3HY0JQ76wSoPnn3sZg"

func TestSignRunVector(t *testing.T) {
	if got := SignRun("secret", "r1", "checkout"); got != runVector {
		t.Fatalf("SignRun = %q, want %q", got, runVector)
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	for _, tc := range []struct{ run, action string }{
		{"r1", "checkout"},
		{"20261024-0915", "order.paid"},
		{"r2", ""},
		{"r3", "a/b"},
	} {
		v := SignRun("tok", tc.run, tc.action)
		run, action, ok := VerifyRun("tok", v)
		if !ok || run != tc.run || action != tc.action {
			t.Errorf("VerifyRun(%q) = %q, %q, %v; want %q, %q, true", v, run, action, ok, tc.run, tc.action)
		}
	}
}

func TestVerifyRunRejectsTampered(t *testing.T) {
	v := SignRun("tok", "r1", "checkout")
	for name, bad := range map[string]string{
		"action changed": "r1/refund" + v[len("r1/checkout"):],
		"run changed":    "r2" + v[2:],
		"sig changed":    v[:len(v)-1] + "A",
		"wrong token":    SignRun("other", "r1", "checkout"),
	} {
		if bad == v {
			t.Fatalf("%s: tampered value equals the original", name)
		}
		if run, action, ok := VerifyRun("tok", bad); ok || run != "" || action != "" {
			t.Errorf("%s: VerifyRun(%q) = %q, %q, %v; want nothing", name, bad, run, action, ok)
		}
	}
	// A signed value cannot be checked without the token, so it is refused
	// rather than taken as unsigned.
	if _, _, ok := VerifyRun("", v); ok {
		t.Error("a signed value verified without a token")
	}
}

func TestVerifyRunUnsigned(t *testing.T) {
	for _, tc := range []struct{ value, run, action string }{
		{"r1/checkout", "r1", "checkout"},
		{"r1/order.paid", "r1", "order.paid"},
		{"r1/", "r1", ""},
	} {
		run, action, ok := VerifyRun("tok", tc.value)
		if ok || run != tc.run || action != tc.action {
			t.Errorf("VerifyRun(%q) = %q, %q, %v; want %q, %q, false", tc.value, run, action, ok, tc.run, tc.action)
		}
	}
	for _, bad := range []string{"", "r1", "/checkout"} {
		if run, action, ok := VerifyRun("tok", bad); ok || run != "" || action != "" {
			t.Errorf("VerifyRun(%q) = %q, %q, %v; want nothing", bad, run, action, ok)
		}
	}
}

func TestSignRunWithoutToken(t *testing.T) {
	if got := SignRun("", "r1", "checkout"); got != "r1/checkout" {
		t.Fatalf("SignRun without a token = %q, want it unsigned", got)
	}
}

// A v1 capture that uses none of the new optional fields keeps its exact
// JSON, so older catchers and connectors are unaffected.
func TestCaptureV1Unchanged(t *testing.T) {
	c := Capture{
		Version: 1,
		Kind:    "email",
		Run:     "r1/checkout",
		Source:  Source{Via: "wordpress", Stack: "wordpress", Hook: "wp_mail", Host: "shop.test"},
		Email:   &Email{From: "a@shop.test", To: []string{"b@example.com"}, Subject: "Hi", Text: "Hello"},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"kind":"email","run":"r1/checkout","source":{"via":"wordpress","stack":"wordpress","hook":"wp_mail","host":"shop.test"},"email":{"from":"a@shop.test","to":["b@example.com"],"subject":"Hi","text":"Hello"}}`
	if string(b) != want {
		t.Fatalf("marshal =\n%s\nwant\n%s", b, want)
	}
	var back Capture
	if err := json.Unmarshal([]byte(want), &back); err != nil {
		t.Fatal(err)
	}
	if back.Source.Site != "" || back.Email.Attachments != nil {
		t.Fatalf("a v1 capture decoded new fields: %+v", back)
	}
}

func TestCaptureNewFields(t *testing.T) {
	in := `{"v":1,"kind":"email","source":{"via":"node","site":"client-a"},"email":{"attachments":[{"name":"invoice-42.pdf","type":"application/pdf","size":86016,"sha256":"ab"}]}}`
	var c Capture
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	if c.Source.Site != "client-a" {
		t.Errorf("site = %q", c.Source.Site)
	}
	want := Attachment{Name: "invoice-42.pdf", Type: "application/pdf", Size: 86016, SHA256: "ab"}
	if len(c.Email.Attachments) != 1 || c.Email.Attachments[0] != want {
		t.Errorf("attachments = %+v", c.Email.Attachments)
	}
	b, _ := json.Marshal(c)
	if string(b) != in {
		t.Errorf("round trip =\n%s\nwant\n%s", b, in)
	}
}

// TestVerifyRunConformance checks VerifyRun against the vectors the PHP
// and Node connectors test their ports with.
func TestVerifyRunConformance(t *testing.T) {
	b, err := os.ReadFile("../conformance/testdata/run-signature.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Verify []struct {
			Name, Token, Value, Run, Action string
			OK                              bool
		}
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Verify) == 0 {
		t.Fatal("no verify vectors")
	}
	for _, v := range f.Verify {
		run, action, ok := VerifyRun(v.Token, v.Value)
		if run != v.Run || action != v.Action || ok != v.OK {
			t.Errorf("%s: VerifyRun(%q, %q) = %q, %q, %v; want %q, %q, %v", v.Name, v.Token, v.Value, run, action, ok, v.Run, v.Action, v.OK)
		}
		if v.OK {
			if got := SignRun(v.Token, run, action); got != v.Value {
				t.Errorf("%s: SignRun = %q, want %q", v.Name, got, v.Value)
			}
		}
	}
}
