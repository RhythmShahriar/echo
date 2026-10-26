// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// hooks.js watches the consent calls a page makes and reports each one
// through the run's CDP binding. It runs in the main world before any
// page script. It only observes: every wrapped call goes on to the
// page's own function with the same this, arguments and return value.
//
// Go appends the call: (function (cfg) {...})({binding, maxRecords,
// maxBytes}). Everything the hooks use is captured here first, so a page
// that later replaces JSON.stringify, Reflect or setTimeout can't change
// what is reported.
(function (cfg) {
  "use strict";
  const W = window;
  if (W.__rhythmEchoHooks) return; // a second install in the same document
  const defineProperty = Object.defineProperty;
  const getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  const objectKeys = Object.keys;
  const isArray = Array.isArray;
  const arrayPush = Array.prototype.push;
  const apply = Reflect.apply;
  const stringify = JSON.stringify;
  const ProxyCtor = Proxy;
  const WeakSetCtor = WeakSet;
  const weakHas = WeakSet.prototype.has;
  const weakAdd = WeakSet.prototype.add;
  const NodeCtor = typeof Node === "function" ? Node : null;
  const setTimeoutFn = setTimeout;
  const clearIntervalFn = clearInterval;
  const setIntervalFn = setInterval;
  const addListener = EventTarget.prototype.addEventListener;
  const perf = performance;
  const perfNow = Performance.prototype.now;
  const timeOrigin = perf.timeOrigin;
  const encoder = typeof TextEncoder === "function" ? new TextEncoder() : null;
  const encode = encoder ? TextEncoder.prototype.encode : null;
  const strSlice = String.prototype.slice;
  const mathMin = Math.min;

  defineProperty(W, "__rhythmEchoHooks", { value: true });

  const MAX_RECORD = 4096; // consent.MaxHookRecord
  const MAX_DEPTH = 8;
  const MAX_STRING = 1024;
  const MAX_KEYS = 64;
  const maxRecords = cfg.maxRecords > 0 ? cfg.maxRecords : 2000;
  const maxBytes = cfg.maxBytes > 0 ? cfg.maxBytes : 2097152;

  let bind = null;
  let sent = 0; // records sent from this document
  let sentBytes = 0;
  let dropped = 0; // records not sent since the last dropped report
  let dropTimer = 0;

  function now() {
    return timeOrigin + apply(perfNow, perf, []);
  }

  function binding() {
    if (!bind) {
      try {
        const b = W[cfg.binding];
        if (typeof b === "function") bind = b;
      } catch (e) { /* not there yet */ }
    }
    return bind;
  }

  function byteLength(s) {
    if (s.length * 3 <= MAX_RECORD) return s.length; // short enough either way
    return encode ? apply(encode, encoder, [s]).length : s.length * 3;
  }

  // reportDropped tells Go how many records were left out, at most four
  // times a second.
  function reportDropped() {
    dropTimer = 0;
    const b = binding();
    if (!b || dropped === 0) return;
    const n = dropped;
    dropped = 0;
    try { b(stringify({ t: now(), src: "dropped", n: n })); } catch (e) { /* page went away */ }
  }

  function drop(n) {
    dropped += n;
    if (!dropTimer) dropTimer = apply(setTimeoutFn, W, [reportDropped, 250]);
  }

  // send reports one record, unless this document has sent its share.
  function send(rec) {
    if (sent >= maxRecords || sentBytes >= maxBytes) {
      drop(1);
      return;
    }
    const b = binding();
    if (!b) return;
    let s;
    try { s = stringify(rec); } catch (e) { return; }
    const n = byteLength(s);
    sent++;
    sentBytes += n;
    try { b(s); } catch (e) { /* binding gone with the document */ }
  }

  // ser copies v into plain JSON values: functions "[function]", DOM
  // nodes "[node]", an object inside itself "[cycle]", at most 8 levels,
  // strings of at most limit characters. st.cut is set when anything was
  // left out.
  function ser(v, depth, stack, st, limit) {
    const t = typeof v;
    if (v === null || t === "boolean") return v;
    if (t === "undefined") return null;
    if (t === "number") return v === v && v !== Infinity && v !== -Infinity ? v : String(v);
    if (t === "string") {
      if (v.length > limit) {
        st.cut = true;
        return apply(strSlice, v, [0, limit]);
      }
      return v;
    }
    if (t === "bigint") return String(v);
    if (t === "symbol") return "[symbol]";
    if (t === "function") return "[function]";
    try {
      if (NodeCtor && v instanceof NodeCtor) return "[node]";
      if (v === W) return "[node]";
    } catch (e) { return "[node]"; }
    for (let i = 0; i < stack.length; i++) {
      if (stack[i] === v) return "[cycle]";
    }
    if (depth >= MAX_DEPTH) {
      st.cut = true;
      return "[deep]";
    }
    apply(arrayPush, stack, [v]);
    try {
      if (isArray(v)) {
        const n = mathMin(v.length, MAX_KEYS);
        if (v.length > n) st.cut = true;
        const out = [];
        for (let i = 0; i < n; i++) {
          let x;
          try { x = v[i]; } catch (e) { x = "[unreadable]"; }
          out[i] = ser(x, depth + 1, stack, st, limit);
        }
        return out;
      }
      let keys;
      try { keys = objectKeys(v); } catch (e) { return "[unreadable]"; }
      if (keys.length > MAX_KEYS) {
        st.cut = true;
        keys.length = MAX_KEYS;
      }
      const out = {};
      for (let i = 0; i < keys.length; i++) {
        let x;
        try { x = v[keys[i]]; } catch (e) { x = "[unreadable]"; }
        if (typeof x === "undefined") continue;
        out[keys[i]] = ser(x, depth + 1, stack, st, limit);
      }
      return out;
    } finally {
      stack.length--;
    }
  }

  // record builds a record of a call and cuts it to 4 KiB: shorter
  // strings first, then the arguments themselves.
  function record(src, args) {
    const t = now();
    const list = [];
    const n = mathMin(args.length, MAX_KEYS);
    for (let i = 0; i < n; i++) apply(arrayPush, list, [args[i]]);
    const limits = [MAX_STRING, 256, 64];
    for (let i = 0; i < limits.length; i++) {
      const st = { cut: args.length > n };
      const rec = { t: t, src: src, args: ser(list, 0, [], st, limits[i]) };
      if (st.cut || i > 0) rec.cut = true;
      let s;
      try { s = stringify(rec); } catch (e) { break; }
      if (byteLength(s) <= MAX_RECORD - 64) return rec;
    }
    return { t: t, src: src, args: ["[cut]"], cut: true };
  }

  // quiet is set while gtag runs: its own dataLayer push is the same
  // call, already reported as gtag.
  let quiet = 0;

  function report(src, args) {
    if (src === "datalayer" && quiet > 0) return;
    if (sent >= maxRecords || sentBytes >= maxBytes) {
      drop(1); // counted without the work of copying it
      return;
    }
    send(record(src, args));
  }

  const pushWrapped = new WeakSetCtor();

  // wrapPush watches obj.push and keeps watching when the page (or GTM)
  // replaces push later: push becomes an accessor whose getter is one
  // watcher that reports the call once and runs the newest function the
  // page assigned. A replacement that calls the push it read before
  // (the watcher) reaches the function before it, so a chain of wrappers
  // runs as the page built it and is reported once.
  function wrapPush(obj, src) {
    if (!obj || (typeof obj !== "object" && typeof obj !== "function")) return true;
    if (apply(weakHas, pushWrapped, [obj])) return true;
    let first;
    try { first = obj.push; } catch (e) { return false; }
    if (typeof first !== "function") first = arrayPush;
    const fns = [first];
    let depth = 0;
    const watcher = function () {
      const d = depth;
      if (d === 0) report(src, arguments);
      let i = fns.length - 1 - d;
      if (i < 0) i = 0;
      depth++;
      try {
        return apply(fns[i], this, arguments);
      } finally {
        depth--;
      }
    };
    try {
      defineProperty(obj, "push", {
        configurable: true,
        enumerable: false,
        get: function () { return watcher; },
        set: function (f) {
          if (typeof f !== "function" || f === watcher) return;
          if (fns.length >= 16) fns.length = 15;
          apply(arrayPush, fns, [f]);
        },
      });
    } catch (e) {
      return false;
    }
    apply(weakAdd, pushWrapped, [obj]);
    return true;
  }

  // reportEntries reports what an array held before it was watched.
  function reportEntries(arr, src, from) {
    let n = 0;
    try { n = arr.length; } catch (e) { return 0; }
    for (let i = from; i < n; i++) {
      if (sentBytes >= maxBytes || sent >= maxRecords) {
        drop(n - i);
        return n;
      }
      let x;
      try { x = arr[i]; } catch (e) { continue; }
      report(src, [x]);
    }
    return n;
  }

  const fnWrapped = new WeakSetCtor();

  // wrapFn watches calls of a function through a Proxy, so its own
  // properties (fbq.queue, fbq.callMethod) read through unchanged.
  function wrapFn(fn, src) {
    if (typeof fn !== "function" || apply(weakHas, fnWrapped, [fn])) return fn;
    let depth = 0;
    let p;
    try {
      p = new ProxyCtor(fn, {
        apply: function (target, self, args) {
          if (depth === 0) report(src, args);
          depth++;
          if (src === "gtag") quiet++;
          try {
            return apply(target, self, args);
          } finally {
            depth--;
            if (src === "gtag") quiet--;
          }
        },
      });
    } catch (e) {
      return fn;
    }
    apply(weakAdd, fnWrapped, [p]);
    return p;
  }

  // hookGlobal turns window[name] into an accessor that watches every
  // value the page assigns. false when the page made it impossible.
  function hookGlobal(name, wrap) {
    let value;
    try {
      const d = getOwnPropertyDescriptor(W, name);
      if (d && !d.configurable) return false;
      if (d && "value" in d) value = wrap(d.value);
      defineProperty(W, name, {
        configurable: true,
        enumerable: true,
        get: function () { return value; },
        set: function (v) { value = wrap(v); },
      });
      return true;
    } catch (e) {
      return false;
    }
  }

  function watchArray(src) {
    return function (v) {
      if (v && typeof v === "object" && !apply(weakHas, pushWrapped, [v])) {
        // An array assigned with entries already in it (dataLayer = [[...]]).
        if (isArray(v)) reportEntries(v, src, 0);
        wrapPush(v, src);
      }
      return v;
    };
  }

  hookGlobal("dataLayer", watchArray("datalayer"));
  hookGlobal("uetq", watchArray("uetq"));
  hookGlobal("gtag", function (v) { return wrapFn(v, "gtag"); });
  hookGlobal("fbq", function (v) { return wrapFn(v, "fbq"); });

  // Page names a script can't reach through window: a top-level
  // `const dataLayer = []` is a lexical binding, found only by name.
  function peekDataLayer() {
    try {
      return dataLayer; // the page's global, whatever declared it
    } catch (e) {
      return undefined;
    }
  }

  // Arrays whose push couldn't be watched are read by their length.
  const polled = [];

  let tcfDone = false;
  let gppDone = false;

  function hookTCF() {
    let f;
    try { f = W.__tcfapi; } catch (e) { return; }
    if (typeof f !== "function") return;
    tcfDone = true;
    try {
      f("addEventListener", 2, function (d, ok) {
        if (!ok || !d) return;
        const ev = String(d.eventStatus || "");
        if (ev !== "tcloaded" && ev !== "cmpuishown" && ev !== "useractioncomplete") return;
        const rec = { t: now(), src: "tcf", event: ev };
        if (typeof d.tcString === "string") rec.tcString = apply(strSlice, d.tcString, [0, 3000]);
        if (typeof d.gdprApplies === "boolean") rec.gdprApplies = d.gdprApplies;
        send(rec);
      });
    } catch (e) { /* the CMP refused */ }
  }

  function hookGPP() {
    let f;
    try { f = W.__gpp; } catch (e) { return; }
    if (typeof f !== "function") return;
    gppDone = true;
    try {
      f("addEventListener", function (d, ok) {
        if (ok === false || !d) return;
        const rec = { t: now(), src: "gpp", event: apply(strSlice, String(d.eventName || ""), [0, 64]) };
        const s = d.pingData && d.pingData.gppString;
        if (typeof s === "string") rec.gppString = apply(strSlice, s, [0, 3000]);
        send(rec);
      });
    } catch (e) { /* the CMP refused */ }
  }

  function poll() {
    const dl = peekDataLayer();
    if (dl && typeof dl === "object" && !apply(weakHas, pushWrapped, [dl])) {
      let known = false;
      for (let i = 0; i < polled.length; i++) if (polled[i].arr === dl) known = true;
      if (!known) {
        // Found by name, not through the setter: what it holds was never
        // reported.
        const n = isArray(dl) ? reportEntries(dl, "datalayer", 0) : 0;
        if (!wrapPush(dl, "datalayer")) apply(arrayPush, polled, [{ arr: dl, len: n }]);
      }
    }
    for (let i = 0; i < polled.length; i++) {
      polled[i].len = reportEntries(polled[i].arr, "datalayer", polled[i].len);
    }
    const names = ["gtag", "fbq"];
    for (let i = 0; i < names.length; i++) {
      // A function declaration replaced the accessor; it stays writable.
      try {
        const f = W[names[i]];
        if (typeof f === "function" && !apply(weakHas, fnWrapped, [f])) W[names[i]] = wrapFn(f, names[i]);
      } catch (e) { /* not writable */ }
    }
    try {
      const q = W.uetq;
      if (q && typeof q === "object" && !apply(weakHas, pushWrapped, [q])) watchArray("uetq")(q);
    } catch (e) { /* not readable */ }
    if (!tcfDone) hookTCF();
    if (!gppDone) hookGPP();
  }

  const started = now();
  const timer = apply(setIntervalFn, W, [function () {
    poll();
    if (now() - started > 30000) apply(clearIntervalFn, W, [timer]);
  }, 100]);

  try {
    apply(addListener, W, ["pointerdown", function (e) {
      if (e.isTrusted) send({ t: now(), src: "click" });
    }, { capture: true, passive: true }]);
  } catch (e) { /* no events in this context */ }
})
