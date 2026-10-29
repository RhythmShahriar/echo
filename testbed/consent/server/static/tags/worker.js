// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The worker side of the Partytown-style stand-in: on the first message
// it loads the Google tag stand-in from www.googletagmanager.com (as
// gtm.js) over the page's earlier dataLayer entries, then replays every
// later entry the page copies in.
'use strict';
self.dataLayer = [];
var started = false;
var pending = [];
self.onmessage = function (e) {
  var m = e.data || {};
  if (m.init && !started) {
    started = true;
    self.__tb_page = m.init.page;
    self.__tb_gtm = m.init.container;
    self.__tb_src = 'https://www.googletagmanager.com/gtm.js?id=GTM-TESTBED1&tb_worker=1';
    // The page's earlier entries are in dataLayer before the tag loads,
    // so it applies the Consent Mode default before its first hit.
    self.dataLayer = (m.init.dl || []).slice();
    importScripts(self.__tb_src);
    for (var i = 0; i < pending.length; i++) self.dataLayer.push(pending[i]);
    pending = [];
    return;
  }
  if (!('dl' in m)) return;
  // Arrays stand for gtag() calls, as Arguments objects do on the page.
  if (!started) pending.push(m.dl);
  else self.dataLayer.push(m.dl);
};
