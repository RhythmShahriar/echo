// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The v3 entry point of the Usercentrics replica (see
// ../../app.usercentrics.eu/browser-ui/latest/loader.js): like the real
// Web CMP loader it only loads the UI bundle, here the shared replica
// with data-version 3. Captured 2026-10-26.
(function () {
  "use strict";
  var me = document.currentScript;
  var s = document.createElement("script");
  s.src = new URL("../../app.usercentrics.eu/browser-ui/latest/loader.js", me.src).href;
  ["data-show", "data-shadow"].forEach(function (n) {
    if (me.hasAttribute(n)) s.setAttribute(n, me.getAttribute(n));
  });
  s.setAttribute("data-version", "3");
  document.head.appendChild(s);
})();
