// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A hand-made cookie banner for Echo's generic rule tests, the kind a
// site writes itself. It shows the page's <template id="replica-banner">
// unless a choice is stored in localStorage (echo-generic-choice).
// Buttons say what they do with data-do: accept, reject, settings, save
// (stores the choice and whether every switch is off) or hide (closes
// the banner and stores nothing, so it comes back on the next load).
// Script tag attributes: data-show (now, late after 4 s, never) and
// data-frame (the id of a same-origin iframe to draw the banner in).
// Written 2026-10-26.
(function () {
  "use strict";
  var me = document.currentScript;
  var show = (me && me.getAttribute("data-show")) || "now";
  var frameID = me && me.getAttribute("data-frame");
  var KEY = "echo-generic-choice";

  function stored() {
    try { return localStorage.getItem(KEY); } catch (e) { return null; }
  }

  function mount() {
    if (stored()) return;
    var tpl = document.getElementById("replica-banner");
    if (!tpl) return;
    var doc = document;
    if (frameID) {
      var f = document.getElementById(frameID);
      doc = f && f.contentDocument;
      if (!doc) return;
    }
    var root = doc.createElement("div");
    root.appendChild(doc.importNode(tpl.content, true));
    doc.body.appendChild(root);
    var banner = root.querySelector("[data-banner]");
    var dialog = root.querySelector("[data-settings]");
    function close() {
      if (banner) banner.hidden = true;
      if (dialog) dialog.hidden = true;
    }
    function choose(v) {
      try { localStorage.setItem(KEY, v); } catch (e) { /* storage blocked */ }
      close();
    }
    var buttons = root.querySelectorAll("[data-do]");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].addEventListener("click", function (e) {
        var what = e.currentTarget.getAttribute("data-do");
        e.preventDefault();
        if (what === "accept") choose("accept");
        else if (what === "reject") choose("reject");
        else if (what === "hide") close();
        else if (what === "settings" && dialog) dialog.hidden = false;
        else if (what === "save") {
          var on = root.querySelectorAll("[data-settings] input[type=checkbox]:checked:not([disabled])");
          choose(on.length ? "custom" : "reject");
        }
      });
    }
  }

  function start() {
    if (show === "now") mount();
    else if (show === "late") setTimeout(mount, 4000);
  }
  if (frameID) {
    var f = document.getElementById(frameID);
    var fd = f && f.contentDocument;
    // Before the frame loads its page it holds an empty about:blank.
    if (fd && fd.readyState === "complete" && fd.location.href !== "about:blank") start();
    else if (f) f.addEventListener("load", start);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
