// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for a Partytown-style loader in the Echo consent testbed:
// Google Tag Manager runs in a Web Worker and its hits leave from the
// worker with fetch. Every dataLayer entry of the page, before and after
// this loader, is copied into the worker, so Consent Mode commands reach
// the tag there.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.partytown = true;
  var worker = new Worker('/~partytown/gtm-worker.js');
  worker.postMessage({init: {page: {href: location.href, host: location.hostname, title: document.title},
    container: w.__tb_gtm || {ga4: 'G-TESTBED001'}}});
  function plain(x) {
    var arr = Array.isArray(x) || Object.prototype.toString.call(x) === '[object Arguments]';
    try { return JSON.parse(JSON.stringify(arr ? Array.prototype.slice.call(x) : x)); } catch (e) { return null; }
  }
  function forward(x) {
    var p = plain(x);
    if (p) worker.postMessage({dl: p});
  }
  var dl = w.dataLayer = w.dataLayer || [];
  for (var i = 0; i < dl.length; i++) forward(dl[i]);
  var push = dl.push;
  dl.push = function () {
    var r = push.apply(dl, arguments);
    for (var i = 0; i < arguments.length; i++) forward(arguments[i]);
    return r;
  };
})();
