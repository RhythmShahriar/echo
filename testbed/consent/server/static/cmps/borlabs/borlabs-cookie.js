// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Borlabs Cookie replica for the Echo consent testbed (see SOURCE.md),
// served from the site's own /wp-content/plugins/borlabs-cookie/ as the
// WordPress plugin is. It shows #BorlabsCookieBox in the Borlabs 2
// markup (._brlbs-box-wrap with ._brlbs-btn-accept-all,
// ._brlbs-refuse-btn and ._brlbs-manage-btn) or, with
// data-borlabs-version="3" on its script tag, the Borlabs 3 markup
// (.brlbs-cmpnt-dialog with data-borlabs-cookie-actions buttons). Both
// have a preferences layer with a checkbox per group and a save button.
// It writes the borlabs-cookie cookie (JSON: consents per group, uid,
// version), answers window.BorlabsCookie.Consents (getAll, hasConsent,
// hasConsentForServiceGroup), fires borlabs-cookie-consent-saved and
// sends Consent Mode updates. Scripts marked type="text/plain"
// data-borlabs-cookie-group="statistics|marketing|external-media" run
// once their group is accepted.
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.borlabs = true;
  var v3 = S && S.getAttribute('data-borlabs-version') === '3';
  var GROUPS = {essential: ['borlabs-cookie'], statistics: ['google-analytics'], marketing: ['facebook-pixel', 'google-ads'], 'external-media': ['youtube']};
  var LABELS = {statistics: 'Statistics', marketing: 'Marketing', 'external-media': 'External Media'};

  function read() {
    var raw = T.getCookie('borlabs-cookie');
    if (!raw) return null;
    try {
      var j = JSON.parse(decodeURIComponent(raw));
      return j.uid ? j.consents : null;
    } catch (e) { return null; }
  }

  var consents = read();
  var has = function (g) { return !!consents && !!consents[g] && consents[g].length > 0; };

  function apply() {
    T.consentUpdate(S, has('statistics'), has('marketing'));
    T.activate('script[type="text/plain"][data-borlabs-cookie-group], iframe[data-borlabs-cookie-group][data-src]', function (n) {
      return has(n.getAttribute('data-borlabs-cookie-group'));
    });
  }

  function choose(groups) {
    var c = {essential: GROUPS.essential.slice()};
    groups.forEach(function (g) { c[g] = GROUPS[g].slice(); });
    consents = c;
    var exp = new Date(Date.now() + 365 * 86400000).toISOString();
    T.setCookie('borlabs-cookie', encodeURIComponent(JSON.stringify({consents: c, domainPath: location.hostname + '/', expires: exp, uid: 'tb' + T.rand(), version: '1'})), S);
    close();
    T.emit(window, 'borlabs-cookie-consent-saved', {consents: c});
    apply();
  }

  var others = ['statistics', 'marketing', 'external-media'];
  window.BorlabsCookie = {Consents: {
    getAll: function () { return consents; },
    hasConsent: function (svc, group) {
      for (var g in consents || {}) { if ((!group || g === group) && consents[g].indexOf(svc) >= 0) return true; }
      return false;
    },
    hasConsentForServiceGroup: function (g) { return has(g); }
  }};

  T.css('#BorlabsCookieBox ._brlbs-box-wrap,#BorlabsCookieBox .brlbs-cmpnt-dialog{' + T.banner + '}#BorlabsCookieBox button,#BorlabsCookieBox a[role=button]{' + T.button + ';display:inline-block;color:#111;text-decoration:none}' +
    '#BorlabsCookieBox .tb-prefs{display:none}#BorlabsCookieBox.tb-open-prefs .tb-prefs{display:block}#BorlabsCookieBox.tb-open-prefs .tb-first{display:none}');

  function build() {
    if (document.getElementById('BorlabsCookieBox')) return;
    var boxes = others.map(function (g) {
      return '<label class="_brlbs-checkbox brlbs-checkbox"><input type="checkbox" data-borlabs-cookie-checkbox data-group="' + g + '" checked> ' + LABELS[g] + '</label><br>';
    }).join('');
    var markup = v3 ?
      '<div id="BorlabsCookieBox" class="brlbs-cmpnt-container" style="display:none"><div class="brlbs-cmpnt-dialog" role="dialog" aria-label="Privacy Preference">' +
        '<div class="tb-first"><p class="brlbs-cmpnt-p">We need your consent before you can continue on our website.</p>' +
        '<button type="button" class="brlbs-btn brlbs-btn-accept-all" data-borlabs-cookie-actions="accept-all">Accept all</button>' +
        '<button type="button" class="brlbs-btn brlbs-btn-reject" data-borlabs-cookie-actions="reject">Accept only essential cookies</button>' +
        '<button type="button" class="brlbs-btn brlbs-btn-preferences" data-borlabs-cookie-actions="preferences">Individual Privacy Preferences</button></div>' +
        '<div class="tb-prefs">' + boxes + '<button type="button" class="brlbs-btn brlbs-btn-save" data-borlabs-cookie-actions="save-consents">Save</button></div></div></div>' :
      '<div id="BorlabsCookieBox" class="BorlabsCookie" style="display:none"><div class="_brlbs-box-wrap" role="dialog" aria-label="Privacy Preference">' +
        '<div class="tb-first"><p class="_brlbs-flex-center">We need your consent before you can continue on our website.</p>' +
        '<p class="_brlbs-accept"><a href="#" role="button" class="_brlbs-btn _brlbs-btn-accept-all _brlbs-cursor" data-cookie-accept-all>Accept all</a></p>' +
        '<p class="_brlbs-refuse-btn"><a href="#" role="button" class="_brlbs-btn _brlbs-cursor" data-cookie-refuse>Accept only essential cookies</a></p>' +
        '<p class="_brlbs-manage-btn"><a href="#" role="button" class="_brlbs-cursor" data-cookie-individual>Individual Privacy Preferences</a></p></div>' +
        '<div class="tb-prefs">' + boxes + '<a href="#" role="button" class="_brlbs-btn _brlbs-btn-save" data-cookie-accept>Save</a></div></div></div>';
    document.body.appendChild(T.html(markup));
    var box = document.getElementById('BorlabsCookieBox');
    var on = function (sel, fn) {
      box.querySelectorAll(sel).forEach(function (el) { el.addEventListener('click', function (e) { e.preventDefault(); fn(); }); });
    };
    on('[data-cookie-accept-all], [data-borlabs-cookie-actions="accept-all"]', function () { choose(others); });
    on('[data-cookie-refuse], [data-borlabs-cookie-actions="reject"]', function () { choose([]); });
    on('[data-cookie-individual], [data-borlabs-cookie-actions="preferences"]', function () { box.classList.add('tb-open-prefs'); });
    on('[data-cookie-accept], [data-borlabs-cookie-actions="save-consents"]', function () {
      choose(others.filter(function (g) { return box.querySelector('[data-group="' + g + '"]').checked; }));
    });
  }

  function open() { T.ready(function () { build(); document.getElementById('BorlabsCookieBox').style.display = 'block'; }); }
  function close() { var b = document.getElementById('BorlabsCookieBox'); if (b) b.style.display = 'none'; }

  if (consents) apply();
  else T.show(S, open);
})();
