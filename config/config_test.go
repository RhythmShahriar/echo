// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTemplateLoadsAsDefaults(t *testing.T) {
	c, err := Load(write(t, Template))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.Mode != d.Mode || c.Listen != d.Listen || c.SMTP != d.SMTP || c.Quiet != d.Quiet || c.Timeout != d.Timeout || len(c.Actions) != 0 {
		t.Errorf("template differs from defaults: %+v", c)
	}
}

func TestMissingAndEmpty(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err != nil {
		t.Error(err)
	}
	if _, err := Load(write(t, "")); err != nil {
		t.Error(err)
	}
}

func TestErrors(t *testing.T) {
	for in, want := range map[string]string{
		"mode: loud\n":                     "mode",
		"modee: seal\n":                    "modee",
		"quiet: soon\n":                    "duration",
		"actions: [{name: a}]\n":           "exactly one",
		"actions: [{http: {url: /x}}]\n":   "needs a name",
		"actions: [{name: a, http: {}}]\n": "needs a url",
		"actions: [{name: a, command: x}, {name: a, command: y}]\n": "twice",
	} {
		_, err := Load(write(t, in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}
}

func TestIsProduction(t *testing.T) {
	c := Config{Production: []string{"https://shop.com"}}
	if !c.IsProduction("https://shop.com/checkout") || c.IsProduction("https://staging.shop.com/") {
		t.Error("IsProduction")
	}
}
