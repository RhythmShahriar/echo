// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// scan: which detect selectors of each rule match, whether each rule's
// banner container is visible and which known script URLs the page
// loaded (resource timing and script elements). Page text is never
// read, so a page that only names a CMP never matches.
const out = { rules: {}, scripts: [] };
for (const r of (input.rules || []).slice(0, MAX)) {
  const matched = [];
  for (const s of (r.selectors || []).slice(0, MAX)) {
    if (find(s.loc)) matched.push(s.key);
  }
  out.rules[r.id] = { matched: matched, banner: (r.banners || []).slice(0, MAX).some(shown) };
}
const subs = (input.scripts || []).slice(0, MAX);
if (subs.length) {
  const urls = [];
  try {
    for (const e of performance.getEntriesByType("resource").slice(0, 2000)) urls.push(String(e.name));
  } catch (e) { /* no resource timing */ }
  for (const s of Array.from(document.scripts).slice(0, 2000)) {
    if (s.src) urls.push(String(s.src));
  }
  for (const sub of subs) {
    if (urls.some((u) => u.includes(sub))) out.scripts.push(sub);
  }
}
return out;
