// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for Google's gtag.js and gtm.js in the Echo consent testbed.
// It is not Google's code. It models what Echo's consent checks read, as
// Google and the community document it:
//   - dataLayer and gtag() commands (consent, config, event, set, js),
//     Arguments objects and arrays alike
//   - Consent Mode default (with region and wait_for_update) and update
//   - gcs and gcd on every hit, gcd with the community's letter table
//   - _ga and _ga_<id> only while analytics_storage is granted
//   - basic mode (data-mode absent or "basic"): nothing is sent while
//     denied; queued events go out when an update grants them
//   - advanced mode (data-mode="advanced" on the script tag): cookieless
//     pings while denied
//   - several queued events go out as one POST body (sendBeacon)
//   - a Google Ads conversion ping while ad_storage is granted
// With no Consent Mode command at all every signal reads as unset, which
// Google's tags treat as granted: they send and write cookies.
// Loaded as gtm.js it plays a container whose tags are in window.__tb_gtm
// ({ga4: "G-...", ads: "AW-..."}, default {ga4: "G-TESTBED001"}).
// It also runs in a Web Worker (Partytown style), where it sends with
// fetch and writes no cookies.
(function () {
  'use strict';
  var g = typeof window !== 'undefined' ? window : self;
  var doc = typeof document !== 'undefined' ? document : null;
  g.__tb_loaded = g.__tb_loaded || {};
  var script = doc && doc.currentScript;
  var src = script ? script.src : (g.__tb_src || '');
  var isGTM = /\/gtm\.js/.test(src);
  g.__tb_loaded[isGTM ? 'gtm' : 'gtag'] = true;
  if (g.__tb_google) {
    // gtag.js and gtm.js on one page share one tag, as Google's do.
    if (isGTM) g.__tb_google.startContainer();
    return;
  }
  var mode = (script && script.getAttribute('data-mode')) || g.__tb_mode || 'basic';
  var region = ((script && script.getAttribute('data-region')) || g.__tb_region || 'DE').toUpperCase();
  var page = g.__tb_page || (g.location ? {href: g.location.href, host: g.location.hostname, title: doc ? doc.title : ''} : {href: '', host: '', title: ''});

  var SIGNALS = ['ad_storage', 'analytics_storage', 'ad_user_data', 'ad_personalization'];
  // gcd letters by default state and update (none: no update), from the
  // community-documented table (Stape, Didomi, Complianz). Keep it in
  // this shape: TestGCDEncodingMatchesDecoder reads it.
  var LETTERS = {
    'unset,none': 'l',
    'denied,none': 'p',
    'denied,denied': 'q',
    'granted,none': 't',
    'denied,granted': 'r',
    'unset,denied': 'm',
    'unset,granted': 'n',
    'granted,denied': 'u',
    'granted,granted': 'v'
  };

  var defaults = {}; // signal -> {v, spec}
  var updates = {}; // signal -> value
  var consentSeen = false;
  var updateSeen = false;
  var waitUntil = 0;
  var configs = {};
  var queue = [];
  var timer = null;
  var containerStarted = false;

  function now() { return Date.now(); }

  function run() {
    var h = (page.host || '').toLowerCase();
    var m = /([a-z0-9-]+)\.shop\.test$/.exec(h);
    return m ? m[1] : 'unknown';
  }

  // regionSpec: 0 for a default with no region, 2 for a country match,
  // 3 for a subdivision match; -1 when the default isn't for us.
  function regionSpec(list) {
    if (!list || !list.length) return 0;
    var best = -1;
    for (var i = 0; i < list.length; i++) {
      var r = String(list[i]).toUpperCase();
      if (r === region) best = Math.max(best, r.indexOf('-') > 0 ? 3 : 2);
      else if (region.indexOf(r + '-') === 0) best = Math.max(best, 2);
    }
    return best;
  }

  function isSignal(k) { return /_storage$/.test(k) || k === 'ad_user_data' || k === 'ad_personalization'; }

  function applyDefault(st) {
    var spec = regionSpec(st.region);
    if (spec < 0) return;
    consentSeen = true;
    for (var k in st) {
      if (!isSignal(k)) continue;
      var v = st[k] === 'granted' ? 'granted' : 'denied';
      // The most specific region wins; among equals the later one.
      if (!defaults[k] || spec >= defaults[k].spec) defaults[k] = {v: v, spec: spec};
    }
    var w = Number(st.wait_for_update);
    if (w > 0) waitUntil = Math.max(waitUntil, now() + Math.min(w, 30000));
  }

  function applyUpdate(st) {
    consentSeen = true;
    updateSeen = true;
    for (var k in st) {
      if (isSignal(k)) updates[k] = st[k] === 'granted' ? 'granted' : 'denied';
    }
  }

  function value(k) {
    if (k in updates) return updates[k];
    if (defaults[k]) return defaults[k].v;
    return 'unset';
  }

  function granted(k) { var v = value(k); return v === 'granted' || v === 'unset'; }

  function digit(k) { var v = value(k); return v === 'granted' ? '1' : v === 'denied' ? '0' : '-'; }

  function gcs() { return consentSeen ? 'G1' + digit('ad_storage') + digit('analytics_storage') : ''; }

  function letter(k) {
    var d = defaults[k] ? defaults[k].v : 'unset';
    var u = k in updates ? updates[k] : 'none';
    return LETTERS[d + ',' + u] || 'l';
  }

  function gcd() {
    return '13' + letter('ad_storage') + '3' + letter('analytics_storage') + '3' +
      letter('ad_user_data') + '3' + letter('ad_personalization') + '5';
  }

  function rand() { return String(Math.floor(Math.random() * 1e9) + 1e9); }

  function cookie(name) {
    if (!doc) return '';
    var m = new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)').exec(doc.cookie);
    return m ? m[1] : '';
  }

  function setCookie(name, v) {
    if (doc && !cookie(name)) doc.cookie = name + '=' + v + '; path=/; max-age=63072000; samesite=lax';
  }

  function enc(v) { return encodeURIComponent(v); }

  function get(url) {
    if (typeof fetch === 'function') {
      fetch(url, {mode: 'no-cors', keepalive: true, credentials: 'include'}).catch(function () {});
    } else {
      new Image().src = url;
    }
  }

  function post(url, body) {
    if (typeof navigator !== 'undefined' && navigator.sendBeacon && navigator.sendBeacon(url, body)) return;
    fetch(url, {method: 'POST', body: body, mode: 'no-cors', keepalive: true, credentials: 'include'}).catch(function () {});
  }

  function collectBase(tid) {
    var c = configs[tid] || {};
    var base = c.transport_url || c.server_container_url || 'https://www.google-analytics.com';
    return base.replace(/\/$/, '') + '/g/collect';
  }

  function ga4(tid, events, cookieless) {
    var q = 'v=2&tid=' + enc(tid) + '&gtm=45tb0&_p=' + rand();
    if (!cookieless) {
      var ts = Math.floor(now() / 1000);
      setCookie('_ga', 'GA1.1.' + rand() + '.' + ts);
      setCookie('_ga_' + tid.replace(/^G-/, ''), 'GS1.1.' + ts + '.1.0.' + ts + '.0.0.0');
      q += '&cid=' + enc(cookie('_ga').replace(/^GA1\.1\./, '') || rand() + '.' + ts);
    }
    var s = gcs();
    if (s) q += '&gcs=' + s;
    q += '&gcd=' + gcd() + '&dl=' + enc(page.href) + '&dt=' + enc(page.title || '') + '&' + '_tbrun=' + run();
    var url = collectBase(tid) + '?' + q;
    if (events.length === 1) {
      get(url + '&en=' + enc(events[0].en) + params(events[0].params));
      return;
    }
    var lines = [];
    for (var i = 0; i < events.length; i++) lines.push('en=' + enc(events[i].en) + params(events[i].params));
    post(url, lines.join('\n'));
  }

  function params(p) {
    var out = '';
    for (var k in p || {}) {
      if (typeof p[k] === 'string' || typeof p[k] === 'number') out += '&ep.' + enc(k) + '=' + enc(p[k]);
    }
    return out;
  }

  function ads(id) {
    var num = String(id).replace(/^AW-/, '').split('/')[0];
    setCookie('_gcl_au', '1.1.' + rand() + '.' + Math.floor(now() / 1000));
    var s = gcs();
    get('https://googleads.g.doubleclick.net/pagead/viewthroughconversion/' + enc(num) + '/?random=' + rand() +
      '&cv=11&fst=' + now() + (s ? '&gcs=' + s : '') + '&gcd=' + gcd() + '&url=' + enc(page.href) + '&_tbrun=' + run());
  }

  function scheduleFlush() {
    if (timer) return;
    var wait = updateSeen ? 0 : Math.max(0, waitUntil - now());
    timer = setTimeout(function () { timer = null; flush(); }, wait);
  }

  function flush() {
    if (!updateSeen && now() < waitUntil) { scheduleFlush(); return; }
    var keep = [];
    var byTid = {};
    var order = [];
    for (var i = 0; i < queue.length; i++) {
      var e = queue[i];
      if (e.ads) {
        if (granted('ad_storage')) ads(e.ads);
        else if (mode !== 'advanced') keep.push(e);
        continue;
      }
      var full = granted('analytics_storage');
      if (!full && mode !== 'advanced') { keep.push(e); continue; }
      if (!byTid[e.tid]) { byTid[e.tid] = {events: [], cookieless: !full}; order.push(e.tid); }
      byTid[e.tid].events.push(e);
    }
    for (var j = 0; j < order.length; j++) ga4(order[j], byTid[order[j]].events, byTid[order[j]].cookieless);
    queue = keep;
  }

  function command(a) {
    var cmd = a[0];
    if (cmd === 'consent') {
      if (a[1] === 'default' && a[2]) applyDefault(a[2]);
      else if (a[1] === 'update' && a[2]) applyUpdate(a[2]);
      scheduleFlush();
    } else if (cmd === 'config' && typeof a[1] === 'string') {
      var id = a[1];
      configs[id] = a[2] || {};
      if (/^G-/.test(id) && configs[id].send_page_view !== false) queue.push({tid: id, en: 'page_view'});
      if (/^AW-/.test(id)) queue.push({ads: id});
      scheduleFlush();
    } else if (cmd === 'event' && typeof a[1] === 'string') {
      var p = a[2] || {};
      var targets = p.send_to ? [].concat(p.send_to) : Object.keys(configs).filter(function (k) { return /^G-/.test(k); });
      for (var i = 0; i < targets.length; i++) {
        if (/^AW-/.test(targets[i])) queue.push({ads: targets[i]});
        else queue.push({tid: targets[i], en: a[1], params: p});
      }
      scheduleFlush();
    }
  }

  function isArgs(x) {
    return Array.isArray(x) || Object.prototype.toString.call(x) === '[object Arguments]';
  }

  function process(x) {
    if (!x || typeof x !== 'object') return;
    if (isArgs(x)) { command(Array.prototype.slice.call(x)); return; }
    // An object push is a GTM event, never Consent Mode.
    if (x.event === 'gtm.js' && isGTM) startContainer();
  }

  function startContainer() {
    if (containerStarted) return;
    containerStarted = true;
    var c = g.__tb_gtm || {ga4: 'G-TESTBED001'};
    if (c.ga4) command(['config', c.ga4]);
    if (c.ads) command(['config', c.ads]);
  }

  var dl = g.dataLayer = g.dataLayer || [];
  var earlier = dl.slice();
  var push = dl.push;
  dl.push = function () {
    var r = push.apply(dl, arguments);
    for (var i = 0; i < arguments.length; i++) process(arguments[i]);
    return r;
  };
  for (var i = 0; i < earlier.length; i++) process(earlier[i]);
  if (isGTM) startContainer();

  g.__tb_google = {
    startContainer: startContainer,
    gcs: gcs,
    gcd: gcd,
    value: value,
    mode: mode,
    // encode returns the gcs and gcd a hit would carry for a state, for
    // the tests: defaults and updates map a signal to granted or denied.
    encode: function (d, u) {
      var saved = [defaults, updates, consentSeen];
      defaults = {};
      updates = {};
      consentSeen = false;
      for (var k in d || {}) { defaults[k] = {v: d[k], spec: 0}; consentSeen = true; }
      for (var j in u || {}) { updates[j] = u[j]; consentSeen = true; }
      var out = {gcs: gcs(), gcd: gcd()};
      defaults = saved[0];
      updates = saved[1];
      consentSeen = saved[2];
      return out;
    }
  };
})();
