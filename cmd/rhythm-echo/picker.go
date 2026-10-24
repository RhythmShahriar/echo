package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/config"
	"github.com/RhythmShahriar/echo/report"
	"github.com/RhythmShahriar/echo/store"
)

// pickState is what the picker offers, from this folder and a quick look
// for a running Echo.
type pickState struct {
	running    bool   // an Echo answers at the configured URL
	hasConfig  bool   // .echo/config.yaml exists
	hasActions bool   // and lists actions
	snapshot   string // the latest snapshot, described; "" when none
	hasResult  bool   // a run or snapshot to report
}

func loadPickState() pickState {
	var ps pickState
	_, err := os.Stat(config.Path())
	ps.hasConfig = err == nil
	cfg, err := config.Load(config.Path())
	ps.hasActions = err == nil && len(cfg.Actions) > 0
	st := store.Store{Root: config.Dir}
	if dir, err := st.Snapshot(""); err == nil {
		if r, err := store.LoadRun(dir); err == nil {
			ps.snapshot = fmt.Sprintf("%q, %s", r.Name, report.Ago(r.Started))
		}
	}
	_, err = st.LatestRun()
	ps.hasResult = err == nil
	url := cmp.Or(os.Getenv("RHYTHM_ECHO_URL"), "http://"+cfg.Listen)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = client.New(url, os.Getenv("RHYTHM_ECHO_TOKEN")).Health(ctx)
	ps.running = err == nil
	return ps
}

// pickOptions lists what to do, the likeliest first.
func pickOptions(ps pickState) []huh.Option[string] {
	var opts []huh.Option[string]
	add := func(label, value string) { opts = append(opts, huh.NewOption(label, value)) }
	if ps.running {
		add("Watch messages as they arrive", "watch")
	} else {
		add("Start Echo: seal this machine and catch what sites send", "up")
	}
	if ps.hasActions && ps.snapshot != "" {
		add("Compare with the latest snapshot ("+ps.snapshot+")", "compare")
	}
	if ps.hasActions {
		add("Take a snapshot: record what each action sends now", "snapshot")
		add("Run the actions once, without comparing", "run")
	}
	if ps.hasResult {
		add("Show the last result again", "report")
	}
	add("Connect a site (WordPress, Laravel, Next.js, Node, anything)", "install")
	add("Check that every capture layer works (doctor)", "doctor")
	if !ps.hasConfig {
		add("Write .echo/config.yaml to add actions (init)", "init")
	}
	return opts
}

// pick asks what to do, then runs it.
func pick(stdin io.Reader, stdout, stderr io.Writer) int {
	ps := loadPickState()
	opts := pickOptions(ps)
	action := opts[0].Value
	stack, site := "wordpress", "."
	open := true
	form := huh.NewForm(
		huh.NewGroup(huh.NewSelect[string]().Title("What should Echo do?").Options(opts...).Value(&action)),
		huh.NewGroup(huh.NewSelect[string]().Title("Which stack?").Options(
			huh.NewOption("WordPress: installs a mu-plugin", "wordpress"),
			huh.NewOption("Laravel", "laravel"),
			huh.NewOption("Next.js", "next"),
			huh.NewOption("Node (Express, Fastify…)", "node"),
			huh.NewOption("Anything else: SMTP, sendmail and the proxy", "generic"),
		).Value(&stack)).WithHideFunc(func() bool { return action != "install" }),
		huh.NewGroup(huh.NewInput().
			Title("Where is the site?").
			Description("The WordPress folder (with wp-content), or the Next.js project. Only sites you own or are authorised to test.").
			Value(&site).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("enter a folder, e.g. . or /var/www/staging")
				}
				return nil
			}),
		).WithHideFunc(func() bool { return action != "install" || (stack != "wordpress" && stack != "next") }),
		huh.NewGroup(huh.NewConfirm().Title("Open the HTML report when done?").Value(&open)).
			WithHideFunc(func() bool {
				return action != "compare" && action != "snapshot" && action != "run" && action != "report"
			}),
	).WithTheme(huh.ThemeCharm())
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return exitCanceled
		}
		fmt.Fprintln(stderr, "rhythm echo:", err)
		return exitError
	}
	return run(pickArgs(action, stack, site, open), stdin, stdout, stderr)
}

// pickArgs is the command line the picker's answers stand for.
func pickArgs(action, stack, site string, open bool) []string {
	args := []string{action}
	switch action {
	case "install":
		args = append(args, stack)
		if stack == "wordpress" || stack == "next" {
			args = append(args, strings.TrimSpace(site))
		}
	case "compare", "snapshot", "run", "report":
		if open {
			args = append(args, "--open")
		}
	}
	return args
}
