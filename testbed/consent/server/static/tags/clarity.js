// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for Microsoft Clarity's tag in the Echo consent testbed. It is
// not Microsoft's code. As the real tag does, it writes its _clck and
// _clsk cookies and the _clck localStorage key as soon as it loads, then
// sends its first upload (POST https://www.clarity.ms/collect) 3 seconds
// later: storage before any hit.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.clarity = true;
  var id = 'tb' + Math.floor(Math.random() * 1e9);
  document.cookie = '_clck=' + id + '%7C2%7Cftb%7C0%7C1; path=/; max-age=31536000; samesite=lax';
  document.cookie = '_clsk=' + id + '%7C1%7C1%7C0%7Ctb.clarity.ms%2Fcollect; path=/; max-age=86400; samesite=lax';
  try { localStorage.setItem('_clck', id); } catch (e) { /* storage off */ }

  function run() {
    var m = /([a-z0-9-]+)\.shop\.test$/.exec(location.hostname.toLowerCase());
    return m ? m[1] : 'unknown';
  }

  setTimeout(function () {
    navigator.sendBeacon('https://www.clarity.ms/collect?_tbrun=' + run(), JSON.stringify({e: [[0, 'tb']], url: location.href}));
  }, 3000);
})();
