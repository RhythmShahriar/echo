// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A replica of Cookiebot for Echo's tests, written fresh from the public
// developer documentation (https://www.cookiebot.com/en/developer/):
// window.Cookiebot with consent, consented, declined, hasResponse,
// show, hide, renew, withdraw and submitCustomConsent, and the
// CookieConsent cookie in its documented JavaScript-object form. It is
// not Cookiebot's code. Captured 2026-10-26.
//
// The page's <template id="replica-banner"> holds the dialog markup; the
// script tag's data-show says when it appears: now, late (after 4 s) or
// never (a returning visitor, or a region that needs no banner).
(function () {
  "use strict";
  var me = document.currentScript;
  var show = (me && me.getAttribute("data-show")) || "now";
  var dialog = null;

  function cookie(p, s, m) {
    var v = "{stamp:'replica-" + Date.now() + "',necessary:true,preferences:" + p + ",statistics:" + s +
      ",marketing:" + m + ",method:'explicit',ver:1,utc:" + Date.now() + ",region:'de'}";
    document.cookie = "CookieConsent=" + encodeURIComponent(v) + "; path=/; max-age=31536000; SameSite=Lax";
  }

  var api = {
    consent: { necessary: true, preferences: false, statistics: false, marketing: false, method: null },
    consented: false,
    declined: false,
    hasResponse: false,
    show: function () { mount(); if (dialog) dialog.style.display = ""; },
    hide: function () { if (dialog) dialog.style.display = "none"; },
    renew: function () { api.show(); },
    withdraw: function () { api.submitCustomConsent(false, false, false); },
    submitCustomConsent: function (p, s, m) {
      p = !!p; s = !!s; m = !!m;
      api.consent = { necessary: true, preferences: p, statistics: s, marketing: m, method: "explicit" };
      api.hasResponse = true;
      api.consented = p || s || m;
      api.declined = !api.consented;
      cookie(p, s, m);
      api.hide();
    }
  };
  window.Cookiebot = api;

  function byId(id) { return document.getElementById(id); }

  function on(id, fn) {
    var el = byId(id);
    if (el) el.addEventListener("click", function (e) { e.preventDefault(); fn(); });
  }

  function openDetails() {
    var d = byId("CybotCookiebotDialogDetail");
    if (d) d.style.display = "block";
  }

  function level(name) {
    var el = byId("CybotCookiebotDialogBodyLevelButton" + name);
    return !!(el && el.checked);
  }

  function mount() {
    if (dialog) return;
    dialog = byId("CybotCookiebotDialog");
    if (!dialog) {
      var tpl = byId("replica-banner");
      if (!tpl) return;
      document.body.appendChild(tpl.content.cloneNode(true));
      dialog = byId("CybotCookiebotDialog");
    }
    on("CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll", function () { api.submitCustomConsent(true, true, true); });
    on("CybotCookiebotDialogBodyButtonAccept", function () { api.submitCustomConsent(true, true, true); });
    on("CybotCookiebotDialogBodyButtonDecline", function () { api.submitCustomConsent(false, false, false); });
    on("CybotCookiebotDialogBodyLevelButtonCustomize", openDetails);
    on("CybotCookiebotDialogNavDetails", openDetails);
    on("CybotCookiebotDialogBodyLevelButtonLevelOptinAllowallSelection", function () {
      api.submitCustomConsent(level("Preferences"), level("Statistics"), level("Marketing"));
    });
  }

  if (show === "now") {
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount);
    else mount();
  } else if (show === "late") {
    setTimeout(mount, 4000);
  }
})();
