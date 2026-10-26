// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Cookiebot replica for the Echo consent testbed (see SOURCE.md). Loaded
// as https://consent.cookiebot.com/uc.js with data-cbid and
// data-blockingmode="auto" or "manual". It shows #CybotCookiebotDialog
// with Allow all, Deny and Customize (a second layer with category
// checkboxes and Allow selection), writes the CookieConsent cookie,
// answers window.Cookiebot (consent, submitCustomConsent, renew, show,
// hide, withdraw), fires CookiebotOnAccept, CookiebotOnDecline and
// CookiebotOnConsentReady. It sends Consent Mode updates unless
// data-consentmode="disabled". With blocking mode auto it runs scripts
// marked type="text/plain" data-cookieconsent="statistics|marketing|
// preferences" once their category is accepted.
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.cookiebot = true;
  var auto = !S || S.getAttribute('data-blockingmode') !== 'manual';
  var gcmOff = S && S.getAttribute('data-consentmode') === 'disabled';

  function read() {
    var raw = T.getCookie('CookieConsent');
    if (!raw) return null;
    var v = decodeURIComponent(raw);
    var get = function (k) { var m = new RegExp(k + ':(true|false)').exec(v); return m ? m[1] === 'true' : false; };
    return {stamp: (/stamp:'([^']*)'/.exec(v) || [])[1] || '', necessary: true,
      preferences: get('preferences'), statistics: get('statistics'), marketing: get('marketing'),
      method: (/method:'([^']*)'/.exec(v) || [])[1] || 'explicit'};
  }

  function write(c) {
    var v = "{stamp:'" + c.stamp + "',necessary:true,preferences:" + c.preferences + ',statistics:' + c.statistics +
      ',marketing:' + c.marketing + ",method:'explicit',ver:1,utc:" + Date.now() + ",region:'de'}";
    T.setCookie('CookieConsent', v.replace(/'/g, '%27').replace(/,/g, '%2C'), S);
  }

  var state = read();
  var CB = window.Cookiebot = window.CookieConsent = {
    consent: state || {necessary: true, preferences: false, statistics: false, marketing: false, stamp: '0', method: null},
    consented: !!state && (state.statistics || state.marketing || state.preferences),
    declined: !!state && !(state.statistics || state.marketing || state.preferences),
    hasResponse: !!state,
    submitCustomConsent: function (pref, stat, mark) { choose(!!pref, !!stat, !!mark); },
    renew: function () { open(); },
    show: function () { open(); },
    hide: function () { close(); },
    withdraw: function () { choose(false, false, false); }
  };

  function apply(c, fresh) {
    CB.consent = c;
    CB.hasResponse = true;
    CB.consented = c.statistics || c.marketing || c.preferences;
    CB.declined = !CB.consented;
    if (!gcmOff) T.consentUpdate(S, c.statistics, c.marketing);
    if (auto) {
      T.activate('script[type="text/plain"][data-cookieconsent], iframe[data-cookieconsent][data-src]', function (n) {
        var cats = (n.getAttribute('data-cookieconsent') || '').split(',');
        return cats.every(function (k) { k = k.trim(); return k === 'necessary' || c[k] === true; });
      });
    }
    if (fresh) T.emit(window, CB.consented ? 'CookiebotOnAccept' : 'CookiebotOnDecline');
    T.emit(window, 'CookiebotOnConsentReady');
  }

  function choose(pref, stat, mark) {
    var c = {stamp: (state && state.stamp) || 'tb-' + T.rand(), necessary: true, preferences: pref, statistics: stat, marketing: mark, method: 'explicit'};
    state = c;
    write(c);
    close();
    apply(c, true);
  }

  T.css('#CybotCookiebotDialog{' + T.banner + '}#CybotCookiebotDialog button{' + T.button + '}' +
    '#CybotCookiebotDialog .CybotCookiebotDialogDetailBody{display:none}' +
    '#CybotCookiebotDialog.tb-details .CybotCookiebotDialogDetailBody{display:block}');

  function build() {
    var d = document.getElementById('CybotCookiebotDialog');
    if (d) return d;
    d = T.html('<div id="CybotCookiebotDialog" role="dialog" aria-modal="true" aria-label="Cookie consent">' +
      '<div id="CybotCookiebotDialogBody"><h2 id="CybotCookiebotDialogBodyContentTitle">This website uses cookies</h2>' +
      '<p id="CybotCookiebotDialogBodyContentText">We use cookies to personalise content and ads and to analyse our traffic.</p></div>' +
      '<div class="CybotCookiebotDialogDetailBody">' +
      '<label><input type="checkbox" class="CybotCookiebotDialogBodyLevelButton" id="CybotCookiebotDialogBodyLevelButtonNecessary" checked disabled> Necessary</label> ' +
      '<label><input type="checkbox" class="CybotCookiebotDialogBodyLevelButton" id="CybotCookiebotDialogBodyLevelButtonPreferences" checked> Preferences</label> ' +
      '<label><input type="checkbox" class="CybotCookiebotDialogBodyLevelButton" id="CybotCookiebotDialogBodyLevelButtonStatistics" checked> Statistics</label> ' +
      '<label><input type="checkbox" class="CybotCookiebotDialogBodyLevelButton" id="CybotCookiebotDialogBodyLevelButtonMarketing" checked> Marketing</label>' +
      '<div><button id="CybotCookiebotDialogBodyLevelButtonLevelOptinAllowallSelection" type="button">Allow selection</button></div></div>' +
      '<div id="CybotCookiebotDialogFooter">' +
      '<button id="CybotCookiebotDialogBodyButtonDecline" type="button">Deny</button>' +
      '<button id="CybotCookiebotDialogBodyLevelButtonCustomize" type="button">Customize</button>' +
      '<button id="CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll" type="button">Allow all</button></div></div>');
    document.body.appendChild(d);
    d.querySelector('#CybotCookiebotDialogBodyButtonDecline').addEventListener('click', function () { choose(false, false, false); });
    d.querySelector('#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll').addEventListener('click', function () { choose(true, true, true); });
    d.querySelector('#CybotCookiebotDialogBodyLevelButtonCustomize').addEventListener('click', function () { d.classList.add('tb-details'); });
    d.querySelector('#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowallSelection').addEventListener('click', function () {
      var on = function (id) { return d.querySelector('#CybotCookiebotDialogBodyLevelButton' + id).checked; };
      choose(on('Preferences'), on('Statistics'), on('Marketing'));
    });
    return d;
  }

  function open() { T.ready(function () { build().style.display = 'block'; }); }
  function close() { var d = document.getElementById('CybotCookiebotDialog'); if (d) d.style.display = 'none'; }

  if (state) apply(state, false);
  else T.show(S, open);
})();
