// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package codes

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/model"
)

// BenchmarkExtractLarge reads a message far over what Extract scans.
func BenchmarkExtractLarge(b *testing.B) {
	line := "Hello Jane, your order #42 has shipped and is on its way to you. Your code is in the app.\n"
	m := model.Message{Text: strings.Repeat(line, (20<<20)/len(line))}
	for b.Loop() {
		Extract(m)
	}
}

// BenchmarkExtractLargeHTML reads a large HTML newsletter.
func BenchmarkExtractLargeHTML(b *testing.B) {
	row := `<tr><td style="padding:4px"><a href="https://shop.test/p/42?utm_source=mail">Shoes</a> for $42.00, code below</td></tr>` + "\n"
	m := model.Message{HTML: strings.Repeat(row, (20<<20)/len(row))}
	for b.Loop() {
		Extract(m)
	}
}
