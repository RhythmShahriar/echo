// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Iubenda Privacy Controls and Cookie Solution replica for the Echo
// consent testbed (see SOURCE.md). Loaded as
// https://cdn.iubenda.com/cs/iubenda_cs.js after the page sets
// _iub.csConfiguration ({siteId, cookiePolicyId, ...}). It shows
// #iubenda-cs-banner with Accept, Reject and Learn more and customize (a
// preferences panel #iubenda-iframe with a switch per purpose and Save
// and continue), writes the _iub_cs-<cookiePolicyId> cookie (JSON with
// purposes 1 to 5), answers _iub.cs.api (acceptAll, rejectAll,
// openPreferences, getPreferences, isConsentGiven), calls the
// callback.onPreferenceExpressed hook and sends Consent Mode updates.
// Scripts marked type="text/plain" class="_iub_cs_activate"
// data-iub-purposes="4" (measurement) or "5" (marketing) run once their
// purposes are accepted.
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.iubenda = true;
  var _iub = window._iub = window._iub || [];
  var conf = _iub.csConfiguration || {};
  var policy = conf.cookiePolicyId || 12345678;
  var name = '_iub_cs-' + policy;
  var NAMES = {2: 'Basic interactions', 3: 'Experience enhancement', 4: 'Measurement', 5: 'Marketing'};

  function read() {
    var raw = T.getCookie(name);
    if (!raw) return null;
    try { return JSON.parse(decodeURIComponent(raw)).purposes || null; } catch (e) { return null; }
  }

  var purposes = read();

  function apply(fresh) {
    T.consentUpdate(S, !!purposes[4], !!purposes[5]);
    T.activate('script[type="text/plain"]._iub_cs_activate, iframe._iub_cs_activate[data-src]', function (n) {
      return (n.getAttribute('data-iub-purposes') || '').split(',').every(function (p) { p = p.trim(); return p === '1' || purposes[p]; });
    });
    if (fresh && conf.callback && typeof conf.callback.onPreferenceExpressed === 'function') {
      try { conf.callback.onPreferenceExpressed({consent: !!purposes[5], purposes: purposes}); } catch (e) { /* the site's own code */ }
    }
  }

  function choose(p) {
    p[1] = true;
    purposes = p;
    T.setCookie(name, encodeURIComponent(JSON.stringify({timestamp: new Date().toISOString(), version: '1.60.0', purposes: p, id: policy, cons: {rand: T.rand().slice(0, 6)}})), S);
    close();
    apply(true);
  }

  var all = function (v) { return {1: true, 2: v, 3: v, 4: v, 5: v}; };
  _iub.cs = {api: {
    acceptAll: function () { choose(all(true)); },
    rejectAll: function () { choose(all(false)); },
    openPreferences: function () { open(true); },
    getPreferences: function () { return purposes ? {purposes: purposes} : null; },
    isConsentGiven: function () { return !!purposes && !!purposes[5]; }
  }};

  T.css('#iubenda-cs-banner{' + T.banner + '}#iubenda-cs-banner button,#iubenda-iframe button{' + T.button + '}' +
    '#iubenda-iframe{position:fixed;top:10%;left:10%;right:10%;z-index:2147483001;background:#fff;border:2px solid #333;padding:16px;display:none}');

  function build() {
    var b = document.getElementById('iubenda-cs-banner');
    if (b) return;
    var rows = [2, 3, 4, 5].map(function (k) {
      return '<label class="purposes-item"><input type="checkbox" class="purposes-item-input" data-purpose="' + k + '" checked> ' + NAMES[k] + '</label><br>';
    }).join('');
    document.body.appendChild(T.html('<div id="iubenda-cs-banner" class="iubenda-cs-default" role="alertdialog" aria-label="Cookie notice" style="display:none">' +
      '<div class="iubenda-cs-content"><div id="iubenda-cs-paragraph">We and selected third parties use cookies or similar technologies for technical purposes and, with your consent, for experience, measurement and marketing.</div>' +
      '<div class="iubenda-cs-opt-group"><button class="iubenda-cs-customize-btn" type="button">Learn more and customize</button>' +
      '<button class="iubenda-cs-reject-btn iubenda-cs-btn-primary" type="button">Reject</button>' +
      '<button class="iubenda-cs-accept-btn iubenda-cs-btn-primary" type="button">Accept</button></div></div></div>'));
    document.body.appendChild(T.html('<div id="iubenda-iframe" role="dialog" aria-modal="true" aria-label="Preferences">' +
      '<h2>Your consent preferences</h2>' + rows + '<button class="iub-btn-consent iubenda-cs-save-btn" type="button">Save and continue</button></div>'));
    var q = function (sel) { return document.querySelector(sel); };
    q('.iubenda-cs-accept-btn').addEventListener('click', _iub.cs.api.acceptAll);
    q('.iubenda-cs-reject-btn').addEventListener('click', _iub.cs.api.rejectAll);
    q('.iubenda-cs-customize-btn').addEventListener('click', function () { open(true); });
    q('.iubenda-cs-save-btn').addEventListener('click', function () {
      var p = {1: true};
      document.querySelectorAll('#iubenda-iframe .purposes-item-input').forEach(function (i) { p[i.getAttribute('data-purpose')] = i.checked; });
      choose(p);
    });
  }

  function open(prefs) {
    T.ready(function () {
      build();
      document.getElementById('iubenda-cs-banner').style.display = prefs ? 'none' : 'block';
      document.getElementById('iubenda-iframe').style.display = prefs ? 'block' : 'none';
    });
  }

  function close() {
    var b = document.getElementById('iubenda-cs-banner');
    if (b) b.style.display = 'none';
    var f = document.getElementById('iubenda-iframe');
    if (f) f.style.display = 'none';
  }

  if (purposes) apply(false);
  else T.show(S, function () { open(false); });
})();
