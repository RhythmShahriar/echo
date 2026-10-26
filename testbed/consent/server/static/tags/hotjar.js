// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for Hotjar's tag in the Echo consent testbed. It is not
// Hotjar's code. It writes the session cookies and storage test keys the
// real tag writes (_hjSessionUser_<id>, _hjSession_<id>,
// _hjLocalStorageTest, _hjSessionStorageTest) when it loads, then sends
// its first visit-data POST to in.hotjar.com 3 seconds later.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.hotjar = true;
  var id = (w._hjSettings && w._hjSettings.hjid) || 1000001;
  var v = 'tb' + Math.floor(Math.random() * 1e9);
  document.cookie = '_hjSessionUser_' + id + '=' + v + '; path=/; max-age=31536000; samesite=lax';
  document.cookie = '_hjSession_' + id + '=' + v + '; path=/; max-age=1800; samesite=lax';
  try {
    localStorage.setItem('_hjLocalStorageTest', '1');
    sessionStorage.setItem('_hjSessionStorageTest', '1');
  } catch (e) { /* storage off */ }

  function run() {
    var m = /([a-z0-9-]+)\.shop\.test$/.exec(location.hostname.toLowerCase());
    return m ? m[1] : 'unknown';
  }

  setTimeout(function () {
    navigator.sendBeacon('https://in.hotjar.com/api/v2/client/sites/' + id + '/visit-data?sv=6&_tbrun=' + run(),
      JSON.stringify({url: location.href}));
  }, 3000);
})();
