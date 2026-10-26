// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A replica of CookieYes for Echo's tests, written fresh from the public
// API documentation
// (https://www.cookieyes.com/documentation/retrieving-consent-data-using-api-getckyconsent/):
// window.getCkyConsent() with categories and isUserActionCompleted, and
// the cookieyes-consent cookie as comma-separated key:value pairs. The
// markup follows the banner template of the GPL WordPress plugin
// (cookie-law-info 3.5.7). It is not CookieYes's code. Captured
// 2026-10-26.
//
// The page holds the banner in <template id="replica-banner">; the
// script tag's data-show says when it appears: now, late (after 4 s) or
// never.
(function () {
  "use strict";
  var me = document.currentScript;
  var show = (me && me.getAttribute("data-show")) || "now";
  var cats = ["functional", "analytics", "performance", "advertisement"];
  var state = { necessary: true, functional: false, analytics: false, performance: false, advertisement: false };
  var done = false;
  var mounted = false;

  function q(sel) { return document.querySelector(sel); }

  window.getCkyConsent = function () {
    return {
      activeLaw: "gdpr",
      categories: Object.assign({}, state),
      isUserActionCompleted: done,
      consentID: "replica",
      languageCode: "en"
    };
  };

  function store(granted) {
    var parts = ["consentid:replica", "consent:" + (cats.some(function (c) { return granted[c]; }) ? "yes" : "no"), "action:yes", "necessary:yes"];
    cats.forEach(function (c) {
      state[c] = !!granted[c];
      parts.push(c + ":" + (state[c] ? "yes" : "no"));
    });
    parts.push("other:no");
    done = true;
    document.cookie = "cookieyes-consent=" + parts.join(",") + "; path=/; max-age=31536000; SameSite=Lax";
    close();
  }

  function all(v) {
    var g = {};
    cats.forEach(function (c) { g[c] = v; });
    return g;
  }

  function close() {
    var c = q(".cky-consent-container");
    var m = q(".cky-modal");
    var r = q(".cky-btn-revisit-wrapper");
    if (c) c.classList.add("cky-hide");
    if (m) m.classList.remove("cky-modal-open");
    if (r) r.classList.remove("cky-revisit-hide");
  }

  function openCenter() {
    var m = q(".cky-modal");
    if (m) m.classList.add("cky-modal-open");
  }

  function on(sel, fn) {
    var els = document.querySelectorAll(sel);
    for (var i = 0; i < els.length; i++) {
      els[i].addEventListener("click", function (e) { e.preventDefault(); fn(); });
    }
  }

  function mount() {
    if (mounted) return;
    mounted = true;
    if (!q(".cky-consent-container")) {
      var tpl = document.getElementById("replica-banner");
      if (!tpl) return;
      document.body.appendChild(tpl.content.cloneNode(true));
    }
    q(".cky-consent-container").classList.remove("cky-hide");
    on("[data-cky-tag=accept-button]", function () { store(all(true)); });
    on("[data-cky-tag=reject-button]", function () { store(all(false)); });
    on("[data-cky-tag=settings-button]", openCenter);
    on("[data-cky-tag=detail-accept-button]", function () { store(all(true)); });
    on("[data-cky-tag=detail-reject-button]", function () { store(all(false)); });
    on("[data-cky-tag=detail-save-button]", function () {
      var g = {};
      cats.forEach(function (c) {
        var el = document.getElementById("ckySwitch" + c);
        g[c] = !!(el && el.checked);
      });
      store(g);
    });
    on("[data-cky-tag=revisit-consent]", function () {
      q(".cky-consent-container").classList.remove("cky-hide");
    });
  }

  if (show === "now") {
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount);
    else mount();
  } else if (show === "late") {
    setTimeout(mount, 4000);
  }
})();
