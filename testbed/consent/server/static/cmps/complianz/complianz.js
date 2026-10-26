// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Complianz replica for the Echo consent testbed's Go mode (see
// SOURCE.md); Docker mode runs the real WordPress plugin. Served from the
// site's own /wp-content/plugins/complianz-gdpr/ as the plugin is. It
// shows #cmplz-cookiebanner-container > .cmplz-cookiebanner with Accept,
// Deny and View preferences (category checkboxes
// .cmplz-consent-checkbox and Save preferences) and the
// .cmplz-manage-consent button that reopens it. It writes the
// cmplz_functional, cmplz_preferences, cmplz_statistics and
// cmplz_marketing cookies (allow or deny) and cmplz_banner-status,
// defines window.complianz and window.cmplz_set_cookie, fires the
// cmplz_status_change document event and sends Consent Mode updates.
// Scripts marked type="text/plain" class="cmplz-script"
// data-category="statistics|marketing|preferences" run once their
// category is allowed.
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.complianz = true;
  var CATS = ['functional', 'preferences', 'statistics', 'marketing'];

  function read() {
    if (!T.getCookie('cmplz_banner-status')) return null;
    var out = {};
    var any = false;
    CATS.forEach(function (c) {
      var v = T.getCookie('cmplz_' + c);
      if (v) any = true;
      out[c] = v === 'allow';
    });
    return any ? out : null;
  }

  var cats = read();
  window.complianz = {version: '7.1.0', region: 'eu', consenttype: 'optin', categories: CATS.slice()};
  window.cmplz_set_cookie = function (name, value) { T.setCookie('cmplz_' + name, value, S); };

  function apply(fresh) {
    T.consentUpdate(S, cats.statistics, cats.marketing);
    T.activate('script[type="text/plain"].cmplz-script, iframe.cmplz-iframe[data-src]', function (n) {
      return cats[n.getAttribute('data-category') || 'marketing'] === true;
    });
    if (fresh) T.emit(document, 'cmplz_status_change', {category: 'all', value: cats.marketing ? 'allow' : 'deny'});
  }

  function choose(c) {
    c.functional = true;
    cats = c;
    CATS.forEach(function (k) { window.cmplz_set_cookie(k, c[k] ? 'allow' : 'deny'); });
    window.cmplz_set_cookie('banner-status', 'dismissed');
    window.cmplz_set_cookie('consented_services', '');
    window.cmplz_set_cookie('policy_id', '1');
    close();
    apply(true);
  }

  var all = function (v) { return {functional: true, preferences: v, statistics: v, marketing: v}; };

  T.css('#cmplz-cookiebanner-container .cmplz-cookiebanner{' + T.banner + '}#cmplz-cookiebanner-container button,.cmplz-manage-consent{' + T.button + '}' +
    '.cmplz-categories{display:none}.cmplz-cookiebanner.tb-prefs .cmplz-categories{display:block}.cmplz-save-preferences{display:none}.cmplz-cookiebanner.tb-prefs .cmplz-save-preferences{display:inline-block}' +
    '.cmplz-manage-consent{position:fixed;left:12px;bottom:12px;z-index:2147482000;display:none}');

  function build() {
    if (document.getElementById('cmplz-cookiebanner-container')) return;
    var boxes = ['preferences', 'statistics', 'marketing'].map(function (c) {
      return '<label><input type="checkbox" class="cmplz-consent-checkbox cmplz-' + c + '" data-category="cmplz_' + c + '" checked> ' + c.charAt(0).toUpperCase() + c.slice(1) + '</label> ';
    }).join('');
    document.body.appendChild(T.html('<div id="cmplz-cookiebanner-container"><div class="cmplz-cookiebanner banner-1 optin cmplz-bottom" role="dialog" aria-live="polite" aria-labelledby="cmplz-header-1-optin" style="display:none">' +
      '<div class="cmplz-header"><div class="cmplz-title" id="cmplz-header-1-optin">Manage Cookie Consent</div></div>' +
      '<div class="cmplz-body"><div class="cmplz-message"><p>To provide the best experiences, we use technologies like cookies to store and/or access device information.</p></div>' +
      '<div class="cmplz-categories"><label><input type="checkbox" class="cmplz-consent-checkbox cmplz-functional" checked disabled> Functional (always active)</label> ' + boxes + '</div></div>' +
      '<div class="cmplz-buttons"><button class="cmplz-btn cmplz-accept" type="button">Accept</button>' +
      '<button class="cmplz-btn cmplz-deny" type="button">Deny</button>' +
      '<button class="cmplz-btn cmplz-view-preferences" type="button">View preferences</button>' +
      '<button class="cmplz-btn cmplz-save-preferences" type="button">Save preferences</button></div></div></div>'));
    document.body.appendChild(T.html('<button class="cmplz-manage-consent cmplz-show-banner" type="button">Manage consent</button>'));
    var q = function (sel) { return document.querySelector(sel); };
    q('.cmplz-accept').addEventListener('click', function () { choose(all(true)); });
    q('.cmplz-deny').addEventListener('click', function () { choose(all(false)); });
    q('.cmplz-view-preferences').addEventListener('click', function () { q('.cmplz-cookiebanner').classList.add('tb-prefs'); });
    q('.cmplz-save-preferences').addEventListener('click', function () {
      var c = {functional: true};
      ['preferences', 'statistics', 'marketing'].forEach(function (k) { c[k] = q('.cmplz-consent-checkbox.cmplz-' + k).checked; });
      choose(c);
    });
    q('.cmplz-manage-consent').addEventListener('click', open);
  }

  function open() {
    T.ready(function () {
      build();
      document.querySelector('.cmplz-cookiebanner').style.display = 'block';
      document.querySelector('.cmplz-manage-consent').style.display = 'none';
    });
  }

  function close() {
    var b = document.querySelector('.cmplz-cookiebanner');
    if (b) b.style.display = 'none';
    var m = document.querySelector('.cmplz-manage-consent');
    if (m) m.style.display = 'block';
  }

  if (cats) {
    apply(false);
    T.ready(function () { build(); close(); });
  } else {
    T.show(S, open);
  }
})();
