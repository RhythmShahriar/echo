// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// toggles: every checkbox or switch the locator matches, whether it is
// on and a CSS path of what a visitor clicks to change it (the control
// itself, or its label when the control is drawn by a styled slider).
// With input.press the matches are buttons that switch a purpose off
// when pressed: one not pressed yet counts as on.
function cssPath(el, root) {
  const parts = [];
  for (let n = el, i = 0; n && n.nodeType === 1 && i < 32; n = n.parentElement, i++) {
    if (n.id && root.querySelectorAll("#" + CSS.escape(n.id)).length === 1) {
      parts.unshift("#" + CSS.escape(n.id));
      break;
    }
    let k = 1;
    for (let s = n.previousElementSibling; s; s = s.previousElementSibling) {
      if (s.tagName === n.tagName) k++;
    }
    parts.unshift(n.tagName.toLowerCase() + ":nth-of-type(" + k + ")");
    if (n.parentNode === root) break;
  }
  return parts.join(" > ");
}

function clickTarget(el, root) {
  if (visible(el)) return el;
  if (el.id) {
    let l = null;
    try { l = root.querySelector("label[for=\"" + CSS.escape(el.id) + "\"]"); } catch (e) { l = null; }
    if (visible(l)) return l;
  }
  const closest = el.closest("label");
  if (visible(closest)) return closest;
  if (visible(el.parentElement)) return el.parentElement;
  return null;
}

const out = [];
const root = rootOf(input.loc);
if (!root) return out;
let els = [];
try { els = Array.from(root.querySelectorAll(input.loc.css)).slice(0, MAX); } catch (e) { return out; }
for (const el of els) {
  const check = el.type === "checkbox" || el.type === "radio";
  let on = check ? !!el.checked : el.getAttribute("aria-checked") === "true" || el.getAttribute("aria-pressed") === "true";
  if (input.press) on = el.getAttribute("aria-pressed") !== "true";
  const target = clickTarget(el, root);
  out.push({ path: target ? cssPath(target, root) : "", on: on, disabled: disabled(el), text: label(el) });
}
return out;
