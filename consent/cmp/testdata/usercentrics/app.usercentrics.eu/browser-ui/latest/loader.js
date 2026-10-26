// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A replica of the Usercentrics consent banner for Echo's tests, written
// fresh from Usercentrics's public documentation: the Browser UI API
// (window.UC_UI with acceptAllConsents, denyAllConsents,
// showFirstLayer, showSecondLayer, getServicesBaseInfo and
// isInitialized), the v3 Web CMP API (window.__ucCmp), the CMP selectors
// of the custom CSS page (#uc-main-dialog, #accept, #deny, #more, #save,
// .uc-toggle) and the per-service consent model, stored in localStorage
// uc_settings (v2) or ucData (v3) with uc_user_interaction. It is not
// Usercentrics's code. Captured 2026-10-26.
//   https://usercentrics.com/docs/web/features/custom-css/configuration/
//   https://usercentrics.atlassian.net/wiki/spaces/SKB/pages/705593353
//
// The page holds the banner's content in <template id="replica-banner">
// and the host element (#usercentrics-root for v2,
// #usercentrics-cmp-ui for v3). Attributes of the script tag:
// data-show (now, late after 4 s, hidden: drawn and not shown, or
// never), data-version (2 or 3) and data-shadow (open or closed).
(function () {
  "use strict";
  var me = document.currentScript;
  function attr(n, d) { return (me && me.getAttribute(n)) || d; }
  var show = attr("data-show", "now");
  var v3 = attr("data-version", "2") === "3";
  var mode = attr("data-shadow", "open");
  var services = [
    { id: "H1Vl5NidjWX", name: "Usercentrics Consent Management Platform", essential: true, status: true },
    { id: "HkocEodjb7", name: "Google Analytics", essential: false, status: false },
    { id: "ko1w5PpFl", name: "Facebook Pixel", essential: false, status: false }
  ];
  var interacted = false;
  var root = null;
  var dialog = null;

  function store(decide) {
    services.forEach(function (s) { s.status = s.essential || decide(s); });
    interacted = true;
    var now = Date.now();
    if (v3) {
      var m = {};
      services.forEach(function (s) { m[s.id] = { name: s.name, consent: s.status }; });
      localStorage.setItem("ucData", JSON.stringify({ gcm: {}, consent: { services: m } }));
    } else {
      localStorage.setItem("uc_settings", JSON.stringify({
        controllerId: "replica", id: "replica", language: "en", version: "1.0.0",
        services: services.map(function (s) {
          return { id: s.id, name: s.name, status: s.status, version: "1.0.0",
            history: [{ action: "onUpdateServices", language: "en", status: s.status, timestamp: now, type: "explicit" }] };
        })
      }));
    }
    localStorage.setItem("uc_user_interaction", "true");
    hide();
  }

  function el(sel) { return root ? root.querySelector(sel) : null; }

  function hide() {
    if (dialog) dialog.style.display = "none";
  }

  function showLayer(second) {
    mount();
    if (!dialog) return;
    dialog.style.display = "block";
    var s = el(".replica-second");
    if (s) s.style.display = second ? "block" : "none";
  }

  function toggles() {
    var out = {};
    var list = root ? root.querySelectorAll("input[type=checkbox]") : [];
    for (var i = 0; i < list.length; i++) out[list[i].getAttribute("data-service")] = list[i].checked;
    return out;
  }

  var api = {
    acceptAllConsents: function () { store(function () { return true; }); return Promise.resolve(); },
    denyAllConsents: function () { store(function () { return false; }); return Promise.resolve(); },
    showFirstLayer: function () { showLayer(false); },
    showSecondLayer: function () { showLayer(true); },
    isInitialized: function () { return true; },
    getServicesBaseInfo: function () {
      return services.map(function (s) { return { id: s.id, name: s.name, consent: { status: s.status } }; });
    },
    getConsentDetails: function () {
      var m = {};
      services.forEach(function (s) { m[s.id] = { name: s.name, consent: { given: s.status } }; });
      return Promise.resolve({ consent: { required: !interacted }, services: m });
    }
  };
  if (v3) window.__ucCmp = api;
  else window.UC_UI = api;

  function on(sel, fn) {
    var e = el(sel);
    if (e) e.addEventListener("click", function (ev) { ev.preventDefault(); fn(); });
  }

  function mount() {
    if (root) return;
    var host = document.getElementById(v3 ? "usercentrics-cmp-ui" : "usercentrics-root");
    var tpl = document.getElementById("replica-banner");
    if (!host || !tpl) return;
    root = host.attachShadow({ mode: mode === "closed" ? "closed" : "open" });
    root.appendChild(tpl.content.cloneNode(true));
    dialog = el(v3 ? "#uc-main-dialog" : "[data-testid=uc-default-banner]");
    if (v3) {
      on("#accept", api.acceptAllConsents);
      on("#deny", api.denyAllConsents);
      on("#more", api.showSecondLayer);
      on("#save", function () { var t = toggles(); store(function (s) { return !!t[s.id]; }); });
    } else {
      on("[data-testid=uc-accept-all-button]", api.acceptAllConsents);
      on("[data-testid=uc-deny-all-button]", api.denyAllConsents);
      on("[data-testid=uc-more-button]", api.showSecondLayer);
      on("[data-testid=uc-save-button]", function () { var t = toggles(); store(function (s) { return !!t[s.id]; }); });
    }
  }

  if (show === "now") {
    showLayer(false);
  } else if (show === "late") {
    setTimeout(function () { showLayer(false); }, 4000);
  } else if (show === "hidden") {
    mount();
    hide();
  }
})();
