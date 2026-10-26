// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// toggles: every checkbox or switch the locator matches, whether it is
// on and a CSS path of what a visitor clicks to change it (the control
// itself, its label or a parent that holds nothing else to click, when
// the control is drawn by a styled slider).
// With input.press the matches are buttons that switch a purpose off
// when pressed: one not pressed yet counts as on.
function clickTarget(el, root) {
  if (visible(el)) return el;
  if (el.id) {
    let l = null;
    try { l = root.querySelector("label[for=\"" + CSS.escape(el.id) + "\"]"); } catch (e) { l = null; }
    if (visible(l)) return l;
  }
  const closest = el.closest("label");
  if (visible(closest)) return closest;
  // The parent row only when the control is all it holds: the centre
  // of a wider row can land on a link or an info button next to it.
  const parent = el.parentElement;
  if (visible(parent) && !otherControl(parent, el)) return parent;
  return null;
}

function otherControl(parent, el) {
  let list = [];
  try { list = Array.from(parent.querySelectorAll("a, button, input, select, textarea, [role=button], [role=switch], [role=link], [tabindex]")); } catch (e) { return true; }
  return list.some((c) => c !== el);
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
