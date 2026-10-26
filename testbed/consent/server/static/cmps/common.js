// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Helpers every CMP replica of the Echo consent testbed shares. The
// server serves this file in front of each replica's own script, as one
// file, so a replica loads with a single script tag like the real CMP.
// None of this is vendor code.
//
// Testbed switches, read from the replica's script tag:
//   data-tb-delay="4000"       show the banner that many ms after load
//   data-tb-hide-bots          no banner for HeadlessChrome or webdriver
//   data-tb-cookie-path="/x"   write the stored choice under that path
//   data-tb-no-gcm             send no Consent Mode update on a choice
//   data-tb-gcm-v1             Consent Mode updates without the v2 signals
// Each replica documents the switches of its own.
window.__tbcmp = window.__tbcmp || (function () {
  'use strict';
  var w = window;
  w.__tb_loaded = w.__tb_loaded || {};

  function script() { return document.currentScript; }

  function opt(s, name) { return s ? s.getAttribute('data-tb-' + name) : null; }
  function has(s, name) { return !!s && s.hasAttribute('data-tb-' + name); }

  function getCookie(name) {
    var parts = document.cookie ? document.cookie.split('; ') : [];
    for (var i = 0; i < parts.length; i++) {
      var eq = parts[i].indexOf('=');
      if (parts[i].slice(0, eq) === name) return parts[i].slice(eq + 1);
    }
    return null;
  }

  function setCookie(name, value, s, days) {
    var path = opt(s, 'cookie-path') || '/';
    document.cookie = name + '=' + value + '; path=' + path + '; max-age=' + ((days || 365) * 86400) + '; samesite=lax';
  }

  function delCookie(name) {
    document.cookie = name + '=; path=/; max-age=0';
  }

  function ready(fn) {
    if (document.body) fn();
    else document.addEventListener('DOMContentLoaded', fn);
  }

  function bot() { return navigator.webdriver === true || /HeadlessChrome/.test(navigator.userAgent); }

  // show runs fn (which builds the banner) when the page may see it.
  function show(s, fn) {
    if (has(s, 'hide-bots') && bot()) return;
    var delay = Number(opt(s, 'delay')) || 0;
    ready(function () { if (delay) setTimeout(fn, delay); else fn(); });
  }

  function css(text) {
    var st = document.createElement('style');
    st.textContent = text;
    (document.head || document.documentElement).appendChild(st);
  }

  function html(markup) {
    var t = document.createElement('template');
    t.innerHTML = markup.trim();
    return t.content.firstElementChild;
  }

  function gtagPush() {
    w.dataLayer = w.dataLayer || [];
    w.dataLayer.push(arguments);
  }

  // consentUpdate sends a Consent Mode update for the categories:
  // analytics maps to analytics_storage, marketing to the three ad signals.
  // Every call also fires the tb-consent window event the sites' own
  // glue listens to (the layout's tbConsent), the way a site wires its
  // other tags to its CMP.
  function consentUpdate(s, analytics, marketing) {
    emit(window, 'tb-consent', {analytics: !!analytics, marketing: !!marketing});
    if (has(s, 'no-gcm')) return;
    var g = function (b) { return b ? 'granted' : 'denied'; };
    var st = {analytics_storage: g(analytics), ad_storage: g(marketing)};
    if (!has(s, 'gcm-v1')) {
      st.ad_user_data = g(marketing);
      st.ad_personalization = g(marketing);
    }
    gtagPush('consent', 'update', st);
  }

  // activate runs the scripts the page blocked (type="text/plain") whose
  // category allow() accepts. It also loads iframes kept in data-src.
  function activate(selector, allow) {
    ready(function () {
      var nodes = document.querySelectorAll(selector);
      for (var i = 0; i < nodes.length; i++) {
        var n = nodes[i];
        if (n.getAttribute('data-tb-activated') || !allow(n)) continue;
        n.setAttribute('data-tb-activated', '1');
        if (n.tagName === 'IFRAME') {
          if (n.getAttribute('data-src')) n.src = n.getAttribute('data-src');
          continue;
        }
        var s = document.createElement('script');
        for (var j = 0; j < n.attributes.length; j++) {
          var a = n.attributes[j];
          if (a.name !== 'type' && a.name !== 'data-src') s.setAttribute(a.name, a.value);
        }
        var src = n.getAttribute('src') || n.getAttribute('data-src');
        if (src) s.src = src;
        else s.text = n.text;
        n.parentNode.insertBefore(s, n.nextSibling);
      }
    });
  }

  function emit(target, name, detail) {
    var ev;
    try { ev = new CustomEvent(name, {detail: detail}); } catch (e) { return; }
    target.dispatchEvent(ev);
  }

  function rand() { return Math.random().toString(36).slice(2, 12); }

  return {
    script: script, opt: opt, has: has, getCookie: getCookie, setCookie: setCookie, delCookie: delCookie,
    ready: ready, show: show, css: css, html: html, gtag: gtagPush, consentUpdate: consentUpdate,
    activate: activate, emit: emit, rand: rand,
    banner: 'position:fixed;left:0;right:0;bottom:0;z-index:2147483000;background:#fff;color:#111;' +
      'border-top:2px solid #333;padding:16px;font:15px/1.4 system-ui,sans-serif;box-shadow:0 -4px 16px rgba(0,0,0,.2)',
    button: 'margin:6px 8px 0 0;padding:8px 14px;font:inherit;cursor:pointer;border:1px solid #333;border-radius:4px;background:#f4f4f4'
  };
})();
