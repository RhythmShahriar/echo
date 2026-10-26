// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A replica of OneTrust's cookie banner for Echo's tests, written fresh
// from the public JavaScript API documentation
// (https://developer.onetrust.com/onetrust/docs/javascript-api):
// window.OneTrust (and Optanon) with AllowAll, RejectAll,
// ToggleInfoDisplay, Close and IsAlertBoxClosed, window.OnetrustActiveGroups
// and the OptanonConsent and OptanonAlertBoxClosed cookies. It is not
// OneTrust's code. Captured 2026-10-26.
//
// The page's <template id="replica-banner"> holds the banner and the
// preference center; the script tag's data-show says when they appear:
// now, late (after 4 s) or never.
(function () {
  "use strict";
  var me = document.currentScript;
  var show = (me && me.getAttribute("data-show")) || "now";
  var groups = ["C0002", "C0003", "C0004"];
  var mounted = false;

  function byId(id) { return document.getElementById(id); }

  function store(granted) {
    var list = ["C0001:1"];
    var active = ",C0001,";
    groups.forEach(function (g) {
      list.push(g + ":" + (granted[g] ? 1 : 0));
      if (granted[g]) active += g + ",";
    });
    window.OnetrustActiveGroups = active;
    var v = "isGpcEnabled=0&datestamp=" + encodeURIComponent(new Date().toString()) + "&version=202409.1.0" +
      "&isIABGlobal=false&hosts=&consentId=replica&interactionCount=1&landingPath=NotLandingPage&groups=" +
      encodeURIComponent(list.join(","));
    document.cookie = "OptanonConsent=" + v + "; path=/; max-age=31536000; SameSite=Lax";
    document.cookie = "OptanonAlertBoxClosed=" + new Date().toISOString() + "; path=/; max-age=31536000; SameSite=Lax";
    close();
  }

  function all(v) {
    var g = {};
    groups.forEach(function (k) { g[k] = v; });
    return g;
  }

  function close() {
    var b = byId("onetrust-banner-sdk");
    var pc = byId("onetrust-pc-sdk");
    if (b) b.style.display = "none";
    if (pc) pc.style.display = "none";
  }

  function openPC() {
    mount();
    var pc = byId("onetrust-pc-sdk");
    if (pc) pc.style.display = "block";
  }

  var api = {
    AllowAll: function () { store(all(true)); },
    RejectAll: function () { store(all(false)); },
    ToggleInfoDisplay: openPC,
    Close: close,
    IsAlertBoxClosed: function () { return document.cookie.indexOf("OptanonAlertBoxClosed=") >= 0; }
  };
  window.OneTrust = api;
  window.Optanon = api;
  window.OnetrustActiveGroups = ",C0001,";

  function on(sel, fn) {
    var els = document.querySelectorAll(sel);
    for (var i = 0; i < els.length; i++) {
      els[i].addEventListener("click", function (e) { e.preventDefault(); fn(); });
    }
  }

  function mount() {
    if (mounted) return;
    mounted = true;
    if (!byId("onetrust-consent-sdk")) {
      var tpl = byId("replica-banner");
      if (!tpl) return;
      document.body.appendChild(tpl.content.cloneNode(true));
    }
    on("#onetrust-accept-btn-handler", api.AllowAll);
    on("#onetrust-reject-all-handler", api.RejectAll);
    on(".ot-pc-refuse-all-handler", api.RejectAll);
    on("#onetrust-pc-btn-handler", openPC);
    on(".save-preference-btn-handler", function () {
      var g = {};
      groups.forEach(function (k) {
        var el = byId("ot-group-id-" + k);
        g[k] = !!(el && el.checked);
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
