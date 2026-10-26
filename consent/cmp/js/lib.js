// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Helpers shared by the CMP scripts. Every script runs in an isolated
// world and only reads the page: no network, no cookie or storage
// writes and no change to the DOM. Lists stop at 200 entries and texts
// at 200 characters, so a hostile page can't make the answer large.
const MAX = 200;

function cut(s) {
  return String(s == null ? "" : s).replace(/\s+/g, " ").trim().slice(0, MAX);
}

// up is the parent of n, crossing from a shadow root to its host.
function up(n) {
  if (!n) return null;
  if (n.parentNode) return n.parentNode;
  return n.nodeType === 11 ? n.host : null;
}

// rootOf is the document, same-origin frame document or shadow root a
// locator's CSS is looked up in; null when it isn't there (a closed
// shadow root, a frame of another origin).
function rootOf(loc) {
  let root = document;
  if (loc.frame && !String(location.href).includes(loc.frame)) {
    root = null;
    for (const f of document.querySelectorAll("iframe, frame")) {
      let src = "";
      try { src = String(f.src || ""); } catch (e) { continue; }
      if (!src.includes(loc.frame)) continue;
      try { if (f.contentDocument) { root = f.contentDocument; break; } } catch (e) { /* another origin */ }
    }
    if (!root) return null;
  }
  for (const host of loc.shadow || []) {
    let h = null;
    try { h = root.querySelector(host); } catch (e) { return null; }
    if (!h || !h.shadowRoot) return null;
    root = h.shadowRoot;
  }
  return root;
}

function find(loc) {
  const root = rootOf(loc);
  if (!root) return null;
  try { return root.querySelector(loc.css); } catch (e) { return null; }
}

// visible: sized, not display:none or visibility hidden, not (with its
// ancestors) transparent and inside the page or scrollable into it.
function visible(el) {
  if (!el || !el.isConnected) return false;
  const win = (el.ownerDocument && el.ownerDocument.defaultView) || window;
  const r = el.getBoundingClientRect();
  if (!((el.offsetWidth > 0 && el.offsetHeight > 0) || (r.width > 0 && r.height > 0))) return false;
  const own = win.getComputedStyle(el);
  if (own.visibility === "hidden" || own.visibility === "collapse") return false;
  for (let n = el, i = 0; n && i < 256; n = up(n), i++) {
    if (n.nodeType !== 1) continue;
    const cs = win.getComputedStyle(n);
    if (cs.display === "none" || parseFloat(cs.opacity) <= 0.01) return false;
  }
  const de = el.ownerDocument.documentElement;
  const w = Math.max(de.scrollWidth, win.innerWidth);
  const h = Math.max(de.scrollHeight, win.innerHeight);
  const x = r.left + win.scrollX;
  const y = r.top + win.scrollY;
  return x + r.width > 0 && y + r.height > 0 && x < w && y < h;
}

function inside(el, container) {
  for (let n = el, i = 0; n && i < 4096; n = up(n), i++) {
    if (n === container) return true;
  }
  return false;
}

// topMost: the element (or a child of it) is what a click at its centre
// reaches. A centre outside the viewport can't be tested before the tab
// scrolls to it; the click checks again then.
function topMost(el) {
  const r = el.getBoundingClientRect();
  const cx = r.left + r.width / 2;
  const cy = r.top + r.height / 2;
  const win = el.ownerDocument.defaultView || window;
  if (cx < 0 || cy < 0 || cx >= win.innerWidth || cy >= win.innerHeight) return true;
  const rn = el.getRootNode();
  const hit = rn.elementFromPoint ? rn.elementFromPoint(cx, cy) : document.elementFromPoint(cx, cy);
  return !hit || hit === el || inside(hit, el);
}

// clickable kinds: a button, [role=button], an input button and a link
// (checked for off-site targets separately).
function kindOK(el) {
  const tag = el.tagName.toLowerCase();
  if (tag === "button" || tag === "a") return true;
  if (tag === "input") return el.type === "button" || el.type === "submit";
  return (el.getAttribute("role") || "").toLowerCase() === "button";
}

function offsite(el) {
  if (el.tagName.toLowerCase() !== "a") return false;
  const raw = el.getAttribute("href");
  if (raw == null || raw === "" || raw.charAt(0) === "#" || /^javascript:/i.test(raw)) return false;
  try { return new URL(raw, location.href).origin !== location.origin; } catch (e) { return true; }
}

function disabled(el) {
  return !!el.disabled || el.getAttribute("aria-disabled") === "true";
}

function label(el) {
  if (el.tagName.toLowerCase() === "input" && el.type !== "checkbox") return cut(el.value);
  return cut(el.getAttribute("aria-label") || el.innerText || el.textContent);
}

function box(el) {
  const r = el.getBoundingClientRect();
  return { X: r.left, Y: r.top, W: r.width, H: r.height };
}
