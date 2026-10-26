// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// generic: the elements that may be a cookie banner no rule knows. A
// candidate is visible and fixed, sticky, stacked high (z-index 1000 or
// more) or a dialog; only the outermost of nested candidates counts.
// For each one whose text has a cookie or consent word it reports the
// words found and its controls with their text, so Go can rank them.
// It also looks inside same-origin frames. Input: the normalised word
// lists of generic_words.yaml and, from Detect, the named rules'
// selectors: a container that holds one of them visible (or sits inside
// it) is reported with that rule's id, so a named CMP's banner is never
// taken for an unknown one. Nothing is clicked or changed.
const W = input.words || {};

// named rules' elements, found once: [{id, el}] for each visible match.
const NAMED = [];
for (const r of (input.named || []).slice(0, MAX)) {
  for (const loc of (r.locs || []).slice(0, MAX)) {
    const el = find(loc);
    if (el && visible(el)) NAMED.push({ id: String(r.id).slice(0, 64), el: el });
  }
}

// namedIn is the id of the first named rule with an element inside c or
// around it; "" when none.
function namedIn(c) {
  for (const n of NAMED) {
    if (inside(n.el, c) || inside(c, n.el)) return n.id;
  }
  return "";
}

// norm is generic.go's normText: NFKC, lower case, combining marks and
// format characters dropped, anything else not a letter or digit a
// space.
function norm(s) {
  return String(s == null ? "" : s)
    .normalize("NFKC").toLowerCase()
    .normalize("NFD").replace(/\p{M}+/gu, "").normalize("NFC")
    .replace(/ς/g, "σ").replace(/\p{Cf}+/gu, "")
    .replace(/[^\p{L}\p{N}]+/gu, " ").trim();
}

function anyIn(text, list) {
  return (list || []).some((w) => w && text.includes(w));
}

function anyWord(text, list) {
  const t = " " + text + " ";
  return (list || []).some((w) => w && t.includes(" " + w + " "));
}

function isCandidate(el, win) {
  const cs = win.getComputedStyle(el);
  const role = (el.getAttribute("role") || "").toLowerCase();
  const z = parseInt(cs.zIndex, 10);
  return cs.position === "fixed" || cs.position === "sticky" || (z >= 1000) ||
    role === "dialog" || role === "alertdialog" || el.getAttribute("aria-modal") === "true";
}

// leaves: a link that loads another page (not a fragment of this one,
// not a script link).
function leaves(el) {
  if (el.tagName.toLowerCase() !== "a") return false;
  const raw = el.getAttribute("href");
  if (raw == null || raw === "" || raw.charAt(0) === "#" || /^javascript:/i.test(raw)) return false;
  try {
    const u = new URL(raw, el.ownerDocument.location.href);
    const here = new URL(el.ownerDocument.location.href);
    return u.origin !== here.origin || u.pathname !== here.pathname || u.search !== here.search;
  } catch (e) { return true; }
}

function policyLink(el) {
  if (el.tagName.toLowerCase() !== "a") return false;
  const raw = String(el.getAttribute("href") || "").toLowerCase();
  let path = raw;
  try { path = decodeURIComponent(raw); } catch (e) { path = raw; }
  return (W.policy || []).some((w) => w && path.includes(w));
}

function text(el) {
  const tag = el.tagName.toLowerCase();
  if (tag === "input") return cut(el.value || el.getAttribute("aria-label"));
  return cut(el.innerText || el.textContent || el.getAttribute("aria-label") || el.getAttribute("title"));
}

const CONTROLS = "button, [role=button], input[type=button], input[type=submit], a";

function scanDoc(doc, frame, out) {
  const win = doc.defaultView;
  if (!doc.body || !win) return;
  const all = doc.body.getElementsByTagName("*");
  const found = [];
  for (let i = 0; i < all.length && i < 5000 && found.length < 20; i++) {
    const el = all[i];
    if (found.some((c) => c.contains(el))) continue;
    if (!isCandidate(el, win) || !visible(el)) continue;
    found.push(el);
  }
  for (const el of found) {
    const t = norm(String(el.innerText || el.textContent || "").slice(0, 20000));
    const cookie = anyIn(t, W.cookie_words);
    const consent = anyIn(t, W.consent);
    if (!cookie && !consent) continue;
    const buttons = [];
    for (const b of Array.from(el.querySelectorAll(CONTROLS)).slice(0, 50)) {
      buttons.push({
        path: cssPath(b, doc),
        text: text(b),
        visible: visible(b),
        kind: kindOK(b),
        leaves: leaves(b),
        policy: policyLink(b),
        disabled: disabled(b),
        top: visible(b) && topMost(b),
        box: box(b),
      });
    }
    out.push({
      path: cssPath(el, doc),
      frame: frame,
      cookie: cookie,
      consent: consent,
      ignored: anyWord(t, W.ignore),
      named: namedIn(el),
      text: cut(el.innerText || el.textContent),
      buttons: buttons,
    });
  }
}

const out = [];
scanDoc(document, "", out);
for (const f of Array.from(document.querySelectorAll("iframe, frame")).slice(0, 10)) {
  let doc = null;
  try { doc = f.contentDocument; } catch (e) { doc = null; }
  if (!doc || !visible(f)) continue;
  let src = "";
  try { src = String(f.src || ""); } catch (e) { src = ""; }
  // The frame's address is matched as a substring, so its first MAX
  // characters still name it.
  if (src) scanDoc(doc, src.slice(0, MAX), out);
}
return { containers: out.slice(0, 20) };
