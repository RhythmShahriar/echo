// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/RhythmShahriar/echo/model"
)

// TestListedMessageFitsInAHundredColumns: an ls line stays under 100
// columns however long the kind, recipient and subject are.
func TestListedMessageFitsInAHundredColumns(t *testing.T) {
	ms := []model.Message{
		{ID: "a25f106901fb", Channel: model.Email, Status: model.Faked, At: time.Now(),
			To:      []string{"a.very.long.recipient.name@a-long-customer-domain.example"},
			Subject: "Your order W-1001 has shipped and is on its way to the address you gave us"},
		{ID: "4b68753c69c0", Channel: model.Webhook, Provider: "woocommerce", Status: model.Blocked, At: time.Now(),
			Method: "POST", URL: "https://hooks.a-long-integration-platform.example/catch/123456/abcdef", Event: "order.created"},
	}
	for _, plain := range []bool{false, true} {
		var buf bytes.Buffer
		p := NewPretty(&buf, &buf, PrettyOptions{Plain: plain})
		for _, m := range ms {
			p.ListedMessage(m.ID, m)
		}
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			if !strings.HasPrefix(line, "a25f106901fb  ") && !strings.HasPrefix(line, "4b68753c69c0  ") {
				t.Errorf("plain %v: the line doesn't start with the ID: %q", plain, line)
			}
			if w := lipgloss.Width(line); !plain && w >= 100 {
				t.Errorf("a line is %d columns: %q", w, line)
			}
		}
	}
}
