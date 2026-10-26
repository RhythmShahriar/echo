// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for Microsoft UET (bat.js) in the Echo consent testbed. It is
// not Microsoft's code. It reads window.uetq, where the page queues
// 'consent', 'default'|'update', {ad_storage} and events, sends a
// pageLoad GET to https://bat.bing.com/action/0 for the tag ID in
// data-ti. It holds hits while ad_storage is denied. It writes _uetsid
// and _uetvid only while it may send.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.uet = true;
  var s = document.currentScript;
  var ti = (s && s.getAttribute('data-ti')) || '100000001';
  var ad = 'unset';
  var held = [];

  function run() {
    var m = /([a-z0-9-]+)\.shop\.test$/.exec(location.hostname.toLowerCase());
    return m ? m[1] : 'unknown';
  }

  function cookie(name) {
    if (!new RegExp('(?:^|; )' + name + '=').test(document.cookie)) {
      document.cookie = name + '=tb' + Math.floor(Math.random() * 1e9) + '; path=/; max-age=34128000; samesite=lax';
    }
  }

  function send(evt) {
    if (ad === 'denied') { held.push(evt); return; }
    cookie('_uetsid');
    cookie('_uetvid');
    new Image().src = 'https://bat.bing.com/action/0?ti=' + encodeURIComponent(ti) + '&evt=' + encodeURIComponent(evt) +
      '&p=' + encodeURIComponent(location.href) + '&asc=' + (ad === 'denied' ? 'D' : 'G') + '&_tbrun=' + run();
  }

  function take(items) {
    for (var i = 0; i < items.length; i++) {
      var it = items[i];
      if (it === 'consent') {
        var st = items[i + 2] || {};
        if (st.ad_storage) ad = st.ad_storage === 'granted' ? 'granted' : 'denied';
        i += 2;
        if (ad === 'granted') {
          var h = held;
          held = [];
          for (var j = 0; j < h.length; j++) send(h[j]);
        }
      } else if (it === 'event') {
        send(String(items[i + 1] || 'custom'));
        i += 2;
      } else if (it === 'pageLoad') {
        send('pageLoad');
      }
    }
  }

  var q = w.uetq || [];
  var queued = Array.isArray(q) ? q.slice() : [];
  w.uetq = {push: function () { take(Array.prototype.slice.call(arguments)); }};
  take(queued);
  send('pageLoad');
})();
