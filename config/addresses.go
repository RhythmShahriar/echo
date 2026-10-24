// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

// SetAddresses rewrites the listen, smtp and proxy keys of the config file
// at path, for `up` when Echo's default ports are taken (by Mailpit, say).
// An empty value leaves that key alone. Only the old values change: every
// comment, blank line and other key stays byte for byte, because
// re-encoding the YAML would reformat a file the user wrote by hand. A key
// that isn't there is added at the end; a missing file is created.
// The result is loaded before it's written, so a rewrite that would break
// the file fails instead. A symlinked config is rewritten where the link
// points, so the link stays.
func SetAddresses(path, listen, smtp, proxy string) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	data, err := os.ReadFile(path) //nolint:gosec // the user's own project config
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	for _, kv := range [][2]string{{"listen", listen}, {"smtp", smtp}, {"proxy", proxy}} {
		if kv[1] == "" {
			continue
		}
		if data, err = setKey(data, kv[0], kv[1]); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	c, err := parse(data)
	if err != nil {
		return fmt.Errorf("rewriting %s would break it: %w", path, err)
	}
	for _, kv := range [][3]string{{"listen", listen, c.Listen}, {"smtp", smtp, c.SMTP}, {"proxy", proxy, c.Proxy}} {
		if kv[1] != "" && kv[1] != kv[2] {
			return fmt.Errorf("rewriting %s: %s reads back as %q, not %q", path, kv[0], kv[2], kv[1])
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// setKey replaces the value of a top-level key in data, keeping the rest
// of the line (spacing and any trailing comment).
func setKey(data []byte, name, value string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	var top *yaml.Node
	if len(root.Content) > 0 {
		top = root.Content[0]
	}
	if top == nil || (top.Kind == yaml.ScalarNode && top.Tag == "!!null") {
		return appendKey(data, name, value), nil
	}
	if top.Kind != yaml.MappingNode || top.Style&yaml.FlowStyle != 0 {
		return nil, errors.New("can't rewrite a config that isn't a block mapping of keys")
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i], top.Content[i+1]
		if k.Value != name {
			continue
		}
		if v.Kind != yaml.ScalarNode || v.Line != k.Line || v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return nil, fmt.Errorf("line %d: %s must be a single value on its own line to be rewritten", k.Line, name)
		}
		start := lineStart(data, k.Line)
		end := bytes.IndexByte(data[start:], '\n')
		if end < 0 {
			end = len(data) - start
		}
		line := data[start : start+end]
		from, to, ok := valueSpan(line, k.Column-1)
		if !ok {
			return nil, fmt.Errorf("line %d: can't find the value of %s", k.Line, name)
		}
		val := quote(value)
		if from == to && line[from-1] == ':' {
			val = " " + val // `listen:` alone
		}
		if from == to && to < len(line) && line[to] == '#' {
			val += " " // `listen: # comment` keeps a space before the comment
		}
		out := make([]byte, 0, len(data)+len(val))
		out = append(out, data[:start+from]...)
		out = append(out, val...)
		return append(out, data[start+to:]...), nil
	}
	return appendKey(data, name, value), nil
}

func appendKey(data []byte, name, value string) []byte {
	out := bytes.Clone(data)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, name+": "+quote(value)+"\n"...)
}

// lineStart is the offset of 1-based line n.
func lineStart(data []byte, n int) int {
	off := 0
	for range n - 1 {
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			return len(data)
		}
		off += i + 1
	}
	return off
}

// valueSpan finds the value after the key that starts at col: from the
// first character after "key: " to the end of the value, before spaces
// and a comment. An empty value gives from == to.
func valueSpan(line []byte, col int) (from, to int, ok bool) {
	i := col
	if i >= len(line) {
		return 0, 0, false
	}
	switch line[i] { // the key itself may be quoted
	case '"', '\'':
		j, ok := quotedEnd(line, i)
		if !ok {
			return 0, 0, false
		}
		i = j
	default:
		for i < len(line) && line[i] != ':' {
			i++
		}
	}
	for i < len(line) && line[i] != ':' {
		i++
	}
	if i >= len(line) {
		return 0, 0, false
	}
	i++ // past ':'
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	from = i
	if i >= len(line) || line[i] == '#' {
		return from, from, true
	}
	if line[i] == '"' || line[i] == '\'' {
		j, ok := quotedEnd(line, i)
		return from, j, ok
	}
	to = len(line)
	for j := i; j < len(line); j++ {
		if line[j] == '#' && (line[j-1] == ' ' || line[j-1] == '\t') {
			to = j
			break
		}
	}
	for to > from && (line[to-1] == ' ' || line[to-1] == '\t' || line[to-1] == '\r') {
		to--
	}
	return from, to, true
}

// quotedEnd returns the offset just past the quoted scalar starting at i.
func quotedEnd(line []byte, i int) (int, bool) {
	q := line[i]
	for j := i + 1; j < len(line); j++ {
		switch {
		case q == '"' && line[j] == '\\':
			j++
		case line[j] == q && q == '\'' && j+1 < len(line) && line[j+1] == '\'':
			j++ // '' is an escaped quote
		case line[j] == q:
			return j + 1, true
		}
	}
	return 0, false
}

var plain = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// quote writes an address as a YAML scalar: bare when that reads back as
// the same string (127.0.0.1:18025), quoted otherwise ([::1]:1025).
func quote(v string) string {
	if plain.MatchString(v) && !slices.Contains([]string{"true", "false", "null", "Null", "NULL", "True", "False", "TRUE", "FALSE"}, v) {
		return v
	}
	return strconv.Quote(v)
}
