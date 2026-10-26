// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A hand-made cookie banner for the Echo consent testbed: no CMP vendor,
// so Echo's generic rule has to find it by its words. Served from the
// site's own /_tb/banner.js. It stores the choice in the first-party
// site_consent cookie (accepted or rejected), sends Consent Mode updates
// and runs scripts marked type="text/plain" data-consent="analytics|
// marketing" after Accept. Options on its script tag:
//   data-lang="en|de|ar"       the banner's language (ar is right to left)
//   data-newsletter            also show a newsletter dialog with an
//                              "Accept offer" button, which no scan may click
// Testbed switches (common.js has the shared ones):
//   data-tb-late-reject="6000" a transparent overlay covers the page and
//                              Reject only works after that many ms (c20)
//   data-tb-hide-only          Reject hides the banner, stores nothing and
//                              runs the tags anyway (c21)
(function () {
  'use strict';
  var T = window.__tbcmp;
  var S = T.script();
  window.__tb_loaded.banner = true;
  var lang = (S && S.getAttribute('data-lang')) || 'en';
  var TEXT = {
    en: {title: 'Cookies on this site', body: 'We use cookies for analytics and advertising. You can accept or reject them.', accept: 'Accept all', reject: 'Reject all', policy: 'Cookie policy'},
    de: {title: 'Cookies auf dieser Website', body: 'Wir verwenden Cookies für Statistik und Werbung. Sie können sie annehmen oder ablehnen.', accept: 'Alle akzeptieren', reject: 'Alle ablehnen', policy: 'Cookie-Richtlinie'},
    ar: {title: 'ملفات تعريف الارتباط على هذا الموقع', body: 'نستخدم ملفات تعريف الارتباط للتحليلات والإعلانات. يمكنك قبولها أو رفضها.', accept: 'قبول الكل', reject: 'رفض الكل', policy: 'سياسة ملفات تعريف الارتباط'}
  };
  var t = TEXT[lang] || TEXT.en;
  var stored = T.getCookie('site_consent');

  function run(accepted) {
    T.consentUpdate(S, accepted, accepted);
    if (accepted) T.activate('script[type="text/plain"][data-consent], iframe[data-consent][data-src]', function () { return true; });
    T.emit(document, 'site-consent', {accepted: accepted});
  }

  function choose(accepted) {
    T.setCookie('site_consent', accepted ? 'accepted' : 'rejected', S);
    stored = accepted ? 'accepted' : 'rejected';
    hide();
    run(accepted);
  }

  T.css('#site-cookie-banner{' + T.banner + '}#site-cookie-banner button{' + T.button + '}' +
    '#site-newsletter{position:fixed;top:24px;left:50%;transform:translateX(-50%);width:320px;z-index:2147483100;background:#fffbe6;border:2px solid #a80;padding:16px;font:15px system-ui,sans-serif}' +
    '#site-newsletter button{' + T.button + '}');

  function hide() { var b = document.getElementById('site-cookie-banner'); if (b) b.style.display = 'none'; }

  function newsletter() {
    var n = T.html('<div id="site-newsletter" role="dialog" aria-modal="false" aria-label="Newsletter">' +
      '<p><strong>Subscribe to our newsletter</strong> and get 10% off your first order.</p>' +
      '<button type="button" id="newsletter-accept">Accept offer</button><button type="button" id="newsletter-close">No thanks</button></div>');
    document.body.appendChild(n);
    n.querySelector('#newsletter-accept').addEventListener('click', function () {
      // A scan that clicks this has mistaken a newsletter for consent.
      T.setCookie('newsletter_clicked', '1', S);
      n.remove();
    });
    n.querySelector('#newsletter-close').addEventListener('click', function () { n.remove(); });
  }

  function show() {
    var b = T.html('<div id="site-cookie-banner" class="cookie-banner" role="dialog" aria-live="polite" aria-label="' + t.title + '" lang="' + lang + '" dir="' + (lang === 'ar' ? 'rtl' : 'ltr') + '">' +
      '<p><strong>' + t.title + '</strong></p><p>' + t.body + ' <a href="/cookie-policy/">' + t.policy + '</a></p>' +
      '<div class="cookie-banner-buttons"><button type="button" class="cookie-accept">' + t.accept + '</button>' +
      '<button type="button" class="cookie-reject">' + t.reject + '</button></div></div>');
    document.body.appendChild(b);
    b.querySelector('.cookie-accept').addEventListener('click', function () { choose(true); });
    var reject = b.querySelector('.cookie-reject');
    var bindReject = function () {
      reject.addEventListener('click', function () {
        if (T.has(S, 'hide-only')) { hide(); run(true); return; }
        choose(false);
      });
    };
    var late = Number(T.opt(S, 'late-reject')) || 0;
    if (!late) { bindReject(); return; }
    var overlay = T.html('<div class="tb-overlay" style="position:fixed;top:0;left:0;right:0;bottom:0;z-index:2147483646;background:transparent"></div>');
    document.body.appendChild(overlay);
    setTimeout(function () { overlay.remove(); bindReject(); }, late);
  }

  if (S && S.hasAttribute('data-newsletter')) T.ready(newsletter);
  if (stored) run(stored === 'accepted');
  else T.show(S, show);
})();
