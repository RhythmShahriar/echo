// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A replica of the iubenda Cookie Solution banner for Echo's tests,
// written fresh from iubenda's public guides: the embed
// (_iub.csConfiguration, then cdn.iubenda.com/cs/iubenda_cs.js), the
// _iub.cs.api methods of the advanced configuration guide
// (https://www.iubenda.com/en/help/1205-how-to-configure-your-cookie-solution-advanced-guide:
// acceptAll, rejectAll, openPreferences, getPreferences,
// isConsentGiven), the iubenda-cs-preferences-link class and the
// _iub_cs-<cookie policy id> cookie with purposes 1 to 5. The markup
// follows the banner iubenda serves on public pages. It is not
// iubenda's code. Captured 2026-10-26.
//
// The page holds the banner and the preferences panel in
// <template id="replica-banner">; the script tag's data-show says when
// they appear: now, late (after 4 s) or never.
(function () {
  "use strict";
  var me = document.currentScript;
  var show = (me && me.getAttribute("data-show")) || "now";
  window._iub = window._iub || [];
  var conf = window._iub.csConfiguration || { cookiePolicyId: 12345678 };
  var purposes = null;
  var mounted = false;

  function q(sel) { return document.querySelector(sel); }

  function store(p) {
    purposes = { "1": true, "2": !!p["2"], "3": !!p["3"], "4": !!p["4"], "5": !!p["5"] };
    var v = JSON.stringify({ timestamp: new Date().toISOString(), version: "1.62.0", purposes: purposes, id: conf.cookiePolicyId, cons: { rand: "replica" } });
    document.cookie = "_iub_cs-" + conf.cookiePolicyId + "=" + encodeURIComponent(v) + "; path=/; max-age=31536000; SameSite=Lax";
    var b = q("#iubenda-cs-banner");
    var f = q("#iubenda-iframe");
    if (b) b.style.display = "none";
    if (f) f.style.display = "none";
  }

  function all(v) { return { "2": v, "3": v, "4": v, "5": v }; }

  function openPreferences() {
    mount();
    var f = q("#iubenda-iframe");
    if (f) f.style.display = "block";
  }

  window._iub.cs = {
    api: {
      acceptAll: function () { store(all(true)); },
      rejectAll: function () { store(all(false)); },
      openPreferences: openPreferences,
      getPreferences: function () { return purposes ? { purposes: purposes } : null; },
      isConsentGiven: function () { return !!purposes; },
      isPreferenceExpressed: function () { return !!purposes; }
    }
  };

  function on(sel, fn) {
    var els = document.querySelectorAll(sel);
    for (var i = 0; i < els.length; i++) {
      els[i].addEventListener("click", function (e) { e.preventDefault(); fn(); });
    }
  }

  function mount() {
    if (mounted) return;
    mounted = true;
    if (!q("#iubenda-cs-banner")) {
      var tpl = document.getElementById("replica-banner");
      if (!tpl) return;
      document.body.appendChild(tpl.content.cloneNode(true));
    }
    q("#iubenda-cs-banner").style.display = "block";
    on(".iubenda-cs-accept-btn", window._iub.cs.api.acceptAll);
    on(".iubenda-cs-reject-btn", window._iub.cs.api.rejectAll);
    on(".iubenda-cs-customize-btn", openPreferences);
    on(".iubenda-cs-preferences-link", openPreferences);
    on(".iubenda-cs-close-btn", function () { q("#iubenda-cs-banner").style.display = "none"; });
    on("#iubenda-iframe .iubenda-cs-save-btn", function () {
      var p = {};
      var boxes = document.querySelectorAll("#iubenda-iframe .purposes-item-input");
      for (var i = 0; i < boxes.length; i++) p[boxes[i].getAttribute("data-purpose")] = boxes[i].checked;
      store(p);
    });
  }

  if (show === "now") {
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount);
    else mount();
  } else if (show === "late") {
    setTimeout(mount, 4000);
  }
})();
