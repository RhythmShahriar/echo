// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for Plausible's script.js in the Echo consent testbed. It is
// not Plausible's code. Like the real one it sets no cookie and no
// storage: it POSTs one pageview as JSON to https://plausible.io/api/event.
(function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};
  w.__tb_loaded.plausible = true;
  var s = document.currentScript;
  var domain = (s && s.getAttribute('data-domain')) || location.hostname;
  var m = /([a-z0-9-]+)\.shop\.test$/.exec(location.hostname.toLowerCase());
  fetch('https://plausible.io/api/event?_tbrun=' + (m ? m[1] : 'unknown'), {
    method: 'POST',
    mode: 'no-cors',
    keepalive: true,
    headers: {'Content-Type': 'text/plain'},
    body: JSON.stringify({n: 'pageview', u: location.href, d: domain, r: document.referrer || null})
  }).catch(function () {});
})();
