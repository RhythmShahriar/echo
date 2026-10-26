// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for the LinkedIn Insight Tag (insight.min.js) in the Echo
// consent testbed. It is not LinkedIn's code. It sends one GET to
// https://px.ads.linkedin.com/collect for _linkedin_partner_id when it
// loads, or (data-scroll on its script tag, or window._tb_li_scroll) on
// the first scroll: the "consent by scrolling" pattern.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.linkedin = true;
  var s = document.currentScript;
  var pid = w._linkedin_partner_id || '1000001';
  var onScroll = (s && s.hasAttribute('data-scroll')) || w._tb_li_scroll;

  function run() {
    var m = /([a-z0-9-]+)\.shop\.test$/.exec(location.hostname.toLowerCase());
    return m ? m[1] : 'unknown';
  }

  function send() {
    new Image().src = 'https://px.ads.linkedin.com/collect/?pid=' + encodeURIComponent(pid) +
      '&fmt=gif&url=' + encodeURIComponent(location.href) + '&time=' + Date.now() + '&_tbrun=' + run();
  }

  if (!onScroll) { send(); return; }
  var sent = false;
  w.addEventListener('scroll', function () {
    if (sent) return;
    sent = true;
    send();
  }, {passive: true});
})();
