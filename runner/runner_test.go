package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
)

// site acts like a WordPress site with Echo's connector: the contact form
// handler emails the admin and the submitter and posts to Zapier; when
// broken, the admin email is lost (a mail plugin update gone wrong).
func site(t *testing.T, echo *client.Client, broken *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := r.Header.Get(wire.RunHeader)
		_ = r.ParseForm()
		send := func(c wire.Capture) {
			c.Run, c.Source = run, wire.Source{Via: "wordpress", Host: r.Host}
			if _, err := echo.Capture(context.Background(), c); err != nil {
				t.Error(err)
			}
		}
		if !broken.Load() {
			send(wire.Capture{Kind: "email", Email: &wire.Email{From: "wordpress@shop.test", To: []string{"admin@shop.test"},
				Subject: "New message from " + r.Form.Get("name"), Text: "Message: " + r.Form.Get("message")}})
		}
		send(wire.Capture{Kind: "email", Email: &wire.Email{From: "wordpress@shop.test", To: []string{r.Form.Get("email")},
			Subject: "Thanks for your message", Text: "We'll get back to you."}})
		go func() { // async, like Action Scheduler: arrives after the response
			time.Sleep(200 * time.Millisecond)
			send(wire.Capture{Kind: "http", Phase: wire.Before, HTTP: &wire.HTTP{Method: "POST",
				URL: "https://hooks.zapier.com/hooks/catch/1/x/", Headers: map[string]string{"Content-Type": "application/json"},
				Body: `{"name":"x","email":"y","message":"z"}`}})
		}()
		fmt.Fprint(w, "ok")
	}))
}

func TestSnapshotThenCompare(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Version: "test"}))
	defer srv.Close()
	echo := client.New(srv.URL, "")
	var broken atomic.Bool
	shop := site(t, echo, &broken)
	defer shop.Close()

	cfg := config.Default()
	cfg.Site = shop.URL
	cfg.Quiet = config.Duration{Duration: 500 * time.Millisecond}
	cfg.Timeout = config.Duration{Duration: 5 * time.Second}
	cfg.Actions = []config.Action{
		{Name: "Contact form", HTTP: &config.HTTPAction{URL: "/contact", Form: map[string]string{
			"name": "${fake:name}", "email": "${fake:email}", "message": "Hello"}}, Expect: &config.Expect{Email: 2}},
		{Name: "Nightly report", Command: `curl -s -X POST ` + srv.URL + `/v1/capture -d "{\"v\":1,\"kind\":\"email\",\"run\":\"$RHYTHM_ECHO_RUN\",\"source\":{\"via\":\"test\"},\"email\":{\"to\":[\"ops@shop.test\"],\"subject\":\"Nightly report\",\"text\":\"All good\"}}" >/dev/null`},
	}
	ctx := context.Background()
	before, err := Run(ctx, Options{Config: cfg, Client: echo, Version: "test", Name: "good"})
	if err != nil {
		t.Fatal(err)
	}
	form := before.Action("action:contact-form")
	if form == nil || len(form.Messages) != 3 || form.Error != "" {
		t.Fatalf("contact form: %+v", form)
	}
	if n := before.Action("action:nightly-report"); n == nil || len(n.Messages) != 1 {
		t.Fatalf("command action: %+v", n)
	}
	for _, a := range before.Actions {
		for _, i := range a.Issues {
			t.Errorf("good state has an issue: %s", i.Title)
		}
	}

	broken.Store(true)
	after, err := Run(ctx, Options{Config: cfg, Client: echo, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	c := compare.Compare(before, after, "", "", "test")
	var got []string
	for _, ch := range c.Changes {
		got = append(got, ch.Class+": "+ch.Title)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"broke: Email to admin@shop.test is no longer sent", "broke: Sent fewer emails than expected"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if c.Count(model.New) != 0 || c.Count(model.Changed) != 0 {
		t.Errorf("only the lost email should change:\n%s", all)
	}
}

func TestProductionRefused(t *testing.T) {
	srv := httptest.NewServer(catcher.NewServer(catcher.NewInbox(10), catcher.Config{}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Site, cfg.Production = "https://shop.com", []string{"https://shop.com"}
	cfg.Quiet, cfg.Timeout = config.Duration{Duration: 100 * time.Millisecond}, config.Duration{Duration: time.Second}
	cfg.Actions = []config.Action{{Name: "Order", HTTP: &config.HTTPAction{URL: "/checkout"}}}
	run, err := Run(context.Background(), Options{Config: cfg, Client: client.New(srv.URL, "")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run.Actions[0].Error, "production") {
		t.Errorf("error = %q", run.Actions[0].Error)
	}
}

func TestSelect(t *testing.T) {
	if _, err := Select(config.Config{}, nil); !errors.Is(err, ErrNoActions) {
		t.Errorf("no actions: %v", err)
	}
	cfg := config.Config{Actions: []config.Action{{Name: "Contact form"}, {Name: "Checkout"}, {Name: "Reset"}}}
	got, err := Select(cfg, []string{"reset", "contact FORM"})
	if err != nil || len(got) != 2 || got[0].Name != "Contact form" || got[1].Name != "Reset" {
		t.Errorf("Select = %v, %v", got, err)
	}
	if _, err := Select(cfg, []string{"Checkout", "nope"}); !errors.Is(err, ErrUnknownAction) || !strings.Contains(err.Error(), `"nope"`) ||
		!strings.Contains(err.Error(), "Contact form, Checkout, Reset") {
		t.Errorf("unknown action: %v", err)
	}
}
