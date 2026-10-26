// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// probe: for each target locator, whether it exists, is visible, is a
// kind of control a visitor clicks, is top-most and sits inside the
// container (the banner) when one is given. Nothing is clicked.
const out = {};
const within = input.within ? find(input.within) : null;
for (const t of (input.targets || []).slice(0, MAX)) {
  const el = find(t.loc);
  if (!el) {
    out[t.key] = { found: false };
    continue;
  }
  out[t.key] = {
    found: true,
    visible: visible(el),
    kind: kindOK(el),
    offsite: offsite(el),
    disabled: disabled(el),
    inside: input.within ? !!within && inside(el, within) : true,
    top: topMost(el),
    text: label(el),
    box: box(el),
  };
}
return out;
