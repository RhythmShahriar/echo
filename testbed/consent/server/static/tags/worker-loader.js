// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for a Partytown-style loader in the Echo consent testbed:
// Google Tag Manager runs in a Web Worker and its hits leave from the
// worker with fetch. Every dataLayer entry of the page is copied into
// the worker, those from before this loader with the first message, so
// Consent Mode commands reach the tag there.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.partytown = true;
  var worker = new Worker('/~partytown/gtm-worker.js');
  function plain(x) {
    var arr = Array.isArray(x) || Object.prototype.toString.call(x) === '[object Arguments]';
    try { return JSON.parse(JSON.stringify(arr ? Array.prototype.slice.call(x) : x)); } catch (e) { return null; }
  }
  // __tb_worker_delay (ms) holds every forwarded entry back, as a busy
  // runner does: the tests use it to put the worker's own timers first.
  var delay = Number(w.__tb_worker_delay) || 0;
  function forward(x) {
    var p = plain(x);
    if (!p) return;
    if (delay > 0) setTimeout(function () { worker.postMessage({dl: p}); }, delay);
    else worker.postMessage({dl: p});
  }
  var dl = w.dataLayer = w.dataLayer || [];
  // What the page pushed before this loader (the Consent Mode default
  // above all) goes with init, so the tag reads it as it loads, as the
  // page tag reads dataLayer. Sent as separate messages it could lose
  // the race with the tag's first flush on a slow machine.
  var earlier = [];
  for (var i = 0; i < dl.length; i++) {
    var p = plain(dl[i]);
    if (p) earlier.push(p);
  }
  worker.postMessage({init: {page: {href: location.href, host: location.hostname, title: document.title},
    container: w.__tb_gtm || {ga4: 'G-TESTBED001'}, dl: earlier}});
  var push = dl.push;
  dl.push = function () {
    var r = push.apply(dl, arguments);
    for (var i = 0; i < arguments.length; i++) forward(arguments[i]);
    return r;
  };
})();
