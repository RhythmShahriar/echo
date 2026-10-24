// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Echo Inbox: the list, the detail pane and live updates. Plain DOM, no
// framework; every value from a message goes in with textContent, never
// as HTML. Message HTML only ever renders in the sandboxed frame.
(function () {
  "use strict";

  const API = "/ui/messages";
  const EVENTS = "/ui/events";
  const PAGE = 50;

  const $ = (id) => document.getElementById(id);
  const el = (tag, cls, text) => {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  };

  const state = {
    rows: [],          // summaries shown, newest first
    next: "",          // cursor for older messages
    selected: "",      // open message ID
    detail: null,      // the open message's detail
    tab: "",           // the open tab
    images: new Set(), // messages whose remote images may load
    fresh: new Set(),  // IDs to flash once
  };

  const filters = {
    channel: $("f-channel"),
    action: $("f-action"),
    provider: $("f-provider"),
    check: $("f-check"),
  };

  // query is the search box plus the filters, in the q= syntax.
  function query() {
    const parts = [$("q").value.trim()];
    for (const [key, sel] of Object.entries(filters)) {
      if (sel.value) parts.push(key + ":" + quote(sel.value));
    }
    return parts.filter(Boolean).join(" ");
  }

  function quote(v) {
    return /\s/.test(v) ? '"' + v + '"' : v;
  }

  async function getJSON(url) {
    const r = await fetch(url, { headers: { Accept: "application/json" } });
    if (r.status === 401) {
      showProblem("Echo needs its token: open /?token=<your token> once in this browser.");
      throw new Error("unauthorized");
    }
    if (!r.ok) throw new Error(r.status + " " + url);
    return r.json();
  }

  function showProblem(text) {
    $("empty").hidden = false;
    $("empty").textContent = text;
  }

  // load fetches the first page again. With keep, rows already loaded
  // further down stay (live updates must not lose the user's place).
  async function load(keep) {
    const q = query();
    const page = await getJSON(API + "?limit=" + PAGE + "&q=" + encodeURIComponent(q));
    if (q !== query()) return; // the search changed while this was in flight
    $("mode").textContent = page.mode === "observe" ? "observe" : "sealed";
    $("mode").title = page.mode === "observe" ? "Messages are recorded and sent for real" : "Nothing leaves; Echo answers for every service";
    fillFacets(page.facets);
    let rows = page.messages;
    let next = page.next;
    if (keep && state.rows.length > rows.length) {
      const seen = new Set(rows.map((r) => r.id));
      const lastNew = rows.length ? rows[rows.length - 1].at : "";
      const older = state.rows.filter((r) => !seen.has(r.id) && r.at <= lastNew);
      if (older.length) {
        rows = rows.concat(older);
        next = state.next;
      }
    }
    if (keep) {
      const before = new Set(state.rows.map((r) => r.id));
      for (const r of rows) if (!before.has(r.id)) state.fresh.add(r.id);
    }
    state.rows = rows;
    state.next = next;
    renderList(page);
  }

  async function loadMore() {
    if (!state.next) return;
    const page = await getJSON(API + "?limit=" + PAGE + "&before=" + encodeURIComponent(state.next) + "&q=" + encodeURIComponent(query()));
    const seen = new Set(state.rows.map((r) => r.id));
    state.rows = state.rows.concat(page.messages.filter((r) => !seen.has(r.id)));
    state.next = page.next;
    renderList(page);
  }

  function fillFacets(f) {
    const lists = { channel: f.channels, action: f.actions, provider: f.providers, check: f.checks };
    for (const [key, sel] of Object.entries(filters)) {
      const want = sel.value;
      const values = lists[key] || [];
      const have = Array.from(sel.options).slice(1).map((o) => o.value);
      if (have.join("\n") === values.join("\n")) continue;
      while (sel.options.length > 1) sel.remove(1);
      for (const v of values) sel.add(new Option(v, v));
      if (want && !values.includes(want)) sel.add(new Option(want, want));
      sel.value = want;
    }
  }

  function renderList(page) {
    const ul = $("rows");
    ul.replaceChildren(...state.rows.map(row));
    $("more").hidden = !state.next;
    const n = page.total;
    $("count").textContent = page.inbox === n ? n + " message" + (n === 1 ? "" : "s") : n + " of " + page.inbox;
    $("empty").hidden = state.rows.length > 0;
    if (!state.rows.length) {
      $("empty").textContent = page.inbox ? "No message matches this search." :
        "Nothing captured yet. Point a connector, the SMTP sink or the proxy at Echo and it shows up here.";
    }
    state.fresh.clear();
  }

  function row(s) {
    const li = el("li");
    li.dataset.id = s.id;
    if (s.id === state.selected) li.classList.add("sel");
    if (state.fresh.has(s.id)) li.classList.add("fresh");
    li.append(el("span", "r-kind", upper(s.kind) + (s.action ? " · " + s.action : "")));
    li.append(el("span", "r-time", when(s.at)));
    li.append(el("span", "r-title", s.title));
    const badges = el("span", "r-badges");
    if (s.errors) badges.append(el("span", "badge error", s.errors + " error" + (s.errors > 1 ? "s" : "")));
    if (s.warnings) badges.append(el("span", "badge warning", s.warnings + " warning" + (s.warnings > 1 ? "s" : "")));
    if (s.attachments) badges.append(el("span", "badge", s.attachments + (s.attachments > 1 ? " files" : " file")));
    badges.append(el("span", "badge " + s.status, s.status));
    li.append(badges);
    const to = (s.to || []).join(", ") + (s.more_to ? " +" + s.more_to : "");
    if (to || s.from) li.append(el("span", "r-to", (s.from ? s.from + " → " : "→ ") + (to || "?")));
    if (s.snippet && s.snippet !== s.title) li.append(el("span", "r-snip", s.snippet));
    return li;
  }

  // upper capitalizes a kind: check.Kind says "email" for plain mail.
  function upper(s) {
    return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
  }

  function when(at) {
    const d = new Date(at);
    const now = new Date();
    if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString();
    return d.toLocaleDateString() + " " + d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  async function select(id, keepTab) {
    state.selected = id;
    for (const li of $("rows").children) li.classList.toggle("sel", li.dataset.id === id);
    const li = $("rows").querySelector('li[data-id="' + CSS.escape(id) + '"]');
    if (li) li.scrollIntoView({ block: "nearest" });
    writeHash();
    let d;
    try {
      d = await getJSON(API + "/" + encodeURIComponent(id));
    } catch (e) {
      if (state.selected === id) closeDetail();
      return;
    }
    if (state.selected !== id) return;
    state.detail = d;
    renderDetail(d, keepTab);
  }

  function closeDetail() {
    state.selected = "";
    state.detail = null;
    $("detail").hidden = true;
    $("placeholder").hidden = false;
    writeHash();
  }

  function renderDetail(d, keepTab) {
    const m = d.message;
    $("placeholder").hidden = true;
    $("detail").hidden = false;
    $("d-kind").textContent = upper(d.kind) + " · " + m.status + (m.error ? ": " + m.error : "");
    $("d-title").textContent = d.title;
    const dl = $("d-fields");
    dl.replaceChildren();
    const field = (k, v) => {
      if (!v || (Array.isArray(v) && !v.length)) return;
      dl.append(el("dt", "", k), el("dd", "", Array.isArray(v) ? v.join(", ") : v));
    };
    field("From", m.from);
    field("To", m.to);
    field("Cc", m.cc);
    field("Bcc", m.bcc);
    field("Reply-To", m.reply_to);
    field("Event", m.event);
    field("URL", m.url ? (m.method ? m.method + " " : "") + m.url : "");
    field("Answered", m.response ? String(m.response.status) : "");
    field("Action", m.action ? (m.run ? m.run + " / " : "") + m.action : "");
    field("Via", [m.source && m.source.via, m.source && m.source.plugin, m.source && m.source.hook].filter(Boolean).join(" · "));
    field("At", new Date(m.at).toLocaleString());

    const has = {
      html: !!m.html,
      text: !!m.text,
      headers: m.headers && Object.keys(m.headers).length > 0,
      raw: !!d.raw,
      json: true,
      checks: true,
      attachments: d.attachments.length > 0,
    };
    for (const b of $("tabs").children) {
      const t = b.dataset.tab;
      b.hidden = !has[t];
      if (t === "checks") b.textContent = "Checks" + (d.issues.length ? " (" + d.issues.length + ")" : "");
      if (t === "attachments") b.textContent = "Attachments" + (d.attachments.length ? " (" + d.attachments.length + ")" : "");
    }
    $("tab-text").textContent = m.text || "";
    $("tab-raw").textContent = d.raw || "";
    $("tab-json").textContent = JSON.stringify(m, null, 2);
    renderHeaders(m.headers || {});
    renderChecks(d.issues);
    renderAttachments(d.attachments);
    let tab = keepTab && has[state.tab] ? state.tab : "";
    if (!tab) tab = ["html", "text", "raw", "json"].find((t) => has[t]);
    showTab(tab, keepTab && tab === state.tab);
  }

  function renderHeaders(h) {
    const t = $("headers");
    t.replaceChildren();
    for (const k of Object.keys(h).sort()) {
      const tr = el("tr");
      tr.append(el("td", "", k), el("td", "", h[k]));
      t.append(tr);
    }
  }

  function renderChecks(issues) {
    const ul = $("checks");
    ul.replaceChildren();
    if (!issues.length) {
      ul.append(el("li", "none", "No problems found in this message."));
      return;
    }
    for (const i of issues) {
      const li = el("li");
      li.append(el("span", "lvl " + i.level, i.level), el("strong", "", i.title));
      if (i.detail) li.append(el("div", "det", i.detail));
      if (i.fix) li.append(el("div", "fix", "Fix: " + i.fix));
      li.append(el("div", "det", i.check));
      ul.append(li);
    }
  }

  function renderAttachments(list) {
    const t = $("attachments");
    t.replaceChildren();
    for (const a of list) {
      const tr = el("tr");
      tr.append(el("td", "", a.type), el("td", "", a.name), el("td", "", size(a.size)));
      t.append(tr);
    }
  }

  function size(n) {
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    return (n / 1024 / 1024).toFixed(1) + " MB";
  }

  function showTab(tab, same) {
    state.tab = tab;
    for (const b of $("tabs").children) b.setAttribute("aria-selected", String(b.dataset.tab === tab));
    for (const b of $("tabs").children) $("tab-" + b.dataset.tab).hidden = b.dataset.tab !== tab;
    if (tab === "html") {
      const id = state.selected;
      $("images").checked = state.images.has(id);
      const src = API + "/" + encodeURIComponent(id) + "/html" + (state.images.has(id) ? "?images=1" : "");
      // Reloading the frame on a live update would flicker; only a new
      // message or the images toggle loads it again.
      if (!same || $("frame").dataset.src !== src) {
        $("frame").dataset.src = src;
        $("frame").src = src;
      }
    }
  }

  async function remove(id) {
    const r = await fetch(API + "/" + encodeURIComponent(id), { method: "DELETE" });
    if (!r.ok && r.status !== 404) return;
    const i = state.rows.findIndex((s) => s.id === id);
    state.rows = state.rows.filter((s) => s.id !== id);
    const next = state.rows[Math.min(i, state.rows.length - 1)];
    await load(true);
    if (next) select(next.id);
    else closeDetail();
  }

  function move(step) {
    if (!state.rows.length) return;
    let i = state.rows.findIndex((s) => s.id === state.selected);
    i = i < 0 ? 0 : Math.max(0, Math.min(state.rows.length - 1, i + step));
    select(state.rows[i].id);
    if (i === state.rows.length - 1 && state.next) loadMore();
  }

  function writeHash() {
    const h = new URLSearchParams();
    if ($("q").value) h.set("q", $("q").value);
    for (const [key, sel] of Object.entries(filters)) if (sel.value) h.set(key, sel.value);
    if (state.selected) h.set("id", state.selected);
    const s = h.toString();
    history.replaceState(null, "", s ? "#" + s : location.pathname);
  }

  function readHash() {
    const h = new URLSearchParams(location.hash.slice(1));
    $("q").value = h.get("q") || "";
    for (const [key, sel] of Object.entries(filters)) {
      const v = h.get(key);
      if (v) {
        sel.add(new Option(v, v));
        sel.value = v;
      }
    }
    return h.get("id") || "";
  }

  // Live updates: the stream says what changed; the page refetches the
  // first page (and the open message when it's the one that changed),
  // at most a few times a second however fast messages arrive.
  let pending = null;
  let changedOpen = false;
  function listen() {
    const es = new EventSource(EVENTS);
    es.onopen = () => $("live").classList.add("on");
    es.onerror = () => $("live").classList.remove("on");
    es.onmessage = (e) => {
      let ev;
      try {
        ev = JSON.parse(e.data);
      } catch (_) {
        return;
      }
      if (ev.id === state.selected) changedOpen = ev.type;
      if (pending) return;
      pending = setTimeout(async () => {
        pending = null;
        const open = changedOpen;
        changedOpen = false;
        await load(true).catch(() => {});
        if (open === "delete") closeDetail();
        else if (open && state.selected) select(state.selected, true);
      }, 300);
    };
    // A message deleted elsewhere (rhythm echo clear) sends no event to
    // this page yet; coming back to the tab catches up.
    document.addEventListener("visibilitychange", () => {
      if (!document.hidden) load(true).catch(() => {});
    });
  }

  let typing = null;
  $("q").addEventListener("input", () => {
    clearTimeout(typing);
    typing = setTimeout(() => { writeHash(); load(false).catch(() => {}); }, 250);
  });
  $("search").addEventListener("submit", (e) => {
    e.preventDefault();
    writeHash();
    load(false).catch(() => {});
  });
  for (const sel of Object.values(filters)) {
    sel.addEventListener("change", () => { writeHash(); load(false).catch(() => {}); });
  }
  $("rows").addEventListener("click", (e) => {
    const li = e.target.closest("li");
    if (li) select(li.dataset.id);
  });
  $("more").addEventListener("click", () => loadMore().catch(() => {}));
  $("tabs").addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (b) showTab(b.dataset.tab, false);
  });
  $("images").addEventListener("change", (e) => {
    if (e.target.checked) state.images.add(state.selected);
    else state.images.delete(state.selected);
    showTab("html", true);
  });
  $("d-delete").addEventListener("click", () => state.selected && remove(state.selected));
  document.addEventListener("keydown", (e) => {
    const typingIn = e.target.matches("input, select, textarea");
    if (e.key === "Escape" && typingIn) {
      e.target.blur();
      return;
    }
    if (typingIn || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "/") {
      e.preventDefault();
      $("q").focus();
    } else if (e.key === "j" || e.key === "ArrowDown") {
      e.preventDefault();
      move(1);
    } else if (e.key === "k" || e.key === "ArrowUp") {
      e.preventDefault();
      move(-1);
    } else if (e.key === "Delete" && state.selected) {
      remove(state.selected);
    }
  });

  const open = readHash();
  load(false).then(() => {
    const id = open || (state.rows[0] && state.rows[0].id);
    if (id) select(id);
  }).catch(() => {});
  listen();
})();
