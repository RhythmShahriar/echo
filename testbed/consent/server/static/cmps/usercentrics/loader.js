// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Usercentrics replica for the Echo consent testbed (see SOURCE.md).
// Loaded as https://app.usercentrics.eu/browser-ui/latest/loader.js with
// id="usercentrics-cmp" and data-settings-id. It renders the banner
// inside the shadow root of #usercentrics-root: [data-testid=
// uc-default-banner] with uc-accept-all-button, uc-deny-all-button and
// uc-more-button (a second layer with a switch per service and
// uc-save-button). It keeps localStorage uc_settings (services with
// status and history) and uc_user_interaction, answers window.UC_UI
// (acceptAllConsents, denyAllConsents, showSecondLayer,
// getServicesBaseInfo, isInitialized), fires the UC_UI_CMP_EVENT and
// ucEvent window events and sends Consent Mode updates. Scripts marked
// type="text/plain" data-usercentrics="<service name>" and iframes with
// data-usercentrics and data-src run once their service is accepted.
// Testbed switch: data-tb-shadow="closed" uses a closed shadow root.
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.usercentrics = true;
  var SERVICES = [
    {id: 'tbUCcmp001', name: 'Usercentrics Consent Management Platform', essential: true},
    {id: 'tbUCga4001', name: 'Google Analytics', cat: 'analytics'},
    {id: 'tbUCads001', name: 'Google Ads', cat: 'marketing'},
    {id: 'tbUCmeta01', name: 'Meta Pixel', cat: 'marketing'},
    {id: 'tbUCyt0001', name: 'YouTube Video', cat: 'marketing'}
  ];
  var mode = T.opt(S, 'shadow') === 'closed' ? 'closed' : 'open';

  function read() {
    try {
      if (localStorage.getItem('uc_user_interaction') !== 'true') return null;
      var s = JSON.parse(localStorage.getItem('uc_settings') || 'null');
      if (!s || !s.services) return null;
      var out = {};
      s.services.forEach(function (x) { out[x.id] = !!x.status; });
      return out;
    } catch (e) { return null; }
  }

  function write(st, action) {
    var now = Date.now();
    var services = SERVICES.map(function (x) {
      return {id: x.id, name: x.name, status: !!st[x.id], history: [{action: action, language: 'en', status: !!st[x.id], timestamp: now, type: 'explicit', versions: {application: '2.0', service: '1.0', settings: '1'}}]};
    });
    try {
      localStorage.setItem('uc_settings', JSON.stringify({controllerId: 'tb-' + T.rand(), id: (S && S.getAttribute('data-settings-id')) || 'tbSettings1', language: 'en', services: services, version: '1'}));
      localStorage.setItem('uc_user_interaction', 'true');
    } catch (e) { /* storage off */ }
  }

  var state = read();

  function allowed(name) {
    var x = SERVICES.filter(function (s) { return s.name === name; })[0];
    return !!x && !!state && !!state[x.id];
  }

  function cat(c) {
    return SERVICES.some(function (s) { return s.cat === c && state && state[s.id]; });
  }

  function apply(fresh) {
    T.consentUpdate(S, cat('analytics'), cat('marketing'));
    T.activate('script[type="text/plain"][data-usercentrics], iframe[data-usercentrics][data-src]', function (n) {
      return allowed(n.getAttribute('data-usercentrics'));
    });
    if (fresh) {
      var detail = {event: 'consent_status', type: 'explicit'};
      SERVICES.forEach(function (s) { detail[s.name] = !!state[s.id]; });
      T.emit(window, 'ucEvent', detail);
      T.emit(window, 'UC_UI_CMP_EVENT', {type: state[SERVICES[1].id] ? 'ACCEPT_ALL' : 'DENY_ALL', source: 'FIRST_LAYER'});
    }
  }

  function choose(fn, action) {
    var st = {};
    SERVICES.forEach(function (s) { st[s.id] = s.essential || fn(s); });
    state = st;
    write(st, action);
    close();
    apply(true);
  }

  window.UC_UI = {
    acceptAllConsents: function () { choose(function () { return true; }, 'onAcceptAllServices'); return Promise.resolve(); },
    denyAllConsents: function () { choose(function () { return false; }, 'onDenyAllServices'); return Promise.resolve(); },
    showSecondLayer: function () { openLayer(2); },
    showFirstLayer: function () { openLayer(1); },
    isInitialized: function () { return true; },
    getServicesBaseInfo: function () {
      return SERVICES.map(function (s) { return {id: s.id, name: s.name, consent: {status: !!(state && state[s.id])}}; });
    }
  };
  // The v3 browser API (window.__ucCmp) has the same calls.
  window.__ucCmp = {version: 'tb', acceptAllConsents: window.UC_UI.acceptAllConsents, denyAllConsents: window.UC_UI.denyAllConsents,
    showSecondLayer: window.UC_UI.showSecondLayer};

  var shadow = null;
  function build() {
    var host = document.getElementById('usercentrics-root');
    if (host && shadow) return shadow;
    host = document.createElement('div');
    host.id = 'usercentrics-root';
    document.body.appendChild(host);
    shadow = host.attachShadow({mode: mode});
    var rows = SERVICES.filter(function (s) { return !s.essential; }).map(function (s) {
      return '<label><input type="checkbox" role="switch" data-testid="uc-switch-' + s.id + '" data-service="' + s.id + '" checked> ' + s.name + '</label><br>';
    }).join('');
    shadow.innerHTML = '<style>.uc{' + T.banner + '}button{' + T.button + '}.layer2{display:none}.second .layer1{display:none}.second .layer2{display:block}</style>' +
      '<div class="uc" data-testid="uc-default-banner" role="dialog" aria-label="Privacy settings" style="display:none">' +
      '<div class="layer1"><h2>Privacy Settings</h2><p>This site uses cookies and similar technologies for analytics and marketing.</p>' +
      '<button data-testid="uc-more-button" type="button">More Information</button>' +
      '<button data-testid="uc-deny-all-button" type="button">Deny</button>' +
      '<button data-testid="uc-accept-all-button" type="button">Accept All</button></div>' +
      '<div class="layer2" data-testid="uc-second-layer"><h2>Services</h2>' + rows +
      '<button data-testid="uc-save-button" type="button">Save Settings</button></div></div>';
    var q = function (sel) { return shadow.querySelector(sel); };
    q('[data-testid=uc-accept-all-button]').addEventListener('click', window.UC_UI.acceptAllConsents);
    q('[data-testid=uc-deny-all-button]').addEventListener('click', window.UC_UI.denyAllConsents);
    q('[data-testid=uc-more-button]').addEventListener('click', function () { openLayer(2); });
    q('[data-testid=uc-save-button]').addEventListener('click', function () {
      choose(function (s) {
        var box = shadow.querySelector('[data-service="' + s.id + '"]');
        return !!box && box.checked;
      }, 'onUpdateServices');
    });
    return shadow;
  }

  function openLayer(n) {
    T.ready(function () {
      var root = build();
      var b = root.querySelector('[data-testid=uc-default-banner]');
      b.style.display = 'block';
      b.classList.toggle('second', n === 2);
    });
  }

  function close() {
    if (shadow) shadow.querySelector('[data-testid=uc-default-banner]').style.display = 'none';
  }

  if (state) apply(false);
  else T.show(S, function () { openLayer(1); });
})();
