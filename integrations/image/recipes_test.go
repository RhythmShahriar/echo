// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package image

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// published is the image the recipes run, by major version.
const published = "ghcr.io/rhythmshahriar/echo:1"

type composeFile struct {
	Services map[string]struct {
		Image       string `yaml:"image"`
		Command     any    `yaml:"command"`
		Environment any    `yaml:"environment"`
		Ports       []string
		Volumes     []string
	} `yaml:"services"`
}

func readYAML(t *testing.T, path string, v any) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), path))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return b
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRecipes checks the Compose recipes run Echo's image the way the
// image expects: the published tag (or an override), the token from the
// environment rather than the command line and the state volume where
// the image keeps the CA.
func TestRecipes(t *testing.T) {
	for _, tc := range []struct {
		path, service, image string
	}{
		{"docs/seal/examples/docker-compose.yml", "echo", "${RHYTHM_ECHO_IMAGE:-" + published + "}"},
		{"integrations/ddev/docker-compose.echo.yaml", "echo", "${ECHO_DOCKER_IMAGE:-" + published + "}"},
		{"integrations/sail/docker-compose.echo.yml", "mailpit", published},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var f composeFile
			raw := readYAML(t, tc.path, &f)
			s, ok := f.Services[tc.service]
			if !ok {
				t.Fatalf("no %s service", tc.service)
			}
			if s.Image != tc.image {
				t.Errorf("image %q, want %q", s.Image, tc.image)
			}
			if strings.Contains(string(raw), "--token") {
				t.Error("passes --token on the command line; use RHYTHM_ECHO_TOKEN")
			}
			for _, v := range s.Volumes {
				if strings.Contains(v, "/root/") {
					t.Errorf("volume %q: the image runs as nonroot", v)
				}
			}
			for _, p := range s.Ports {
				if !strings.HasPrefix(p, "127.0.0.1:") && !strings.HasPrefix(p, `"127.0.0.1:`) {
					t.Errorf("port %q is published beyond loopback", p)
				}
			}
		})
	}
}

// TestDDEVAddon checks install.yaml names files that exist and that each
// carries #ddev-generated, which lets DDEV update and remove them.
func TestDDEVAddon(t *testing.T) {
	var install struct {
		Name         string   `yaml:"name"`
		ProjectFiles []string `yaml:"project_files"`
		Constraint   string   `yaml:"ddev_version_constraint"`
	}
	readYAML(t, "integrations/ddev/install.yaml", &install)
	if install.Name != "echo" {
		t.Errorf("name %q", install.Name)
	}
	if !regexp.MustCompile(`^>= v1\.\d+`).MatchString(install.Constraint) {
		t.Errorf("ddev_version_constraint %q", install.Constraint)
	}
	if len(install.ProjectFiles) == 0 {
		t.Fatal("no project_files")
	}
	for _, f := range install.ProjectFiles {
		b, err := os.ReadFile(filepath.Join(repoRoot(t), "integrations", "ddev", f))
		if err != nil {
			t.Error(err)
			continue
		}
		if !strings.Contains(string(b), "#ddev-generated") {
			t.Errorf("%s has no #ddev-generated line", f)
		}
	}
	var cfg struct {
		MailpitHTTP string   `yaml:"mailpit_http_port"`
		Mailpit     string   `yaml:"mailpit_https_port"`
		Env         []string `yaml:"web_environment"`
	}
	readYAML(t, "integrations/ddev/config.echo.yaml", &cfg)
	if cfg.MailpitHTTP == "8025" || cfg.Mailpit == "8026" || cfg.MailpitHTTP == "" || cfg.Mailpit == "" {
		t.Errorf("Mailpit keeps the ports Echo takes: %q, %q", cfg.MailpitHTTP, cfg.Mailpit)
	}
	for _, want := range []string{"MP_SMTP_RELAY_HOST=echo", "MP_SMTP_RELAY_PORT=1025", "MP_SMTP_RELAY_ALL=true"} {
		if !strings.Contains(strings.Join(cfg.Env, "\n"), want) {
			t.Errorf("web_environment has no %s", want)
		}
	}
	var compose struct {
		Services map[string]struct {
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	readYAML(t, "integrations/ddev/docker-compose.echo.yaml", &compose)
	env := strings.Join(compose.Services["echo"].Environment, "\n")
	for _, want := range []string{"HTTP_EXPOSE=8025:8025", "HTTPS_EXPOSE=8026:8025", "RHYTHM_ECHO_HOSTNAMES=$DDEV_HOSTNAME"} {
		if !strings.Contains(env, want) {
			t.Errorf("the echo service has no %s", want)
		}
	}
}
