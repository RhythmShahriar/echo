// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// OneTrust replica for the Echo consent testbed (see SOURCE.md). Loaded
// as https://cdn.cookielaw.org/scripttemplates/otSDKStub.js with
// data-domain-script. It builds #onetrust-consent-sdk with the banner
// (#onetrust-banner-sdk: Accept All Cookies, Reject All, Cookies
// Settings), the preference centre (#onetrust-pc-sdk: a switch per group
// and Confirm My Choices) and the floating #ot-sdk-btn that reopens it.
// It writes OptanonConsent (groups=C0001:1,C0002:0,...) and
// OptanonAlertBoxClosed, keeps window.OnetrustActiveGroups, answers
// window.OneTrust and window.Optanon (AllowAll, RejectAll,
// ToggleInfoDisplay, IsAlertBoxClosed, OnConsentChanged), calls
// window.OptanonWrapper and pushes the OneTrustGroupsUpdated dataLayer
// event. Google Consent Mode follows the groups: C0002 analytics_storage,
// C0004 the ad signals. Scripts marked type="text/plain"
// class="optanon-category-C000n" run once their group is active.
// Testbed switches:
//   data-tb-no-first-layer-reject  Reject All only in the preference centre
//   data-tb-reject-granted         Reject sends analytics_storage granted
//                                  (a wrong category map)
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.onetrust = true;
  var GROUPS = ['C0001', 'C0002', 'C0003', 'C0004', 'C0005'];
  var NAMES = {C0001: 'Strictly Necessary Cookies', C0002: 'Performance Cookies', C0003: 'Functional Cookies', C0004: 'Targeting Cookies', C0005: 'Social Media Cookies'};
  var listeners = [];

  function read() {
    var raw = T.getCookie('OptanonConsent');
    if (!raw || !T.getCookie('OptanonAlertBoxClosed')) return null;
    var m = /(?:^|&)groups=([^&]*)/.exec(raw);
    if (!m) return null;
    var out = {};
    decodeURIComponent(m[1]).split(',').forEach(function (p) {
      var kv = p.split(':');
      out[kv[0]] = kv[1] === '1';
    });
    return out;
  }

  function write(g) {
    var groups = GROUPS.map(function (k) { return k + ':' + (g[k] ? '1' : '0'); }).join(',');
    var v = 'isGpcEnabled=0&datestamp=' + encodeURIComponent(new Date().toString()).replace(/%20/g, '+') +
      '&version=202409.1.0&browserGpcFlag=0&isIABGlobal=false&hosts=&consentId=tb-' + T.rand() +
      '&interactionCount=1&isAnonUser=1&landingPath=NotLandingPage&groups=' + encodeURIComponent(groups) + '&AwaitingReconsent=false';
    T.setCookie('OptanonConsent', v, S);
    T.setCookie('OptanonAlertBoxClosed', new Date().toISOString(), S);
  }

  function activeString(g) {
    return ',' + GROUPS.filter(function (k) { return g[k]; }).join(',') + ',';
  }

  var groups = read();
  window.OnetrustActiveGroups = window.OptanonActiveGroups = activeString(groups || {C0001: true});

  function apply(g, fresh) {
    groups = g;
    window.OnetrustActiveGroups = window.OptanonActiveGroups = activeString(g);
    var analytics = g.C0002;
    if (T.has(S, 'reject-granted') && !g.C0002 && !g.C0004) analytics = true;
    T.consentUpdate(S, analytics, g.C0004);
    T.activate('script[type="text/plain"][class*="optanon-category-"], iframe[data-src][class*="optanon-category-"]', function (n) {
      var m = /optanon-category-((?:C000\d-?)+)/.exec(n.className);
      return !!m && m[1].split('-').every(function (k) { return g[k]; });
    });
    window.dataLayer = window.dataLayer || [];
    window.dataLayer.push({event: 'OneTrustGroupsUpdated', OnetrustActiveGroups: window.OnetrustActiveGroups});
    if (typeof window.OptanonWrapper === 'function') { try { window.OptanonWrapper(); } catch (e) { /* the site's own code */ } }
    if (fresh) {
      listeners.forEach(function (fn) { try { fn({detail: GROUPS.filter(function (k) { return g[k]; })}); } catch (e) { /* the site's own code */ } });
      T.emit(window, 'OneTrustGroupsUpdated', GROUPS.filter(function (k) { return g[k]; }));
    }
  }

  function choose(g) {
    g.C0001 = true;
    write(g);
    close();
    apply(g, true);
  }

  var all = function (v) { return {C0001: true, C0002: v, C0003: v, C0004: v, C0005: v}; };
  var OT = window.OneTrust = window.Optanon = {
    AllowAll: function () { choose(all(true)); },
    RejectAll: function () { choose(all(false)); },
    ToggleInfoDisplay: function () { openPC(); },
    IsAlertBoxClosed: function () { return !!T.getCookie('OptanonAlertBoxClosed'); },
    OnConsentChanged: function (fn) { listeners.push(fn); },
    GetDomainData: function () { return {Groups: GROUPS.map(function (k) { return {CustomGroupId: k, GroupName: NAMES[k]}; })}; }
  };

  T.css('#onetrust-banner-sdk{' + T.banner + '}#onetrust-consent-sdk button{' + T.button + '}' +
    '#onetrust-pc-sdk{position:fixed;top:10%;left:10%;right:10%;z-index:2147483001;background:#fff;border:2px solid #333;padding:16px;display:none;font:15px system-ui,sans-serif}' +
    '#ot-sdk-btn{position:fixed;left:12px;bottom:12px;z-index:2147482000;display:none}');

  function build() {
    var root = document.getElementById('onetrust-consent-sdk');
    if (root) return root;
    var reject = T.has(S, 'no-first-layer-reject') ? '' : '<button id="onetrust-reject-all-handler" type="button">Reject All</button>';
    var switches = GROUPS.slice(1).map(function (k) {
      return '<div class="ot-cat-item"><label>' + NAMES[k] + ' <input type="checkbox" class="category-switch-handler" id="ot-group-id-' + k + '" data-optanongroupid="' + k + '" checked></label></div>';
    }).join('');
    root = T.html('<div id="onetrust-consent-sdk">' +
      '<div id="onetrust-banner-sdk" class="otFlat" role="region" aria-label="Cookie banner" style="display:none">' +
      '<div id="onetrust-policy"><h2 id="onetrust-policy-title">We value your privacy</h2>' +
      '<p id="onetrust-policy-text">We and our partners use cookies to improve your experience and for analytics and advertising.</p></div>' +
      '<div id="onetrust-button-group"><button id="onetrust-pc-btn-handler" type="button">Cookies Settings</button>' + reject +
      '<button id="onetrust-accept-btn-handler" type="button">Accept All Cookies</button></div></div>' +
      '<div id="onetrust-pc-sdk" class="otPcCenter" role="dialog" aria-modal="true" aria-label="Preference center">' +
      '<h2 id="ot-pc-title">Privacy Preference Center</h2>' +
      '<div><input type="checkbox" checked disabled id="ot-group-id-C0001"> Strictly Necessary Cookies (always active)</div>' + switches +
      '<div class="ot-pc-footer"><button id="accept-recommended-btn-handler" type="button">Allow All</button>' +
      '<button class="ot-pc-refuse-all-handler" type="button">Reject All</button>' +
      '<button class="save-preference-btn-handler onetrust-close-btn-handler" type="button">Confirm My Choices</button></div></div>' +
      '<button id="ot-sdk-btn" class="ot-sdk-show-settings" type="button">Cookie Settings</button></div>');
    document.body.appendChild(root);
    var q = function (sel) { return root.querySelector(sel); };
    q('#onetrust-accept-btn-handler').addEventListener('click', OT.AllowAll);
    if (q('#onetrust-reject-all-handler')) q('#onetrust-reject-all-handler').addEventListener('click', OT.RejectAll);
    q('#onetrust-pc-btn-handler').addEventListener('click', openPC);
    q('#accept-recommended-btn-handler').addEventListener('click', OT.AllowAll);
    q('.ot-pc-refuse-all-handler').addEventListener('click', OT.RejectAll);
    q('.save-preference-btn-handler').addEventListener('click', function () {
      var g = {C0001: true};
      GROUPS.slice(1).forEach(function (k) { g[k] = q('#ot-group-id-' + k).checked; });
      choose(g);
    });
    q('#ot-sdk-btn').addEventListener('click', openBanner);
    return root;
  }

  function openBanner() {
    T.ready(function () {
      var r = build();
      r.querySelector('#onetrust-banner-sdk').style.display = 'block';
      r.querySelector('#ot-sdk-btn').style.display = 'none';
    });
  }

  function openPC() {
    T.ready(function () {
      var r = build();
      r.querySelector('#onetrust-banner-sdk').style.display = 'none';
      r.querySelector('#onetrust-pc-sdk').style.display = 'block';
    });
  }

  function close() {
    var r = document.getElementById('onetrust-consent-sdk');
    if (!r) return;
    r.querySelector('#onetrust-banner-sdk').style.display = 'none';
    r.querySelector('#onetrust-pc-sdk').style.display = 'none';
    r.querySelector('#ot-sdk-btn').style.display = 'block';
  }

  if (groups) {
    apply(groups, false);
    T.ready(function () { build(); close(); });
  } else {
    T.show(S, openBanner);
  }
})();
