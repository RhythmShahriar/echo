// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Didomi replica for the Echo consent testbed (see SOURCE.md). Loaded as
// https://sdk.privacy-center.org/<api-key>/loader.js. It shows
// #didomi-host > #didomi-notice with Agree and close, Disagree and close
// and Learn more (a preferences popup with Agree/Disagree buttons per
// purpose and Save). It is an IAB TCF v2.2 CMP (CMP ID 7): it writes the
// euconsent-v2 TC string and the didomi_token cookie, answers
// window.__tcfapi (ping, getTCData, addEventListener with tcloaded,
// cmpuishown and useractioncomplete, removeEventListener) and
// window.Didomi (setUserAgreeToAll, setUserDisagreeToAll,
// preferences.show, getCurrentUserStatus, notice.isVisible, on), runs
// the window.didomiOnReady callbacks and sends Consent Mode updates.
// Scripts marked type="didomi/javascript" data-vendor="<vendor>" run
// once that vendor is agreed to.
// Testbed switches:
//   data-tb-no-accept-update   Agree sends no Consent Mode update
//   data-tb-tcf-reject-grants="1,3"  the TC string after Disagree still
//                              grants those purposes
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.didomi = true;
  var CMP_ID = 7;
  var VENDOR_GOOGLE = 755;
  var PURPOSES = ['cookies', 'select_basic_ads', 'create_ads_profile', 'select_personalized_ads', 'create_content_profile',
    'select_personalized_content', 'measure_ad_performance', 'measure_content_performance', 'market_research', 'improve_products'];
  var VENDORS = ['google', 'facebook', 'tiktok', 'c:youtube'];

  // TC string: the core segment of IAB TCF v2.2, bit by bit.
  function tcString(purposes, vendors) {
    var bits = '';
    var put = function (v, n) { var s = v.toString(2); while (s.length < n) s = '0' + s; bits += s.slice(-n); };
    var letters = function (s) { put(s.charCodeAt(0) - 65, 6); put(s.charCodeAt(1) - 65, 6); };
    var ds = Math.round(Date.now() / 100);
    put(2, 6);
    put(ds, 36);
    put(ds, 36);
    put(CMP_ID, 12);
    put(1, 12);
    put(1, 6);
    letters('EN');
    put(150, 12);
    put(4, 6);
    put(1, 1);
    put(0, 1);
    put(0, 12);
    for (var p = 1; p <= 24; p++) put(purposes.indexOf(p) >= 0 ? 1 : 0, 1);
    put(0, 24);
    put(0, 1);
    letters('DE');
    var max = vendors.length ? Math.max.apply(null, vendors) : 0;
    put(max, 16);
    put(0, 1);
    for (var v = 1; v <= max; v++) put(vendors.indexOf(v) >= 0 ? 1 : 0, 1);
    while (bits.length % 8) bits += '0';
    var bytes = '';
    for (var i = 0; i < bits.length; i += 8) bytes += String.fromCharCode(parseInt(bits.slice(i, i + 8), 2));
    return btoa(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function read() {
    var raw = T.getCookie('didomi_token');
    if (!raw) return null;
    try {
      var j = JSON.parse(atob(decodeURIComponent(raw)));
      return j.purposes && (j.purposes.enabled.length || j.purposes.disabled.length) ? j : null;
    } catch (e) { return null; }
  }

  var state = read();
  var listeners = {};
  var nextListener = 1;

  function agreed() { return !!state && state.purposes.enabled.length > 0; }

  function tcPurposes() {
    if (!state) return [];
    if (agreed()) return [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
    var grants = T.opt(S, 'tcf-reject-grants');
    return grants ? grants.split(',').map(Number) : [];
  }

  function tcData(status, id) {
    var purposes = tcPurposes();
    var consents = {};
    for (var i = 1; i <= 10; i++) consents[i] = purposes.indexOf(i) >= 0;
    var vendorConsents = {};
    vendorConsents[VENDOR_GOOGLE] = agreed();
    return {tcString: T.getCookie('euconsent-v2') || '', tcfPolicyVersion: 4, cmpId: CMP_ID, cmpVersion: 1, gdprApplies: true,
      eventStatus: status, cmpStatus: 'loaded', listenerId: id, isServiceSpecific: true, useNonStandardTexts: false,
      publisherCC: 'DE', purposeOneTreatment: false, purpose: {consents: consents, legitimateInterests: {}},
      vendor: {consents: vendorConsents, legitimateInterests: {}}, specialFeatureOptins: {}, publisher: {consents: {}, legitimateInterests: {}}};
  }

  function notify(status) {
    for (var id in listeners) { try { listeners[id](tcData(status, Number(id)), true); } catch (e) { /* the caller's code */ } }
  }

  var queued = (window.__tcfapi && window.__tcfapi.a) || [];
  window.__tcfapi = function (cmd, version, cb, arg) {
    if (typeof cb !== 'function') return;
    switch (cmd) {
      case 'ping':
        cb({gdprApplies: true, cmpLoaded: true, cmpStatus: 'loaded', displayStatus: visible() ? 'visible' : 'hidden',
          apiVersion: '2.2', cmpVersion: 1, cmpId: CMP_ID, gvlVersion: 150, tcfPolicyVersion: 4}, true);
        break;
      case 'getTCData':
        cb(tcData(state ? 'tcloaded' : 'cmpuishown'), true);
        break;
      case 'addEventListener':
        var id = nextListener++;
        listeners[id] = cb;
        cb(tcData(state ? 'tcloaded' : 'cmpuishown', id), true);
        break;
      case 'removeEventListener':
        cb(delete listeners[arg], true);
        break;
      default:
        cb(null, false);
    }
  };
  queued.forEach(function (a) { window.__tcfapi.apply(null, a); });

  function apply(fresh) {
    var yes = agreed();
    if (!(yes && T.has(S, 'no-accept-update'))) T.consentUpdate(S, yes, yes);
    T.activate('script[type="didomi/javascript"], iframe[data-didomi-vendor][data-src]', function (n) {
      return yes && VENDORS.indexOf(n.getAttribute('data-vendor') || n.getAttribute('data-didomi-vendor')) >= 0;
    });
    if (fresh) {
      notify('useractioncomplete');
      (window.didomiEventListeners || []).forEach(function (l) {
        if (l && l.event === 'consent.changed' && typeof l.listener === 'function') { try { l.listener({}); } catch (e) { /* the site's own code */ } }
      });
    }
  }

  function choose(yes) {
    var now = new Date().toISOString();
    state = {user_id: 'tb-' + T.rand(), created: now, updated: now, version: 2,
      purposes: {enabled: yes ? PURPOSES.slice() : [], disabled: yes ? [] : PURPOSES.slice()},
      vendors: {enabled: yes ? VENDORS.slice() : [], disabled: yes ? [] : VENDORS.slice()}};
    T.setCookie('didomi_token', encodeURIComponent(btoa(JSON.stringify(state))), S);
    T.setCookie('euconsent-v2', tcString(tcPurposes(), yes ? [VENDOR_GOOGLE] : []), S);
    close();
    apply(true);
  }

  var handlers = {};
  window.Didomi = {
    setUserAgreeToAll: function () { choose(true); },
    setUserDisagreeToAll: function () { choose(false); },
    preferences: {show: function () { open(true); }},
    notice: {show: function () { open(false); }, hide: close, isVisible: visible},
    isConsentRequired: function () { return true; },
    getCurrentUserStatus: function () {
      var st = {purposes: {}, vendors: {}, user_id: state ? state.user_id : ''};
      PURPOSES.forEach(function (p) { st.purposes[p] = {id: p, enabled: agreed()}; });
      VENDORS.forEach(function (v) { st.vendors[v] = {id: v, enabled: agreed()}; });
      return st;
    },
    on: function (ev, fn) { (handlers[ev] = handlers[ev] || []).push(fn); }
  };

  T.css('#didomi-notice{' + T.banner + '}#didomi-host button{' + T.button + '}' +
    '#didomi-popup{position:fixed;top:10%;left:10%;right:10%;z-index:2147483001;background:#fff;border:2px solid #333;padding:16px;display:none}');

  function build() {
    var host = document.getElementById('didomi-host');
    if (host) return host;
    var rows = ['Store and/or access information on a device', 'Personalised advertising', 'Measure content performance'].map(function (p, i) {
      return '<div class="didomi-consent-popup-data-processing"><span>' + p + '</span> ' +
        '<button type="button" aria-pressed="false" class="didomi-components-radio__option" data-purpose="' + i + '" data-choice="disagree">Disagree</button>' +
        '<button type="button" aria-pressed="true" class="didomi-components-radio__option" data-purpose="' + i + '" data-choice="agree">Agree</button></div>';
    }).join('');
    host = T.html('<div id="didomi-host" data-nosnippet="true">' +
      '<div id="didomi-notice" class="didomi-popup-notice" role="dialog" aria-label="Cookie consent" style="display:none">' +
      '<p class="didomi-notice-text">We and our partners store and access information on your device, such as cookies, for analytics and advertising.</p>' +
      '<button id="didomi-notice-learn-more-button" type="button">Learn more</button>' +
      '<button id="didomi-notice-disagree-button" type="button">Disagree and close</button>' +
      '<button id="didomi-notice-agree-button" type="button">Agree and close</button></div>' +
      '<div id="didomi-popup" role="dialog" aria-modal="true" aria-label="Preferences">' +
      '<div class="didomi-consent-popup-categories">' + rows + '</div>' +
      '<div class="didomi-consent-popup-actions"><button type="button" aria-label="Save: Save your choices">Save</button></div></div></div>');
    document.body.appendChild(host);
    host.querySelector('#didomi-notice-agree-button').addEventListener('click', window.Didomi.setUserAgreeToAll);
    host.querySelector('#didomi-notice-disagree-button').addEventListener('click', window.Didomi.setUserDisagreeToAll);
    host.querySelector('#didomi-notice-learn-more-button').addEventListener('click', function () { open(true); });
    host.querySelectorAll('.didomi-consent-popup-categories button').forEach(function (b) {
      b.addEventListener('click', function () {
        host.querySelectorAll('button[data-purpose="' + b.getAttribute('data-purpose') + '"]').forEach(function (o) {
          o.setAttribute('aria-pressed', o === b ? 'true' : 'false');
        });
      });
    });
    host.querySelector('.didomi-consent-popup-actions button').addEventListener('click', function () {
      var agree = host.querySelectorAll('.didomi-consent-popup-categories button[data-choice="agree"][aria-pressed="true"]').length;
      choose(agree > 0);
    });
    return host;
  }

  function visible() { var n = document.getElementById('didomi-notice'); return !!n && n.style.display !== 'none'; }

  function open(prefs) {
    T.ready(function () {
      var h = build();
      h.querySelector('#didomi-notice').style.display = prefs ? 'none' : 'block';
      h.querySelector('#didomi-popup').style.display = prefs ? 'block' : 'none';
    });
  }

  function close() {
    var h = document.getElementById('didomi-host');
    if (!h) return;
    h.querySelector('#didomi-notice').style.display = 'none';
    h.querySelector('#didomi-popup').style.display = 'none';
  }

  if (state) apply(false);
  else T.show(S, function () { open(false); notify('cmpuishown'); });
  (window.didomiOnReady || []).forEach(function (fn) { try { fn(window.Didomi); } catch (e) { /* the site's own code */ } });
  window.didomiOnReady = {push: function (fn) { try { fn(window.Didomi); } catch (e) { /* the site's own code */ } }};
})();
