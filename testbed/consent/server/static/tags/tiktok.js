// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for the TikTok Pixel (events.js) in the Echo consent testbed.
// It is not TikTok's code. It reads the ttq queue the page snippet left
// (ttq.load, ttq.page, ttq.track, ttq.holdConsent, ttq.grantConsent,
// ttq.revokeConsent), writes _ttp and sends each event as a POST to
// https://analytics.tiktok.com/api/v2/pixel unless consent is held.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.tiktok = true;
  var ttq = w.ttq = w.ttq || [];
  if (ttq.__tb) return;
  ttq.__tb = true;
  var held = false;
  var waiting = [];
  var id = ttq._id || 'TESTBEDTT0001';

  function run() {
    var m = /([a-z0-9-]+)\.shop\.test$/.exec(location.hostname.toLowerCase());
    return m ? m[1] : 'unknown';
  }

  function send(ev) {
    if (held) { waiting.push(ev); return; }
    if (!/(?:^|; )_ttp=/.test(document.cookie)) {
      document.cookie = '_ttp=tb' + Math.floor(Math.random() * 1e9) + '; path=/; max-age=33696000; samesite=lax';
    }
    var body = JSON.stringify({event: ev, context: {pixel: {code: id}, page: {url: location.href}}, timestamp: new Date().toISOString()});
    navigator.sendBeacon('https://analytics.tiktok.com/api/v2/pixel?_tbrun=' + run(), body);
  }

  var methods = {
    page: function () { send('Pageview'); },
    track: function (ev) { send(String(ev)); },
    holdConsent: function () { held = true; },
    grantConsent: function () {
      held = false;
      var q = waiting;
      waiting = [];
      for (var i = 0; i < q.length; i++) send(q[i]);
    },
    revokeConsent: function () { held = true; waiting = []; }
  };
  var queued = ttq.slice();
  ttq.length = 0;
  for (var m in methods) ttq[m] = methods[m];
  for (var i = 0; i < queued.length; i++) {
    var call = queued[i];
    if (methods[call[0]]) methods[call[0]].apply(null, call.slice(1));
  }
})();
