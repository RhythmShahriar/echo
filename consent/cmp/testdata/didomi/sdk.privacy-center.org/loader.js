// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A replica of the Didomi consent notice for Echo's tests, written fresh
// from Didomi's public Web SDK reference
// (https://developers.didomi.io/cmp/web-sdk/reference/api:
// window.Didomi with setUserAgreeToAll, setUserDisagreeToAll,
// preferences.show, getCurrentUserStatus and shouldUserStatusBeCollected,
// window.didomiOnReady), its cookies page
// (https://docs.didomi.io/get-started/general/cookies-and-local-storage:
// didomi_token, base64 JSON with enabled and disabled purposes, and
// euconsent-v2) and the IAB TCF v2.2 specification for the TC string
// and __tcfapi. The markup follows the notice Didomi serves on public
// pages. It is not Didomi's code. Captured 2026-10-26.
//
// The page holds the notice in <template id="replica-banner">. Script
// tag attributes: data-show (now, late after 4 s, never) and data-tcf
// (true: a TCF setup that stores the choice in euconsent-v2 only).
(function () {
  "use strict";
  var me = document.currentScript;
  var show = (me && me.getAttribute("data-show")) || "now";
  var tcf = !!(me && me.getAttribute("data-tcf") === "true");
  var purposes = ["cookies", "select_basic_ads", "create_ads_profile", "measure_content_performance"];
  var status = null;
  var mounted = false;

  function q(sel) { return document.querySelector(sel); }

  // tcString encodes the TCF v2.2 core segment: purposes 1 to 10 and
  // vendors 1 to 10 all granted, or none.
  function tcString(granted) {
    var bits = "";
    function put(v, n) { for (var i = n - 1; i >= 0; i--) bits += Math.floor(v / Math.pow(2, i)) % 2 ? "1" : "0"; }
    var ds = Math.round(Date.now() / 100);
    put(2, 6); put(ds, 36); put(ds, 36); put(7, 12); put(1, 12); put(1, 6);
    put(4, 6); put(13, 6); put(150, 12); put(4, 6); put(1, 1); put(0, 1); put(0, 12);
    for (var p = 1; p <= 24; p++) put(granted && p <= 10 ? 1 : 0, 1);
    for (var l = 1; l <= 24; l++) put(l >= 7 && l <= 10 ? 1 : 0, 1);
    put(0, 1); put(5, 6); put(17, 6);
    put(10, 16); put(0, 1);
    for (var v = 1; v <= 10; v++) put(granted ? 1 : 0, 1);
    put(10, 16); put(0, 1);
    for (var w = 1; w <= 10; w++) put(0, 1);
    put(0, 12);
    while (bits.length % 8) bits += "0";
    var bytes = "";
    for (var i = 0; i < bits.length; i += 8) bytes += String.fromCharCode(parseInt(bits.slice(i, i + 8), 2));
    return btoa(bytes).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function store(granted) {
    status = {};
    purposes.forEach(function (p) { status[p] = !!granted[p]; });
    var any = purposes.some(function (p) { return status[p]; });
    if (tcf) {
      document.cookie = "euconsent-v2=" + tcString(any) + "; path=/; max-age=31536000; SameSite=Lax";
    } else {
      var token = {
        user_id: "replica-" + Date.now(), created: new Date().toISOString(), updated: new Date().toISOString(), version: 2,
        purposes: { enabled: purposes.filter(function (p) { return status[p]; }), disabled: purposes.filter(function (p) { return !status[p]; }) },
        vendors: { enabled: any ? ["google", "c:replica"] : [], disabled: any ? [] : ["google", "c:replica"] }
      };
      document.cookie = "didomi_token=" + btoa(JSON.stringify(token)) + "; path=/; max-age=31536000; SameSite=Lax";
    }
    var n = q("#didomi-notice");
    var p = q("#didomi-popup");
    if (n) n.style.display = "none";
    if (p) p.style.display = "none";
  }

  function all(v) {
    var g = {};
    purposes.forEach(function (p) { g[p] = v; });
    return g;
  }

  function showPreferences() {
    mount();
    var p = q("#didomi-popup");
    if (p) p.style.display = "block";
  }

  window.Didomi = {
    setUserAgreeToAll: function () { store(all(true)); },
    setUserDisagreeToAll: function () { store(all(false)); },
    shouldUserStatusBeCollected: function () { return status === null; },
    preferences: { show: showPreferences },
    notice: { isVisible: function () { var n = q("#didomi-notice"); return !!(n && n.style.display !== "none"); } },
    getCurrentUserStatus: function () {
      var out = { purposes: {}, vendors: {}, regulation: "gdpr" };
      purposes.forEach(function (p) { out.purposes[p] = { id: p, enabled: !!(status && status[p]) }; });
      return out;
    }
  };
  if (tcf) {
    window.__tcfapi = function (cmd, version, cb) {
      if (cmd === "ping" && typeof cb === "function") cb({ gdprApplies: true, cmpLoaded: true, cmpStatus: "loaded", apiVersion: "2.2", cmpId: 7 }, true);
    };
  }
  var ready = window.didomiOnReady || [];
  window.didomiOnReady = { push: function (fn) { fn(window.Didomi); } };
  ready.forEach(function (fn) { fn(window.Didomi); });

  function on(sel, fn) {
    var els = document.querySelectorAll(sel);
    for (var i = 0; i < els.length; i++) {
      els[i].addEventListener("click", function (e) { e.preventDefault(); fn(e.currentTarget); });
    }
  }

  function mount() {
    if (mounted) return;
    mounted = true;
    if (!q("#didomi-host")) {
      var tpl = document.getElementById("replica-banner");
      if (!tpl) return;
      document.body.appendChild(tpl.content.cloneNode(true));
    }
    q("#didomi-host").style.display = "block";
    on("#didomi-notice-agree-button", window.Didomi.setUserAgreeToAll);
    on("#didomi-notice-disagree-button", window.Didomi.setUserDisagreeToAll);
    on("#didomi-notice-learn-more-button", showPreferences);
    on(".didomi-consent-popup-categories button[data-choice]", function (b) {
      var row = b.parentNode;
      var both = row.querySelectorAll("button[data-choice]");
      for (var i = 0; i < both.length; i++) both[i].setAttribute("aria-pressed", both[i] === b ? "true" : "false");
    });
    on(".didomi-consent-popup-actions button[aria-label*=Save]", function () {
      var g = {};
      purposes.forEach(function (p) {
        // A purpose left undecided in the preferences counts as agreed,
        // so Save alone is not a Reject.
        var b = q(".didomi-consent-popup-categories [data-purpose=" + p + "] button[data-choice=disagree]");
        g[p] = !(b && b.getAttribute("aria-pressed") === "true");
      });
      store(g);
    });
  }

  if (show === "now") {
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount);
    else mount();
  } else if (show === "late") {
    setTimeout(mount, 4000);
  }
})();
