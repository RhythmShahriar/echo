// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// SpamPort is spamd's port, used when an address has none.
	SpamPort = "783"
	// SpamTimeout bounds one check when the context has no deadline, so a
	// spamd that hangs never holds up the inbox.
	SpamTimeout = 10 * time.Second
	// MaxSpamSize is the largest message sent for scoring. spamc skips
	// larger ones by default too, since spamd gets slow on them and spam
	// is rarely that big.
	MaxSpamSize = 500 * 1024
)

// SpamResult is spamd's verdict on one message.
type SpamResult struct {
	// Score is the message's score and Threshold the score from which
	// spamd calls it spam (5.0 unless configured).
	Score     float64 `json:"score"`
	Threshold float64 `json:"threshold"`
	// Spam is whether Score reached Threshold.
	Spam bool `json:"spam"`
	// Rules are the names of the rules that hit, like "HTML_MESSAGE".
	Rules []string `json:"rules"`
}

// Spam asks the SpamAssassin daemon at addr (host:port, port 783 when
// left out) to score the raw message and returns the score and the rules
// that hit. It speaks spamd's own protocol (SYMBOLS SPAMC/1.5), so it needs
// no spamc. See SpamCheck for the threshold as well.
func Spam(ctx context.Context, addr string, raw []byte) (float64, []string, error) {
	r, err := SpamCheck(ctx, addr, raw)
	return r.Score, r.Rules, err
}

// SpamCheck is Spam with the whole verdict. Errors name the address and
// say whether spamd couldn't be reached, didn't answer in time or answered
// with something that isn't a score.
func SpamCheck(ctx context.Context, addr string, raw []byte) (SpamResult, error) {
	addr = spamAddr(addr)
	fail := func(format string, a ...any) (SpamResult, error) {
		return SpamResult{}, fmt.Errorf("spamassassin at %s: "+format, append([]any{addr}, a...)...)
	}
	if len(raw) > MaxSpamSize {
		return fail("the message is %d bytes; spamd only scores messages up to %d KB", len(raw), MaxSpamSize/1024)
	}
	timeout := SpamTimeout
	if d, ok := ctx.Deadline(); ok {
		timeout = time.Until(d)
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// timedOut words a deadline error the same way wherever it happens.
	timedOut := func(err error) bool {
		var ne net.Error
		return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
			(errors.As(err, &ne) && ne.Timeout())
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		switch {
		case timedOut(err):
			return fail("no answer within %s", timeout.Round(time.Millisecond))
		case errors.Is(err, context.Canceled):
			return fail("canceled")
		}
		return fail("couldn't connect: %v", dialReason(err))
	}
	defer func() { _ = conn.Close() }()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	// A canceled context (not only an expired one) must end a read that
	// is waiting on spamd.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	req := fmt.Sprintf("SYMBOLS SPAMC/1.5\r\nContent-length: %d\r\n\r\n", len(raw))
	// A failed write shows up as a failed read below, where spamd may
	// already have said why it stopped reading.
	_, _ = io.WriteString(conn, req)
	_, _ = conn.Write(raw)
	r, perr := readSpamd(bufio.NewReader(io.LimitReader(conn, 1<<20)))
	if perr != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return fail("canceled")
		case timedOut(perr) || ctx.Err() != nil:
			return fail("no answer within %s", timeout.Round(time.Millisecond))
		case errors.Is(perr, io.EOF) || errors.Is(perr, io.ErrUnexpectedEOF):
			return fail("closed the connection without a score")
		}
		return fail("%v", perr)
	}
	return r, nil
}

// spamAddr adds spamd's port to an address without one.
func spamAddr(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(strings.Trim(addr, "[]"), SpamPort)
	}
	return addr
}

// readSpamd reads spamd's answer to SYMBOLS:
//
//	SPAMD/1.1 0 EX_OK
//	Content-length: 32
//	Spam: True ; 15.2 / 5.0
//
//	GTUBE,HTML_MESSAGE,MISSING_DATE
func readSpamd(br *bufio.Reader) (SpamResult, error) {
	status, err := br.ReadString('\n')
	if err != nil {
		return SpamResult{}, err
	}
	f := strings.Fields(status)
	if len(f) < 2 || !strings.HasPrefix(f[0], "SPAMD/") {
		return SpamResult{}, fmt.Errorf("not a spamd answer: %q", strings.TrimSpace(status))
	}
	if f[1] != "0" {
		return SpamResult{}, fmt.Errorf("spamd refused the message: %s", strings.Join(f[1:], " "))
	}
	var r SpamResult
	length, sawSpam := -1, false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return SpamResult{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "content-length":
			if length, err = strconv.Atoi(value); err != nil || length < 0 {
				return SpamResult{}, fmt.Errorf("not a spamd answer: Content-length %q", value)
			}
		case "spam":
			if r, err = parseSpamHeader(value); err != nil {
				return SpamResult{}, err
			}
			sawSpam = true
		}
	}
	if !sawSpam {
		return SpamResult{}, errors.New("not a spamd answer: no Spam header")
	}
	var body []byte
	if length >= 0 {
		body = make([]byte, length)
		if _, err := io.ReadFull(br, body); err != nil {
			return SpamResult{}, err
		}
	} else if body, err = io.ReadAll(br); err != nil {
		return SpamResult{}, err
	}
	for _, rule := range strings.Split(string(body), ",") {
		if rule = strings.TrimSpace(rule); rule != "" {
			r.Rules = append(r.Rules, rule)
		}
	}
	return r, nil
}

// parseSpamHeader reads "True ; 15.2 / 5.0".
func parseSpamHeader(v string) (SpamResult, error) {
	verdict, scores, ok1 := strings.Cut(v, ";")
	score, threshold, ok2 := strings.Cut(scores, "/")
	s, err1 := strconv.ParseFloat(strings.TrimSpace(score), 64)
	t, err2 := strconv.ParseFloat(strings.TrimSpace(threshold), 64)
	if !ok1 || !ok2 || err1 != nil || err2 != nil {
		return SpamResult{}, fmt.Errorf("not a spamd answer: Spam: %q", v)
	}
	isSpam := strings.EqualFold(strings.TrimSpace(verdict), "true") || strings.EqualFold(strings.TrimSpace(verdict), "yes")
	return SpamResult{Score: s, Threshold: t, Spam: isSpam}, nil
}

// dialReason is the useful end of a dial error ("connection refused"),
// without the address the caller already prints.
func dialReason(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err
	}
	return err
}
