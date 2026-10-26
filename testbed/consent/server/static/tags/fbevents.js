// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for Meta's fbevents.js (Meta Pixel) in the Echo consent
// testbed. It is not Meta's code. It models what the consent checks read:
//   - fbq('consent', 'revoke') before fbq('init') holds every event;
//     a revoke after init is ignored: Meta's guide sets consent before
//     init, so a site that revokes later keeps sending
//   - fbq('consent', 'grant') releases the held events
//   - fbq('track', 'PageView') sends GET https://www.facebook.com/tr with
//     id, ev, dl and (from init's advanced matching) ud[em]
//   - it writes _fbp while it may send
//   - it ignores Google Consent Mode entirely, as the real pixel does
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.fbevents = true;
  var fbq = w.fbq;
  if (!fbq) {
    fbq = w.fbq = function () {
      if (fbq.callMethod) fbq.callMethod.apply(fbq, arguments);
      else fbq.queue.push(arguments);
    };
    fbq.queue = [];
  }
  if (fbq.__tb) return;
  fbq.__tb = true;
  fbq.loaded = true;
  fbq.version = '2.0';
  if (!w._fbq) w._fbq = fbq;

  var revoked = false;
  var pixels = [];
  var matching = {};
  var held = [];

  function run() {
    var m = /([a-z0-9-]+)\.shop\.test$/.exec(location.hostname.toLowerCase());
    return m ? m[1] : 'unknown';
  }

  function fbp() {
    if (revoked || /(?:^|; )_fbp=/.test(document.cookie)) return;
    document.cookie = '_fbp=fb.1.' + Date.now() + '.' + Math.floor(Math.random() * 1e9) + '; path=/; max-age=7776000; samesite=lax';
  }

  function send(id, ev) {
    fbp();
    var u = 'https://www.facebook.com/tr?id=' + encodeURIComponent(id) + '&ev=' + encodeURIComponent(ev) +
      '&dl=' + encodeURIComponent(location.href) + '&rl=' + encodeURIComponent(document.referrer) +
      '&if=false&ts=' + Date.now();
    if (matching[id] && matching[id].em) u += '&ud[em]=' + encodeURIComponent(matching[id].em);
    new Image().src = u + '&_tbrun=' + run();
  }

  function track(ev) {
    if (revoked) { held.push(ev); return; }
    for (var i = 0; i < pixels.length; i++) send(pixels[i], ev);
  }

  function call() {
    var a = Array.prototype.slice.call(arguments);
    switch (a[0]) {
      case 'consent':
        if (a[1] === 'revoke' && !pixels.length) revoked = true;
        if (a[1] === 'grant') {
          revoked = false;
          fbp();
          var h = held;
          held = [];
          for (var i = 0; i < h.length; i++) track(h[i]);
        }
        break;
      case 'init':
        pixels.push(String(a[1]));
        if (a[2] && typeof a[2] === 'object') matching[String(a[1])] = a[2];
        fbp();
        break;
      case 'track':
      case 'trackCustom':
        if (typeof a[1] === 'string') track(a[1]);
        break;
    }
  }

  fbq.callMethod = call;
  var q = fbq.queue || [];
  fbq.queue = [];
  for (var i = 0; i < q.length; i++) call.apply(null, q[i]);
})();
