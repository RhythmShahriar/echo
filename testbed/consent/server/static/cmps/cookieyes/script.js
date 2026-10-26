// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// CookieYes replica for the Echo consent testbed's Go mode (see
// SOURCE.md); Docker mode runs the real WordPress plugin. Loaded as
// https://cdn-cookieyes.com/client_data/<id>/script.js. It shows
// .cky-consent-container with Accept All, Reject All and Customise
// (.cky-preference-center with a switch per category and Save My
// Preferences) and the .cky-btn-revisit-wrapper button that reopens it.
// It writes the cookieyes-consent cookie
// (consentid:...,consent:yes|no,action:yes,necessary:yes,...), answers
// getCkyConsent(), fires the cookieyes_consent_update document event
// and sends Consent Mode updates. Scripts
// marked type="text/plain" data-cookieyes="cookieyes-<category>" run once
// their category is accepted.
// Testbed switch: data-tb-gpc-accepts reads GPC as consent to everything
// (the bug of c15).
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.cookieyes = true;
  var CATS = ['necessary', 'functional', 'analytics', 'performance', 'advertisement', 'other'];

  function read() {
    var raw = T.getCookie('cookieyes-consent');
    if (!raw) return null;
    var out = {};
    decodeURIComponent(raw).split(',').forEach(function (p) {
      var i = p.indexOf(':');
      out[p.slice(0, i)] = p.slice(i + 1);
    });
    return out.action === 'yes' ? out : null;
  }

  function cats(st) {
    var c = {};
    CATS.forEach(function (k) { c[k] = !!st && st[k] === 'yes'; });
    c.necessary = true;
    return c;
  }

  var stored = read();

  window.getCkyConsent = function () {
    return {activeLaw: 'gdpr', categories: cats(stored), isUserActionCompleted: !!stored,
      consentID: stored ? stored.consentid : '', languageCode: 'en'};
  };

  function apply(fresh) {
    var c = cats(stored);
    T.consentUpdate(S, c.analytics, c.advertisement);
    T.activate('script[type="text/plain"][data-cookieyes], iframe[data-cookieyes][data-src]', function (n) {
      return c[(n.getAttribute('data-cookieyes') || '').replace(/^cookieyes-/, '')] === true;
    });
    if (fresh) {
      var accepted = CATS.filter(function (k) { return c[k]; });
      T.emit(document, 'cookieyes_consent_update', {accepted: accepted, rejected: CATS.filter(function (k) { return !c[k]; })});
    }
  }

  function choose(c) {
    c.necessary = true;
    var parts = ['consentid:tb' + T.rand(), 'consent:' + (CATS.slice(1).some(function (k) { return c[k]; }) ? 'yes' : 'no'), 'action:yes'];
    CATS.forEach(function (k) { parts.push(k + ':' + (c[k] ? 'yes' : 'no')); });
    T.setCookie('cookieyes-consent', parts.join(','), S);
    stored = read();
    close();
    apply(true);
  }

  var all = function (v) { var c = {}; CATS.forEach(function (k) { c[k] = v; }); return c; };

  T.css('.cky-consent-container{' + T.banner + '}.cky-consent-container button,.cky-btn-revisit-wrapper{' + T.button + '}' +
    '.cky-preference-center{display:none}.cky-consent-container.tb-prefs .cky-preference-center{display:block}' +
    '.cky-btn-revisit-wrapper{position:fixed;left:12px;bottom:12px;z-index:2147482000;display:none}');

  function build() {
    if (document.querySelector('.cky-consent-container')) return;
    var rows = CATS.slice(1, 5).map(function (k) {
      return '<div class="cky-accordion-item"><label>' + k.charAt(0).toUpperCase() + k.slice(1) +
        ' <span class="cky-switch"><input type="checkbox" id="ckySwitch' + k + '" data-cky-category="' + k + '" checked></span></label></div>';
    }).join('');
    document.body.appendChild(T.html('<div class="cky-consent-container cky-banner-bottom" tabindex="0" aria-label="We value your privacy" role="region" style="display:none">' +
      '<div class="cky-consent-bar"><div class="cky-notice"><p class="cky-title">We value your privacy</p>' +
      '<div class="cky-notice-des"><p>We use cookies to enhance your browsing experience, serve personalised ads or content, and analyse our traffic.</p></div>' + // style:ignore (CookieYes's own wording)
      '<div class="cky-notice-btn-wrapper"><button class="cky-btn cky-btn-customize" data-cky-tag="settings-button" type="button">Customise</button>' +
      '<button class="cky-btn cky-btn-reject" data-cky-tag="reject-button" type="button">Reject All</button>' +
      '<button class="cky-btn cky-btn-accept" data-cky-tag="accept-button" type="button">Accept All</button></div></div></div>' +
      '<div class="cky-preference-center" role="dialog" aria-label="Customise Consent Preferences"><div class="cky-accordion-wrapper">' +
      '<div class="cky-accordion-item"><label>Necessary <span class="cky-switch"><input type="checkbox" checked disabled></span></label></div>' + rows + '</div>' +
      '<button class="cky-btn cky-btn-preferences" data-cky-tag="detail-save-button" type="button">Save My Preferences</button></div></div>'));
    document.body.appendChild(T.html('<div class="cky-btn-revisit-wrapper" data-cky-tag="revisit-consent" role="button" tabindex="0" aria-label="Consent Preferences">Consent Preferences</div>'));
    var q = function (sel) { return document.querySelector(sel); };
    q('[data-cky-tag=accept-button]').addEventListener('click', function () { choose(all(true)); });
    q('[data-cky-tag=reject-button]').addEventListener('click', function () { choose(all(false)); });
    q('[data-cky-tag=settings-button]').addEventListener('click', function () { q('.cky-consent-container').classList.add('tb-prefs'); });
    q('[data-cky-tag=detail-save-button]').addEventListener('click', function () {
      var c = all(false);
      document.querySelectorAll('.cky-switch input[data-cky-category]').forEach(function (i) { c[i.getAttribute('data-cky-category')] = i.checked; });
      choose(c);
    });
    q('.cky-btn-revisit-wrapper').addEventListener('click', open);
  }

  function open() {
    T.ready(function () {
      build();
      document.querySelector('.cky-consent-container').style.display = 'block';
      document.querySelector('.cky-btn-revisit-wrapper').style.display = 'none';
    });
  }

  function close() {
    var b = document.querySelector('.cky-consent-container');
    if (b) b.style.display = 'none';
    var r = document.querySelector('.cky-btn-revisit-wrapper');
    if (r) r.style.display = 'block';
  }

  var gpc = navigator.globalPrivacyControl === true;
  if (!stored && gpc && T.has(S, 'gpc-accepts')) {
    // The bug of c15: the GPC signal is taken as a choice of everything.
    choose(all(true));
  } else if (stored) {
    apply(false);
    T.ready(function () { build(); close(); });
  } else {
    T.show(S, open);
  }
})();
