// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/consent"
)

// worldName names Echo's isolated world in every frame. Scripts there
// see the page's DOM but none of its JavaScript, so a page that patches
// querySelector or JSON can't change what they find.
const worldName = "rhythm-echo"

// maxEvalResult caps what one isolated or main-world script may return
// (the Tab contract): a hostile page can't make Echo hold a huge answer.
const maxEvalResult = 256 << 10

// errResultTooLarge is a script result past maxEvalResult.
var errResultTooLarge = fmt.Errorf("script result larger than %d KiB", maxEvalResult>>10)

// frame is one frame of the run's tab: in the tab's own process or in
// an out-of-process frame's target, whose session runs its scripts.
type frame struct {
	id     proto.PageFrameID
	parent proto.PageFrameID
	url    string
	s      cdp
}

// frames lists the tab's frames, top first: the tab's frame tree plus
// the tree of each out-of-process frame target. A frame both trees name
// is taken from its own target, where its document lives.
func (s *session) frames(ctx context.Context) ([]frame, error) {
	main := s.main
	main.ctx = ctx
	var out []frame
	index := map[proto.PageFrameID]int{}
	add := func(f frame) {
		if i, ok := index[f.id]; ok {
			if f.parent == "" {
				f.parent = out[i].parent
			}
			out[i] = f
			return
		}
		index[f.id] = len(out)
		out = append(out, f)
	}
	var walk func(t *proto.PageFrameTree, c cdp)
	walk = func(t *proto.PageFrameTree, c cdp) {
		if t == nil || t.Frame == nil {
			return
		}
		add(frame{id: t.Frame.ID, parent: t.Frame.ParentID, url: t.Frame.URL, s: c})
		for _, ch := range t.ChildFrames {
			walk(ch, c)
		}
	}
	tree, err := proto.PageGetFrameTree{}.Call(main)
	if err != nil {
		return nil, err
	}
	walk(tree.FrameTree, main)
	s.rec.mu.Lock()
	var oop []cdp
	for _, t := range s.rec.targets {
		if t.kind == kindFrame {
			c := t.s
			c.ctx = ctx
			oop = append(oop, c)
		}
	}
	s.rec.mu.Unlock()
	for _, c := range oop {
		if t, err := (proto.PageGetFrameTree{}).Call(c); err == nil {
			walk(t.FrameTree, c)
		}
	}
	return out, nil
}

// world returns the id of Echo's isolated world in f, making it once
// per document: the recorder forgets it when the frame navigates.
func (s *session) world(f frame) (proto.RuntimeExecutionContextID, error) {
	s.rec.mu.Lock()
	id, ok := s.rec.worlds[f.id]
	s.rec.mu.Unlock()
	if ok {
		return id, nil
	}
	res, err := proto.PageCreateIsolatedWorld{FrameID: f.id, WorldName: worldName}.Call(f.s)
	if err != nil {
		return 0, err
	}
	s.rec.mu.Lock()
	s.rec.worlds[f.id] = res.ExecutionContextID
	s.rec.mu.Unlock()
	return res.ExecutionContextID, nil
}

// forgetWorld drops a world that is gone (its document was replaced).
func (s *session) forgetWorld(id proto.PageFrameID) {
	s.rec.mu.Lock()
	delete(s.rec.worlds, id)
	s.rec.mu.Unlock()
}

// staleContext is Chrome's answer for a world whose document is gone.
func staleContext(err error) bool {
	m := err.Error()
	return strings.Contains(m, "Cannot find context") || strings.Contains(m, "context was destroyed") ||
		strings.Contains(m, "Cannot find execution context")
}

// isolatedJS wraps a function expression so it runs with args and
// returns the JSON of its (awaited) result as a string, refusing one
// larger than maxEvalResult before it leaves the page.
func isolatedJS(fn string, args []any) (string, error) {
	if args == nil {
		args = []any{}
	}
	a, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("script arguments: %w", err)
	}
	return cappedJS("(\n" + fn + "\n)(..." + string(a) + ")"), nil
}

// cappedJS wraps an expression so the page awaits it and returns the
// JSON of its result as a string, refusing one larger than
// maxEvalResult there: Echo never receives a huge answer over CDP. The
// newlines end a trailing line comment in expr.
func cappedJS(expr string) string {
	return "(async () => {\nconst __r = await (\n" + expr + "\n);\n" +
		"const __s = JSON.stringify(__r === undefined ? null : __r);\n" +
		"if (__s !== undefined && __s.length > " + fmt.Sprint(maxEvalResult) + ") throw new Error(\"result too large\");\n" +
		"return __s === undefined ? \"null\" : __s;\n})()"
}

// cappedResult is the JSON a cappedJS script returned. The Go side
// checks the size and the JSON again: in the main world the page can
// replace JSON.stringify.
func cappedResult(res *proto.RuntimeEvaluateResult) (json.RawMessage, error) {
	if res.Result == nil {
		return json.RawMessage("null"), nil
	}
	if res.Result.Type != proto.RuntimeRemoteObjectTypeString {
		return nil, errors.New("script returned something that isn't JSON")
	}
	str := res.Result.Value.Str()
	if len(str) > maxEvalResult {
		return nil, errResultTooLarge
	}
	if !json.Valid([]byte(str)) {
		return nil, errors.New("script returned something that isn't JSON")
	}
	return json.RawMessage(str), nil
}

// evalIsolated runs a function expression with args in f's isolated
// world. A world whose document went away is made again once.
func (s *session) evalIsolated(ctx context.Context, f frame, fn string, args ...any) (json.RawMessage, error) {
	expr, err := isolatedJS(fn, args)
	if err != nil {
		return nil, err
	}
	c := f.s
	c.ctx = ctx
	f.s = c
	for attempt := 0; ; attempt++ {
		id, err := s.world(f)
		if err != nil {
			return nil, err
		}
		res, err := proto.RuntimeEvaluate{Expression: expr, ContextID: id, ReturnByValue: true, AwaitPromise: true}.Call(c)
		if err != nil {
			if attempt == 0 && staleContext(err) {
				s.forgetWorld(f.id)
				continue
			}
			return nil, err
		}
		if res.ExceptionDetails != nil {
			msg := exceptionText(res.ExceptionDetails)
			if attempt == 0 && strings.Contains(msg, "context was destroyed") {
				s.forgetWorld(f.id)
				continue
			}
			if strings.Contains(msg, "result too large") {
				return nil, errResultTooLarge
			}
			return nil, fmt.Errorf("script failed: %s", consent.SafeText(msg, 200))
		}
		return cappedResult(res)
	}
}

// exceptionText is the message of a script's exception.
func exceptionText(d *proto.RuntimeExceptionDetails) string {
	if d.Exception != nil && d.Exception.Description != "" {
		first, _, _ := strings.Cut(d.Exception.Description, "\n")
		return first
	}
	return d.Text
}
