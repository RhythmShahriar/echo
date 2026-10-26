// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stand-in for Google reCAPTCHA's api.js in the Echo consent testbed: a
// necessary service, so it must never count as a tracker. It sets no
// cookie and sends nothing.
(function () {
  'use strict';
  window.__tb_loaded = window.__tb_loaded || {};
  window.__tb_loaded.recaptcha = true;
  window.grecaptcha = {
    ready: function (f) { setTimeout(f, 0); },
    execute: function () { return Promise.resolve('tb-recaptcha-token'); }
  };
})();
