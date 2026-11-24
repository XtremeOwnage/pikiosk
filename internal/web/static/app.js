"use strict";

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
let config = null;
let liveTimer = null;
let stateTimer = null;

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (res.status === 401 && !path.startsWith("/api/login")) {
    showAuth(false);
    throw new Error("login required");
  }
  const type = res.headers.get("Content-Type") || "";
  const data = type.includes("json") ? await res.json() : null;
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

function toast(msg, err = false) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = "toast" + (err ? " err" : "");
  t.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => (t.hidden = true), err ? 6000 : 2500);
}

async function act(fn, ok) {
  try {
    const r = await fn();
    if (ok) toast(ok);
    return r;
  } catch (e) {
    toast(e.message, true);
  }
}

function el(tag, text, cls) {
  const e = document.createElement(tag);
  if (text !== undefined && text !== null) e.textContent = text;
  if (cls) e.className = cls;
  return e;
}

function icon(name) {
  const s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  s.setAttribute("class", "ic");
  const u = document.createElementNS("http://www.w3.org/2000/svg", "use");
  u.setAttribute("href", "#i-" + name);
  s.append(u);
  return s;
}

function chip(text, cls = "", ic) {
  const c = el("span", null, "chip " + cls);
  if (ic) c.append(icon(ic));
  c.append(text);
  return c;
}

function tpl(id) {
  return $("#" + id).content.firstElementChild.cloneNode(true);
}

// --- auth ---

let authInfo = {};

async function boot() {
  const q = new URLSearchParams(location.search);
  const loginError = q.get("login_error");
  if (loginError) history.replaceState(null, "", location.pathname + location.hash);
  authInfo = await api("GET", "/api/auth");
  document.title = authInfo.device_name + " · pikiosk";
  if (authInfo.setup_required) return showAuth(true);
  if (!authInfo.authenticated) {
    showAuth(false);
    if (loginError) $("#auth-error").textContent = loginError;
    return;
  }
  startApp();
}

function showAuth(setup) {
  stopTimers();
  $("#app").hidden = true;
  $("#auth").hidden = false;
  $("#auth-title").textContent = authInfo.device_name || "pikiosk";
  $("#auth-hint").textContent = setup ? "Choose an admin password for this kiosk." : "";
  $("#auth-confirm-field").hidden = !setup;
  $("#auth-confirm").required = setup;
  $("#auth-password").autocomplete = setup ? "new-password" : "current-password";
  $("#auth-submit").textContent = setup ? "Save password" : "Log in";
  $("#auth-form").dataset.setup = setup ? "1" : "";
  const sso = !setup && authInfo.oidc;
  $("#auth-sso").hidden = !sso;
  if (sso) $("#auth-sso-btn span").textContent = authInfo.oidc.label;
  $("#auth-password").focus();
}

$("#auth-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const setup = !!e.target.dataset.setup;
  const pw = $("#auth-password").value;
  $("#auth-error").textContent = "";
  if (setup && pw !== $("#auth-confirm").value) {
    $("#auth-error").textContent = "Passwords do not match";
    return;
  }
  try {
    await api("POST", setup ? "/api/setup" : "/api/login", { password: pw });
    $("#auth-password").value = $("#auth-confirm").value = "";
    authInfo = await api("GET", "/api/auth");
    startApp();
  } catch (err) {
    $("#auth-error").textContent = err.message;
  }
});

$("#logout").addEventListener("click", async () => {
  await api("POST", "/api/logout");
  authInfo = await api("GET", "/api/auth");
  showAuth(false);
});

// --- app shell ---

function startApp() {
  $("#auth").hidden = true;
  $("#app").hidden = false;
  $("#whoami").textContent = authInfo.user && authInfo.user !== "admin" ? authInfo.user : "";
  loadConfig().then(() => selectTab(location.hash.slice(1) || "control"));
  refreshState();
  stopTimers();
  stateTimer = setInterval(refreshState, 3000);
}

function stopTimers() {
  clearInterval(stateTimer);
  clearInterval(liveTimer);
  stateTimer = liveTimer = null;
}

window.addEventListener("hashchange", () => !$("#app").hidden && selectTab(location.hash.slice(1)));

function selectTab(tab) {
  if (!$(`[data-panel="${tab}"]`)) tab = "control";
  if (location.hash !== "#" + tab) history.replaceState(null, "", "#" + tab);
  $$("#tabs a").forEach((b) => b.classList.toggle("active", b.dataset.tab === tab));
  $$("[data-panel]").forEach((p) => (p.hidden = p.dataset.panel !== tab));
  clearInterval(liveTimer);
  liveTimer = null;
  if (tab === "control") startLive();
  if (tab === "network") {
    refreshNetwork();
    liveTimer = setInterval(() => !document.hidden && refreshNetwork(), 4000);
  }
  if (tab === "system") loadSystem();
  if (tab === "sounds") loadSounds();
  window.scrollTo(0, 0);
}

async function loadConfig() {
  config = await api("GET", "/api/config");
  $("#device-name").textContent = config.device_name;
  renderViews();
  fillForm($("#mqtt-form"), config.mqtt);
  fillForm($("#display-form"), config.display);
  fillForm($("#sleep-form"), config.sleep);
  fillWeekdays(config.sleep.schedule_weekdays || []);
  fillForm($("#general-form"), config);
  fillForm($("#cast-form"), config.cast || {});
  fillOIDC();
  renderUpdateSettings();
  renderTopics();
  if (!rotationsDirty) renderRotations();
  $("#volume-save").checked = (config.audio || {}).volume >= 0;
}

function fillForm(form, obj) {
  for (const e of form.elements) {
    if (!e.name || !(e.name in obj)) continue;
    if (e.type === "checkbox") e.checked = !!obj[e.name];
    else e.value = obj[e.name] ?? "";
  }
}

function readForm(form, template) {
  const out = {};
  for (const e of form.elements) {
    if (!e.name || !(e.name in template)) continue;
    const t = typeof template[e.name];
    if (e.type === "checkbox") out[e.name] = e.checked;
    else if (t === "number") out[e.name] = e.value === "" ? 0 : Number(e.value);
    else out[e.name] = e.value;
  }
  return out;
}

function fillSelect(select, names, empty, extra = []) {
  const cur = select.value;
  if (!names.length && !extra.length) {
    select.replaceChildren(new Option(empty, ""));
    select.disabled = true;
    return;
  }
  select.disabled = false;
  select.replaceChildren(...extra.map(([l, v]) => new Option(l, v)), ...names.map((n) => new Option(n, n)));
  if ([...select.options].some((o) => o.value === cur)) select.value = cur;
}

// --- control ---

function tile(k, v, s, cls = "") {
  const t = el("div", null, "tile " + cls);
  t.append(el("div", k, "k"), el("div", v, "v"));
  if (s) t.append(el("div", s, "s"));
  t.title = [v, s].filter(Boolean).join("\n");
  return t;
}

async function refreshState() {
  let s;
  try {
    s = await api("GET", "/api/state");
  } catch {
    setBrand(false, "offline");
    return;
  }
  const st = s.state;
  const dev = s.device || {};
  const browser = s.browser.connected ? "connected" : s.browser.running ? "starting" : "stopped";
  setBrand(s.browser.connected, st.screen ? "screen on" : "screen off");
  const showing = st.casting ? (st.cast_source === "youtube" ? "YouTube" : "AirPlay") : st.view || (st.mode === "stream" ? "Stream" : st.url ? "URL" : "—");
  const tiles = [
    tile("Showing", showing, st.casting ? "a device is casting" : st.url || "", "wide"),
    tile("Rotation", st.rotation || "—", st.rotation ? (st.rotation_active ? `running, step ${st.rotation_index + 1}` : "stopped") : ""),
    tile("Screen", st.screen ? "On" : "Off", [st.screen_reason, st.brightness >= 0 ? st.brightness + "% brightness" : ""].filter(Boolean).join(" · ")),
    tile("Reverts", st.revert_at ? new Date(st.revert_at).toLocaleTimeString() : "—", st.revert_at ? "back to rotation or default" : ""),
    tile("Browser", browser, [s.player.playing ? "stream playing" : "", s.browser.background_tabs ? `${s.browser.background_tabs} background tab${s.browser.background_tabs > 1 ? "s" : ""}` : ""].filter(Boolean).join(" · ")),
    tile("MQTT", s.mqtt, ""),
  ];
  if (st.sound) tiles.push(tile("Sound", st.sound, ""));
  if (dev.cpu_temp_c !== undefined) tiles.push(tile("Pi", `${dev.cpu_temp_c} °C`, `load ${dev.load ?? "?"}`));
  if (s.browser.last_error) tiles.push(tile("Browser error", s.browser.last_error, "", "wide err"));
  if (s.player.last_error) tiles.push(tile("Stream error", s.player.last_error, "", "wide err"));
  $("#status").replaceChildren(...tiles);

  const vol = $("#volume");
  if (document.activeElement !== vol && st.volume >= 0) vol.value = st.volume;
  $("#volume-val").textContent = st.volume >= 0 ? st.volume + "%" : "";
  loadEvents();
  const b = $("#ctl-brightness");
  if (document.activeElement !== b) {
    b.classList.toggle("unset", st.brightness < 0);
    if (st.brightness >= 0) b.value = st.brightness;
    $("#ctl-brightness-val").textContent = st.brightness >= 0 ? st.brightness + "%" : "not set";
  }
  const c = s.cast || {};
  const cs = $("#cast-status");
  const src = st.cast_source;
  cs.textContent = src === "youtube" ? "casting YouTube" : !c.available ? "AirPlay not installed" : !c.enabled ? "AirPlay off" : c.active ? "casting AirPlay" : c.running ? "ready" : "starting";
  cs.className = "badge " + (src || (c.enabled && c.running) ? "ok" : c.enabled ? "bad" : "");
  $("#cast-stop").hidden = src !== "youtube";
  const yt = s.youtube || {};
  let ytText = "";
  if (yt.enabled && yt.last_search) {
    ytText = "Last phone search: " + yt.search_from + ", " + new Date(yt.last_search).toLocaleString() + ".";
  } else if (yt.enabled) {
    ytText = "No phone has found the kiosk on this network. Use Link with TV code.";
  }
  $("#yt-discovery").textContent = ytText;
  cs.title = c.last_error || "";
  renderUpdate(s.update || {});
  const ms = $("#mqtt-status");
  ms.textContent = s.mqtt;
  ms.className = "badge " + (s.mqtt === "connected" ? "ok" : s.mqtt === "disabled" ? "" : "bad");
}

function setBrand(ok, text) {
  $("#brand-status .dot").className = "dot " + (ok ? "ok" : "bad");
  $("#brand-status span").textContent = text;
}

function duration() {
  return Number($("#ctl-duration").value) || 0;
}

$("#ctl-show").addEventListener("click", () =>
  act(() => api("POST", "/api/view", { name: $("#ctl-view").value, duration: duration() }), "Showing view").then(refreshState));
$("#ctl-open").addEventListener("click", () => {
  const url = $("#ctl-url").value.trim();
  if (!url) return;
  const stream = /^(rtsps?|rtmp|srt|udp|rtp):/i.test(url);
  act(() => api("POST", stream ? "/api/stream" : "/api/url", { url, duration: duration() }), "Opened").then(refreshState);
});
$("#ctl-reload").addEventListener("click", () => act(() => api("POST", "/api/reload"), "Reloaded"));
$("#ctl-stop").addEventListener("click", () => act(() => api("POST", "/api/stream/stop"), "Stopped").then(refreshState));
$("#ctl-screen-on").addEventListener("click", () => act(() => api("POST", "/api/screen", { on: true })).then(refreshState));
$("#ctl-screen-off").addEventListener("click", () => act(() => api("POST", "/api/screen", { on: false })).then(refreshState));
$("#ctl-restart").addEventListener("click", () => act(() => api("POST", "/api/browser/restart"), "Restarting browser"));
$("#ctl-brightness").addEventListener("input", (e) => {
  e.target.classList.remove("unset");
  $("#ctl-brightness-val").textContent = e.target.value + "%";
});
$("#ctl-brightness").addEventListener("change", (e) =>
  act(() => api("POST", "/api/brightness", { value: Number(e.target.value) }), "Brightness set"));

function startLive() {
  clearInterval(liveTimer);
  shot();
  liveTimer = setInterval(() => $("#remote-live").checked && !document.hidden && shot(), 2000);
}

let shotURL = null;
async function shot() {
  try {
    const res = await fetch("/api/screenshot", { cache: "no-store" });
    if (!res.ok) {
      $("#shot").hidden = true;
      $("#shot-msg").hidden = false;
      $("#shot-msg").textContent = res.status === 503 ? "No preview: the browser is not running" : "No preview";
      return;
    }
    const url = URL.createObjectURL(await res.blob());
    $("#shot").src = url;
    $("#shot").hidden = false;
    $("#shot-msg").hidden = true;
    if (shotURL) URL.revokeObjectURL(shotURL);
    shotURL = url;
  } catch {}
}

function pos(e) {
  const r = e.target.getBoundingClientRect();
  return { x: (e.clientX - r.left) / r.width, y: (e.clientY - r.top) / r.height };
}

$("#shot").addEventListener("click", (e) => {
  act(() => api("POST", "/api/input", { type: "click", ...pos(e) })).then(() => setTimeout(shot, 400));
});
$("#shot").addEventListener("wheel", (e) => {
  e.preventDefault();
  act(() => api("POST", "/api/input", { type: "scroll", ...pos(e), dy: e.deltaY })).then(() => setTimeout(shot, 300));
}, { passive: false });
$("#remote-send").addEventListener("click", sendText);
$("#remote-text").addEventListener("keydown", (e) => e.key === "Enter" && sendText());
async function sendText() {
  const t = $("#remote-text");
  if (!t.value) return;
  await act(() => api("POST", "/api/input", { type: "text", text: t.value }));
  t.value = "";
  setTimeout(shot, 300);
}
$$("[data-key]").forEach((b) => b.addEventListener("click", () =>
  act(() => api("POST", "/api/input", { type: "key", key: b.dataset.key })).then(() => setTimeout(shot, 300))));
$$("[data-scroll]").forEach((b) => b.addEventListener("click", () =>
  act(() => api("POST", "/api/input", { type: "scroll", x: 0.5, y: 0.5, dy: Number(b.dataset.scroll) })).then(() => setTimeout(shot, 300))));
$("#clear-browser").addEventListener("click", () => {
  if (confirm("Clear all cookies and site data?"))
    act(() => api("POST", "/api/browser/clear"), "Browser data cleared");
});

// --- views ---

const AUTH_LABELS = { basic: "Basic auth", bearer: "Bearer token", home_assistant: "HA token" };

function viewChips(v) {
  const out = [];
  out.push(chip(v.type === "stream" ? "Stream" : "Web"));
  if (v.name === config.default_view) out.push(chip("Default", "accent", "star"));
  if (v.disabled) out.push(chip("Skipped in rotations", "warn"));
  if (v.auth && v.auth.type) out.push(chip(AUTH_LABELS[v.auth.type] || v.auth.type, "", "lock"));
  if (v.headers && v.headers.length) out.push(chip(v.headers.length + (v.headers.length === 1 ? " header" : " headers")));
  if (v.css) out.push(chip("CSS"));
  if (v.zoom && v.zoom !== 1) out.push(chip(Math.round(v.zoom * 100) + "%"));
  if (v.refresh_seconds) out.push(chip("reload " + fmtSeconds(v.refresh_seconds)));
  if (v.keep_loaded) out.push(chip("kept loaded"));
  return out;
}

function fmtSeconds(s) {
  if (!s) return "0s";
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  return [h && h + "h", m && m + "m", sec && sec + "s"].filter(Boolean).join(" ");
}

function renderViews() {
  const views = config.views || [];
  $("#views-list").replaceChildren(...views.map((v, i) => {
    const row = tpl("view-item");
    row.classList.toggle("off", !!v.disabled);
    $("[data-f=name]", row).textContent = v.name;
    $("[data-f=url]", row).textContent = v.url;
    $("[data-f=url]", row).title = v.url;
    $("[data-f=chips]", row).replaceChildren(...viewChips(v));
    const sw = $("[data-f=enabled]", row);
    sw.checked = !v.disabled;
    sw.addEventListener("change", async () => {
      const ok = await act(() => api("POST", sw.checked ? "/api/view/enable" : "/api/view/disable", { name: v.name }),
        sw.checked ? `${v.name} is back in rotations` : `${v.name} is skipped in rotations`);
      if (!ok) sw.checked = !sw.checked;
      await loadConfig();
    });
    $("[data-act=show]", row).addEventListener("click", () =>
      act(() => api("POST", "/api/view", { name: v.name }), "Showing " + v.name).then(refreshState));
    $("[data-act=edit]", row).addEventListener("click", () => openEditor(i));
    $("[data-act=up]", row).disabled = i === 0;
    $("[data-act=down]", row).disabled = i === views.length - 1;
    $("[data-act=up]", row).addEventListener("click", () => moveView(i, -1));
    $("[data-act=down]", row).addEventListener("click", () => moveView(i, 1));
    return row;
  }));
  $("#views-empty").hidden = views.length > 0;
  $("#views-list").hidden = views.length === 0;
  const names = views.map((v) => v.name);
  fillSelect($("#views-default"), names, "(no views yet)", [["(none)", ""]]);
  $("#views-default").value = config.default_view || "";
  fillSelect($("#ctl-view"), names, "(no views yet)");
  $("#ctl-show").disabled = !names.length;
}

async function saveViews(views, defaultView = config.default_view, renames) {
  const body = { views, default_view: defaultView };
  if (renames && Object.keys(renames).length) body.renames = renames;
  await api("PUT", "/api/config/views", body);
  await loadConfig();
}

async function moveView(i, d) {
  const views = [...config.views];
  const [v] = views.splice(i, 1);
  views.splice(i + d, 0, v);
  await act(() => saveViews(views));
}

$("#views-default").addEventListener("change", (e) => act(() => saveViews(config.views, e.target.value), "Default view saved"));

// View editor drawer.

let editIndex = -1;
const ve = $("#ve-form");

function addHeaderRow(h = { name: "", value: "" }) {
  const r = tpl("header-row");
  $("[data-f=name]", r).value = h.name;
  $("[data-f=value]", r).value = h.value;
  $("[data-act=del]", r).addEventListener("click", () => r.remove());
  $("#ve-headers").append(r);
}

function syncEditor() {
  const type = ve.type.value || "web";
  const web = type === "web";
  $("#ve-web").hidden = !web;
  $("#ve-stream").hidden = web;
  $("#ve-url-hint").textContent = web ? "http:// or https:// page." : "rtsp://, rtmp://, srt:// or an HLS/HTTP video URL.";
  ve.url.placeholder = web ? "https://dashboard.lan/" : "rtsp://camera.lan/stream";
  const t = ve.auth_type.value;
  $$("[data-auth]", ve).forEach((e) => {
    const k = e.dataset.auth;
    e.hidden = !(k === t || (k === "token" && (t === "bearer" || t === "home_assistant")));
  });
}

function openEditor(i) {
  editIndex = i;
  const v = i >= 0 ? config.views[i] : { name: "", type: "web", url: "" };
  $("#ve-title").textContent = i >= 0 ? "Configure " + v.name : "New view";
  $("#ve-error").textContent = "";
  ve.name.value = v.name;
  ve.type.value = v.type || "web";
  ve.url.value = v.url;
  ve.enabled.checked = !v.disabled;
  ve.default.checked = i >= 0 && config.default_view === v.name;
  const a = v.auth || {};
  ve.auth_type.value = a.type || "";
  ve.auth_username.value = a.username || "";
  ve.auth_password.value = a.password || "";
  ve.auth_token.value = a.token || "";
  ve.zoom.value = v.zoom || "";
  ve.refresh_seconds.value = v.refresh_seconds || "";
  ve.css.value = v.css || "";
  ve.keep_loaded.checked = !!v.keep_loaded;
  $("#ve-headers").replaceChildren();
  (v.headers || []).forEach(addHeaderRow);
  $("[data-act=delete]", ve).hidden = i < 0;
  syncEditor();
  $("#view-editor").showModal();
  (i >= 0 ? ve.url : ve.name).focus();
}

function readEditor() {
  const type = ve.type.value || "web";
  const v = { name: ve.name.value.trim(), type, url: ve.url.value.trim(), disabled: !ve.enabled.checked };
  if (type === "web") {
    const t = ve.auth_type.value;
    if (t === "basic") v.auth = { type: t, username: ve.auth_username.value.trim(), password: ve.auth_password.value };
    else if (t) v.auth = { type: t, token: ve.auth_token.value.trim() };
    const headers = $$("#ve-headers .header-row")
      .map((r) => ({ name: $("[data-f=name]", r).value.trim(), value: $("[data-f=value]", r).value }))
      .filter((h) => h.name);
    if (headers.length) v.headers = headers;
    if (ve.css.value.trim()) v.css = ve.css.value;
    if (Number(ve.zoom.value)) v.zoom = Number(ve.zoom.value);
    if (Number(ve.refresh_seconds.value)) v.refresh_seconds = Number(ve.refresh_seconds.value);
    if (ve.keep_loaded.checked) v.keep_loaded = true;
  }
  return v;
}

ve.addEventListener("change", (e) => (e.target.name === "type" || e.target.name === "auth_type") && syncEditor());
$("#ve-header-add").addEventListener("click", () => addHeaderRow());
$$("[data-act=close]", ve).forEach((b) => b.addEventListener("click", () => $("#view-editor").close()));
$("#view-editor").addEventListener("click", (e) => e.target === e.currentTarget && $("#view-editor").close());
$("#view-add").addEventListener("click", () => openEditor(-1));

ve.addEventListener("submit", async (e) => {
  e.preventDefault();
  const v = readEditor();
  const err = $("#ve-error");
  if (!v.name) return (err.textContent = "Give the view a name.");
  if (!v.url) return (err.textContent = "Enter a URL.");
  const views = [...config.views];
  const renames = {};
  if (editIndex >= 0) {
    const old = views[editIndex].name;
    if (old !== v.name) renames[old] = v.name;
    views[editIndex] = v;
  } else {
    views.push(v);
  }
  let def = config.default_view;
  if (ve.default.checked) def = v.name;
  else if (def === v.name || (editIndex >= 0 && def === config.views[editIndex].name)) def = "";
  try {
    await saveViews(views, def, renames);
    $("#view-editor").close();
    toast(editIndex >= 0 ? "View saved" : "View added");
  } catch (ex) {
    err.textContent = ex.message;
  }
});

$("[data-act=delete]", ve).addEventListener("click", async () => {
  const v = config.views[editIndex];
  const used = (config.rotations || []).filter((r) => r.items.some((it) => it.view === v.name)).map((r) => r.name);
  if (used.length) {
    $("#ve-error").textContent = `${v.name} is used by ${used.join(", ")}. Remove it there first, or switch it off instead.`;
    return;
  }
  if (!confirm(`Delete the view ${v.name}?`)) return;
  const views = config.views.filter((_, i) => i !== editIndex);
  try {
    await saveViews(views, config.default_view === v.name ? "" : config.default_view);
    $("#view-editor").close();
    toast("View deleted");
  } catch (ex) {
    $("#ve-error").textContent = ex.message;
  }
});

// --- rotations ---

let rotationsDirty = false;

function setRotationsDirty(d) {
  rotationsDirty = d;
  $("#rotations-savebar").hidden = !d;
}

$("#rot-start").addEventListener("click", () =>
  act(() => api("POST", "/api/rotation", { name: $("#ctl-rotation").value }), "Rotation started").then(refreshState));
$("#rot-resume").addEventListener("click", () => act(() => api("POST", "/api/rotation/resume"), "Rotation resumed").then(refreshState));
$("#rot-next").addEventListener("click", () => act(() => api("POST", "/api/rotation/next")).then(refreshState));
$("#rot-stop").addEventListener("click", () => act(() => api("POST", "/api/rotation/stop"), "Rotation stopped").then(refreshState));

function renderRotations() {
  $("#rotations").replaceChildren();
  for (const r of config.rotations || []) addRotationCard(r);
  syncRotations();
  setRotationsDirty(false);
}

function viewOptions(select, value) {
  const views = config.views || [];
  select.replaceChildren(...views.map((v) => new Option(v.name, v.name)));
  if (value && !views.some((v) => v.name === value)) select.append(new Option(value + " (missing)", value));
  if (value) select.value = value;
}

function syncStep(li) {
  const name = $("[data-f=view]", li).value;
  const v = (config.views || []).find((x) => x.name === name);
  const on = $("[data-f=enabled]", li).checked;
  li.classList.toggle("off", !on || !!(v && v.disabled));
  $("[data-f=note]", li).textContent = !on ? "skipped" : v && v.disabled ? "view is switched off" : "";
}

function addRotationItem(list, item = { view: "", seconds: 30 }) {
  const li = tpl("rotation-item");
  viewOptions($("[data-f=view]", li), item.view);
  $("[data-f=seconds]", li).value = item.seconds;
  $("[data-f=enabled]", li).checked = !item.disabled;
  $("[data-act=del]", li).addEventListener("click", () => { li.remove(); syncRotations(); });
  $("[data-act=up]", li).addEventListener("click", () => { li.previousElementSibling && list.insertBefore(li, li.previousElementSibling); syncRotations(); });
  $("[data-act=down]", li).addEventListener("click", () => { li.nextElementSibling && list.insertBefore(li.nextElementSibling, li); syncRotations(); });
  li.addEventListener("change", () => syncStep(li));
  list.append(li);
  syncStep(li);
}

function addRotationCard(r = { name: "", items: [] }) {
  const card = tpl("rotation-card");
  const list = $(".steps", card);
  $("[data-f=name]", card).value = r.name;
  for (const it of r.items) addRotationItem(list, it);
  $("[data-act=add-item]", card).addEventListener("click", () => { addRotationItem(list); syncRotations(); setRotationsDirty(true); });
  $("[data-act=del]", card).addEventListener("click", () => {
    if (r.name && !confirm(`Delete the rotation ${r.name}?`)) return;
    card.remove();
    syncRotations();
    setRotationsDirty(true);
  });
  $("[data-act=start]", card).addEventListener("click", () => {
    if (rotationsDirty) return toast("Save your changes first", true);
    act(() => api("POST", "/api/rotation", { name: $("[data-f=name]", card).value }), "Rotation started").then(refreshState);
  });
  $("#rotations").append(card);
  return card;
}

function readRotations() {
  return $$("#rotations .rotation").map((card) => ({
    name: $("[data-f=name]", card).value.trim(),
    items: $$(".step", card).map((li) => {
      const it = { view: $("[data-f=view]", li).value, seconds: Number($("[data-f=seconds]", li).value) || 0 };
      if (!$("[data-f=enabled]", li).checked) it.disabled = true;
      return it;
    }),
  }));
}

function syncRotations() {
  const cards = $$("#rotations .rotation");
  $("#rotations-empty").hidden = cards.length > 0;
  for (const card of cards) {
    $$(".step", card).forEach((li, i) => {
      $(".num", li).textContent = i + 1;
      $("[data-act=up]", li).disabled = i === 0;
      $("[data-act=down]", li).disabled = !li.nextElementSibling;
    });
    const steps = $$(".step", card);
    const active = steps.filter((li) => !li.classList.contains("off"));
    const total = active.reduce((n, li) => n + (Number($("[data-f=seconds]", li).value) || 0), 0);
    const chips = [chip(`${active.length} of ${steps.length} steps`), chip(fmtSeconds(total) + " cycle")];
    if ($("[data-f=name]", card).value.trim() === config.default_rotation) chips.unshift(chip("Starts at boot", "accent", "star"));
    $("[data-f=chips]", card).replaceChildren(...chips);
  }
  const names = readRotations().map((r) => r.name).filter(Boolean);
  const def = $("#rotations-default");
  const cur = rotationsDirty ? def.value : config.default_rotation;
  def.replaceChildren(new Option("(none — show the default view)", ""), ...names.map((n) => new Option(n, n)));
  def.value = names.includes(cur) ? cur : "";
  const saved = (config.rotations || []).map((r) => r.name);
  fillSelect($("#ctl-rotation"), saved, "(no rotations yet)");
  $("#rot-start").disabled = !saved.length;
}

$("#rotations").addEventListener("input", () => { setRotationsDirty(true); syncRotations(); });
$("#rotations").addEventListener("change", () => { setRotationsDirty(true); syncRotations(); });
$("#rotations-default").addEventListener("change", () => setRotationsDirty(true));
$("#rotation-add").addEventListener("click", () => {
  if (!(config.views || []).length) return toast("Add some views first", true);
  const card = addRotationCard();
  addRotationItem($(".steps", card));
  syncRotations();
  setRotationsDirty(true);
  $("[data-f=name]", card).focus();
});
$("#rotations-discard").addEventListener("click", () => renderRotations());
$("#rotations-save").addEventListener("click", async () => {
  const ok = await act(() => api("PUT", "/api/config/rotations", { rotations: readRotations(), default_rotation: $("#rotations-default").value }), "Rotations saved");
  if (ok) {
    setRotationsDirty(false);
    await loadConfig();
  }
});
window.addEventListener("beforeunload", (e) => rotationsDirty && e.preventDefault());

// --- sound & cast ---

async function loadSounds() {
  const list = await act(() => api("GET", "/api/sounds"));
  const ul = $("#sounds-list");
  if (!list || !list.length) return ul.replaceChildren(el("li", "No sounds yet", "muted"));
  ul.replaceChildren(...list.map((snd) => {
    const li = el("li");
    li.append(el("span", snd.name, "name"));
    if (snd.builtin) li.append(chip("built-in"));
    li.append(el("span", Math.max(1, Math.round(snd.size / 1024)) + " KiB", "muted small"));
    const play = el("button", null, "btn sm icon");
    play.title = "Play";
    play.setAttribute("aria-label", "Play " + snd.name);
    play.append(icon("play"));
    play.addEventListener("click", () => act(() => api("POST", "/api/sound/play", { name: snd.name })));
    li.append(play);
    if (!snd.builtin) {
      const del = el("button", null, "btn sm icon ghost danger");
      del.title = "Delete";
      del.setAttribute("aria-label", "Delete " + snd.name);
      del.append(icon("trash"));
      del.addEventListener("click", async () => {
        if (!confirm(`Delete ${snd.name}?`)) return;
        await act(() => api("DELETE", "/api/sounds/" + encodeURIComponent(snd.name)), "Deleted");
        loadSounds();
      });
      li.append(del);
    }
    return li;
  }));
}

$("#sound-upload input[type=file]").addEventListener("change", async (e) => {
  if (!e.target.files.length) return;
  const res = await fetch("/api/sounds", { method: "POST", headers: { "X-Pikiosk": "1" }, body: new FormData($("#sound-upload")) });
  const data = await res.json().catch(() => ({}));
  e.target.value = "";
  if (!res.ok) return toast(data.error || res.statusText, true);
  toast("Uploaded " + data.name);
  loadSounds();
});
$("#sound-play").addEventListener("click", () => act(() => api("POST", "/api/sound/play", { path: $("#sound-url").value.trim() })));
$("#sound-stop").addEventListener("click", () => act(() => api("POST", "/api/sound/stop")));
$("#volume").addEventListener("input", (e) => ($("#volume-val").textContent = e.target.value + "%"));
$("#volume").addEventListener("change", async (e) => {
  const v = Number(e.target.value);
  await act(() => api("POST", "/api/volume", { value: v }), `Volume ${v}%`);
  if ($("#volume-save").checked) await act(() => api("PUT", "/api/config/audio", { volume: v }));
});
$("#volume-save").addEventListener("change", (e) =>
  act(() => api("PUT", "/api/config/audio", { volume: e.target.checked ? Number($("#volume").value) : -1 }), "Saved"));

$("#yt-link").addEventListener("click", async () => {
  const b = $("#yt-link");
  b.disabled = true;
  $("#yt-code-box").hidden = false;
  $("#yt-code").textContent = "Opening YouTube on the kiosk…";
  try {
    const r = await api("POST", "/api/cast/youtube/link");
    $("#yt-code").textContent = r.code;
  } catch (e) {
    $("#yt-code").textContent = "";
    $("#yt-code-box").hidden = true;
    toast(e.message + ". Use the Remote's arrow keys: Settings → Link with TV code.", true);
  } finally {
    b.disabled = false;
    refreshState();
  }
});
$("#cast-stop").addEventListener("click", () => act(() => api("POST", "/api/cast/stop"), "Casting stopped").then(refreshState));
$("#cast-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  await act(() => api("PUT", "/api/config/cast", { ...(config.cast || {}), ...readForm(e.target, config.cast || {}) }), "Casting saved");
  await loadConfig();
});

// --- MQTT ---

$("#mqtt-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  await act(() => api("PUT", "/api/config/mqtt", readForm(e.target, config.mqtt)), "MQTT saved");
  await loadConfig();
});

$("#mqtt-republish").addEventListener("click", async () => {
  const r = await act(() => api("POST", "/api/mqtt/discovery"));
  if (r) toast("Published to " + r.topic);
});
$("#mqtt-clear").addEventListener("click", async () => {
  if (!confirm("Clear retained messages?")) return;
  const r = await act(() => api("POST", "/api/mqtt/clear"));
  if (r) toast(`Cleared ${r.topics.length} retained topics`);
});

function renderTopics() {
  const b = config.mqtt.base_topic;
  $("#mqtt-discovery-topic").textContent = `${config.mqtt.discovery_prefix}/device/${config.device_id}/config`;
  $("#mqtt-republish").disabled = !config.mqtt.discovery;
  $("#mqtt-topics").textContent = [
    `${b}/availability            online | offline (retained)`,
    `${b}/state                   JSON state (retained)`,
    `${b}/error                   last command error`,
    ``,
    `${b}/cmd/view                <view name>`,
    `${b}/cmd/url                 https://…`,
    `${b}/cmd/stream              rtsp://…`,
    `${b}/cmd/stop_stream         (any)`,
    `${b}/cmd/screen              ON | OFF`,
    `${b}/cmd/brightness          0-100`,
    `${b}/cmd/reload              (any)`,
    `${b}/cmd/restart_browser     (any)`,
    `${b}/cmd/reboot              (any)`,
    `${b}/cmd/enable_view         <view name>  (include in rotations)`,
    `${b}/cmd/disable_view        <view name>  (skip in rotations)`,
    `${b}/cmd/rotation            <rotation name>`,
    `${b}/cmd/resume_rotation     (any)`,
    `${b}/cmd/stop_rotation       (any)`,
    `${b}/cmd/next                (any)`,
    `${b}/cmd/play_sound          <sound name, library path or https URL>`,
    `${b}/cmd/stop_sound          (any)`,
    `${b}/cmd/volume              0-100`,
    `${b}/cmd/list_sounds         (any) -> ${b}/sounds`,
    `${b}/cmd                     {"action":"view","name":"cams","duration":60}`,
    `${b}/event                   activity feed (JSON, not retained)`,
    `${b}/sounds                  sound library (JSON, retained)`,
  ].join("\n");
}

// --- network ---

async function loadNetwork() {
  const list = $("#net-list");
  let ifs;
  try {
    ifs = await api("GET", "/api/network");
  } catch (e) {
    list.replaceChildren(el("div", e.message, "card error"));
    return;
  }
  list.replaceChildren(...(ifs || []).map(netCard));
}

function netCard(it) {
  const card = el("div", null, "card net");
  const head = el("div", null, "card-head");
  head.append(el("h2", it.device));
  head.append(chip(it.type));
  if (it.connection) head.append(chip(it.connection));
  const st = el("span", it.state, "badge " + (/^connected/.test(it.state) ? "ok" : ""));
  head.append(st);
  const kv = el("dl", null, "kv");
  for (const [k, v] of [["Addresses", (it.addresses || []).join(", ") || "—"], ["Gateway", it.gateway || "—"], ["DNS", (it.dns || []).join(", ") || "—"]])
    kv.append(el("dt", k), el("dd", v));
  card.append(head, kv);
  if (!it.connection) return card;

  const form = document.createElement("form");
  form.innerHTML = `
    <div class="grid-fields">
      <label class="field"><span>IPv4</span><select name="method"><option value="auto">DHCP</option><option value="manual">Static</option></select></label>
      <label class="field"><span>Address</span><input name="address" placeholder="192.168.1.50/24"></label>
      <label class="field"><span>Gateway</span><input name="gateway"></label>
      <label class="field"><span>DNS</span><input name="dns" placeholder="Comma separated"></label>
    </div>
    <div class="card-foot"><button class="btn primary">Apply</button></div>`;
  form.method.value = it.method === "manual" ? "manual" : "auto";
  form.address.value = it.method === "manual" ? (it.addresses || [])[0] || "" : "";
  form.gateway.value = it.method === "manual" ? it.gateway || "" : "";
  form.dns.value = (it.dns || []).join(", ");
  const sync = () => ["address", "gateway"].forEach((n) => (form[n].disabled = form.method.value !== "manual"));
  form.method.addEventListener("change", sync);
  sync();
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const body = {
      connection: it.connection,
      method: form.method.value,
      address: form.address.value.trim(),
      gateway: form.gateway.value.trim(),
      dns: form.dns.value.split(",").map((s) => s.trim()).filter(Boolean),
    };
    if (!confirm(`Apply to ${it.connection}?`)) return;
    await act(() => api("POST", "/api/network/ipv4", body), "Applying network settings");
    setTimeout(loadNetwork, 5000);
  });
  card.append(form);
  return card;
}

// Skips while a field on the page is being edited, so typing is not lost.
function refreshNetwork() {
  const a = document.activeElement;
  const typing = a && a.closest('[data-panel="network"]') &&
    (a.tagName === "SELECT" || a.tagName === "TEXTAREA" || (a.tagName === "INPUT" && a.type !== "checkbox"));
  if (typing) return;
  loadNetwork();
  loadWiFi();
}

async function loadWiFi() {
  let st;
  try {
    st = await api("GET", "/api/network/wifi");
  } catch {
    return;
  }
  $("#wifi-radio").checked = st.radio;
  $("#wifi-scan").disabled = false;
  $("#wifi-note").textContent = "";
  $("#wifi-saved-head").hidden = !st.saved.length;
  $("#wifi-saved").replaceChildren(...st.saved.map((n) => {
    const li = el("li");
    li.append(el("span", n.name, "name"));
    if (n.active) li.append(chip("connected" + (n.device ? " on " + n.device : ""), "accent"));
    const b = el("button", null, "btn sm danger ghost");
    b.append(icon("trash"), el("span", "Forget"));
    b.addEventListener("click", async () => {
      if (!confirm(`Forget ${n.name}?`)) return;
      await act(() => api("POST", "/api/network/wifi/forget", { name: n.name }), "Forgot " + n.name);
      loadWiFi();
      refreshNetwork();
    });
    li.append(b);
    return li;
  }));
}

$("#wifi-radio").addEventListener("change", async (e) => {
  const on = e.target.checked;
  if (!on && !confirm("Turn off Wi-Fi?")) {
    e.target.checked = true;
    return;
  }
  await act(() => api("POST", "/api/network/wifi/radio", { on }), on ? "Wi-Fi on" : "Wi-Fi off");
  loadWiFi();
  refreshNetwork();
});

$("#wifi-scan").addEventListener("click", async () => {
  const list = $("#wifi-list");
  list.replaceChildren(el("li", "Scanning…", "muted"));
  const nets = await act(() => api("POST", "/api/network/wifi/scan"));
  if (!nets || !nets.length) return list.replaceChildren(el("li", "No networks found", "muted"));
  list.replaceChildren(...nets.map((n) => {
    const li = el("li", null, "clickable");
    li.append(el("span", n.ssid, "name"));
    if (n.active) li.append(chip("connected", "accent"));
    li.append(el("span", `${n.signal}%`, "muted small"), chip(n.security || "open"));
    li.addEventListener("click", () => { $("#wifi-ssid").value = n.ssid; $("#wifi-pass").focus(); });
    return li;
  }));
});
$("#wifi-connect").addEventListener("click", async () => {
  await act(() => api("POST", "/api/network/wifi/connect", { ssid: $("#wifi-ssid").value, password: $("#wifi-pass").value }), "Connected");
  $("#wifi-pass").value = "";
  loadNetwork();
});

// --- display & sleep ---

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
function fillWeekdays(selected) {
  $("#weekdays").replaceChildren(...DAYS.map((d, i) => {
    const l = el("label");
    const c = document.createElement("input");
    c.type = "checkbox";
    c.value = i;
    c.checked = selected.includes(i);
    l.append(c, el("span", d));
    return l;
  }));
}

$("#display-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const d = readForm(e.target, config.display);
  d.rotation = Number(d.rotation);
  await act(() => api("PUT", "/api/config/display", d), "Display saved");
  await loadConfig();
});

$("#sleep-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const s = readForm(e.target, config.sleep);
  s.schedule_weekdays = $$("#weekdays input:checked").map((c) => Number(c.value));
  await act(() => api("PUT", "/api/config/sleep", s), "Sleep saved");
  await loadConfig();
});

// --- system ---

async function loadSystem() {
  const s = await act(() => api("GET", "/api/system"));
  if (!s) return;
  const i = s.info;
  const rows = [
    ["Hostname", i.hostname], ["Version", i.version], ["OS", i.os_version || "—"], ["Hardware", i.model || "—"],
    ["Addresses", (i.addresses || []).join(", ")], ["Uptime", i.uptime], ["Timezone", i.timezone],
  ];
  $("#sys-info").replaceChildren(...rows.flatMap(([k, v]) => [el("dt", k), el("dd", v || "—")]));
  $("#tz").value = i.timezone || "";
  $("#sys-rollback").hidden = !i.rollback;
  $("#api-token").value = "•".repeat(24);
  $("#api-token").dataset.token = s.api_token;
  $("#token-show").textContent = "Show";
}

function renderUpdate(u) {
  $("#upd-badge").hidden = !u.available;
  $("#upd-badge-ver").textContent = u.latest || "";
  const pill = $("#upd-badge-state");
  let cls = "", label;
  if (!u.enabled) label = "Checks off";
  else if (u.error) { cls = "warn"; label = "Check failed"; }
  else if (!u.checked_at) label = "Not checked yet";
  else if (u.available) { cls = "bad"; label = "Update available"; }
  else { cls = "ok"; label = "Up to date"; }
  pill.className = "status-pill " + cls;
  pill.textContent = label;
  $("#upd-now").hidden = !u.available;
  $("#upd-now span").textContent = u.available ? "Update now to " + u.latest : "Update now";
  $("#upd-check").disabled = !u.enabled;
  const parts = ["Installed " + u.current];
  if (u.latest) parts.push("latest release " + u.latest);
  if (u.error && u.enabled) parts.push("last check failed: " + u.error);
  if (u.checked_at && u.enabled) parts.push("checked " + new Date(u.checked_at).toLocaleString());
  if (u.last_auto) parts.push("last automatic update " + new Date(u.last_auto).toLocaleString());
  $("#upd-status").textContent = parts.join(", ") + ".";
  renderUpdateSettings();
}

function updateSettings() {
  return (config && config.update_check) || {};
}

function renderUpdateSettings() {
  const s = updateSettings();
  const on = !s.disabled;
  const sw = $("#upd-enabled"), auto = $("#upd-auto");
  if (document.activeElement !== sw) sw.checked = on;
  if (document.activeElement !== auto) auto.checked = on && !!s.auto;
  auto.disabled = !on;
  $("#upd-window").hidden = !(on && s.auto);
  if (document.activeElement !== $("#upd-from")) $("#upd-from").value = s.auto_from || "03:00";
  if (document.activeElement !== $("#upd-to")) $("#upd-to").value = s.auto_to || "05:00";
  const days = s.auto_days || [];
  if (!$("#upd-days").children.length) {
    $("#upd-days").replaceChildren(...DAYS.map((d, i) => {
      const l = el("label");
      const c = document.createElement("input");
      c.type = "checkbox";
      c.value = i;
      c.addEventListener("change", saveUpdateSettings);
      l.append(c, el("span", d));
      return l;
    }));
  }
  $$("#upd-days input").forEach((c) => (c.checked = days.includes(Number(c.value))));
  $("#upd-next").textContent = on && s.auto ? nextWindow(s) : "";
}

// nextWindow describes when the next automatic update can run.
function nextWindow(s) {
  const days = s.auto_days || [];
  const [fh, fm] = (s.auto_from || "03:00").split(":").map(Number);
  const now = new Date();
  for (let i = 0; i < 8; i++) {
    const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() + i, fh, fm);
    if ((days.length === 0 || days.includes(d.getDay())) && d > now) {
      const when = d.toLocaleString(undefined, { weekday: "long", hour: "2-digit", minute: "2-digit" });
      return "Next: " + when + ".";
    }
  }
  return "";
}

async function saveUpdateSettings() {
  const body = {
    disabled: !$("#upd-enabled").checked,
    auto: $("#upd-enabled").checked && $("#upd-auto").checked,
    auto_days: $$("#upd-days input:checked").map((c) => Number(c.value)),
    auto_from: $("#upd-from").value || "03:00",
    auto_to: $("#upd-to").value || "05:00",
  };
  const ok = await act(() => api("PUT", "/api/config/update_check", body), "Update settings saved");
  await loadConfig();
  if (ok) renderUpdate(await api("GET", "/api/update"));
}

["#upd-enabled", "#upd-auto", "#upd-from", "#upd-to"].forEach((s) => $(s).addEventListener("change", saveUpdateSettings));
$("#upd-now").addEventListener("click", () => $("#sys-update").click());

$("#upd-check").addEventListener("click", async () => {
  const u = await act(() => api("POST", "/api/update/check"));
  if (u) renderUpdate(u);
});

function fillOIDC() {
  const o = config.oidc || {};
  const f = $("#oidc-form");
  f.enabled.checked = !!o.enabled;
  f.issuer.value = o.issuer || "";
  f.client_id.value = o.client_id || "";
  f.client_secret.value = o.client_secret || "";
  f.allowed_groups.value = (o.allowed_groups || []).join(", ");
  f.button_label.value = o.button_label || "";
  f.redirect_url.value = o.redirect_url || "";
  f.redirect_url.placeholder = location.origin + "/api/oidc/callback";
}

$("#oidc-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = e.target;
  const body = {
    enabled: f.enabled.checked,
    issuer: f.issuer.value.trim(),
    client_id: f.client_id.value.trim(),
    client_secret: f.client_secret.value,
    allowed_groups: f.allowed_groups.value.split(",").map((s) => s.trim()).filter(Boolean),
    button_label: f.button_label.value.trim(),
    redirect_url: f.redirect_url.value.trim(),
  };
  await act(() => api("PUT", "/api/config/oidc", body), "Single sign-on saved");
  await loadConfig();
});
$("#oidc-copy").addEventListener("click", async () => {
  const f = $("#oidc-form");
  const v = f.redirect_url.value.trim() || f.redirect_url.placeholder;
  try {
    await navigator.clipboard.writeText(v);
    toast("Copied " + v);
  } catch {
    f.redirect_url.value = v;
    f.redirect_url.select();
  }
});

// --- backup & restore ---

const SECTION_LABELS = {
  views: "Views and rotations",
  sounds: "Uploaded sounds and boot volume",
  mqtt: "MQTT and Home Assistant discovery",
  display: "Display and sleep",
  cast: "AirPlay and YouTube casting",
  sso: "Single sign-on",
  updates: "Release check and automatic updates",
  device: "Device name and ID",
  access: "Admin password and API token",
};
let pendingBackup = null;

$("#backup-secrets").addEventListener("change", (e) => {
  $("#backup-download").href = "/api/backup" + (e.target.checked ? "" : "?secrets=0");
  $("#backup-secrets-note").textContent = e.target.checked
    ? "Keep the file somewhere safe."
    : "Secrets left out; a restore keeps the current ones.";
});

$("#backup-file").addEventListener("change", async (e) => {
  const f = e.target.files[0];
  e.target.value = "";
  if (!f) return;
  let b;
  try {
    b = JSON.parse(await f.text());
  } catch {
    return toast("That file is not a pikiosk backup", true);
  }
  if (b.format !== "pikiosk-backup") return toast("That file is not a pikiosk backup", true);
  pendingBackup = b;
  const c = b.config || {};
  const sameDevice = c.device_id === config.device_id;
  const rows = [
    ["File", f.name],
    ["From", (c.device_name || "?") + " (" + (c.device_id || "?") + ")"],
    ["Made", new Date(b.created).toLocaleString() + ", pikiosk " + (b.pikiosk_version || "?")],
    ["Contains", `${(c.views || []).length} views, ${(c.rotations || []).length} rotations, ${(b.sounds || []).length} sounds` + (b.secrets ? ", with secrets" : ", without secrets")],
  ];
  $("#backup-summary").replaceChildren(...rows.flatMap(([k, v]) => [el("dt", k), el("dd", v)]));
  $("#backup-sections").replaceChildren(...Object.entries(SECTION_LABELS).map(([key, label]) => {
    const l = el("label", null, "switch-label");
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.className = "switch";
    cb.value = key;
    // On another kiosk, keep its own identity and login by default.
    cb.checked = sameDevice || (key !== "device" && key !== "access");
    if (key === "access" && !b.secrets) { cb.checked = false; cb.disabled = true; }
    l.append(cb, " " + label);
    return l;
  }));
  $("#backup-hint").textContent = sameDevice ? "" : "From another kiosk: name, ID and login not selected.";
  $("#backup-preview").hidden = false;
});

$("#backup-restore").addEventListener("click", async () => {
  if (!pendingBackup) return;
  const sections = $$("#backup-sections input:checked").map((c) => c.value);
  if (!sections.length) return toast("Pick at least one part", true);
  if (!confirm("Restore " + sections.length + " parts?")) return;
  const r = await act(() => api("POST", "/api/backup/restore", { backup: pendingBackup, sections }));
  if (!r) return;
  toast(`Restored${r.sounds ? ", " + r.sounds + " sounds" : ""}`);
  pendingBackup = null;
  $("#backup-preview").hidden = true;
  if (sections.includes("access")) {
    authInfo = await api("GET", "/api/auth");
    if (!authInfo.authenticated) return showAuth(false);
  }
  await loadConfig();
  loadSystem();
});

$("#general-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = e.target;
  await act(() => api("PUT", "/api/config/general", {
    device_name: f.device_name.value, device_id: f.device_id.value, default_view: config.default_view,
  }), "Device saved");
  await loadConfig();
});

$("#password-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = e.target;
  const ok = await act(() => api("POST", "/api/password", { current: f.current.value, new: f.new.value }), "Password changed");
  if (ok) f.reset();
});

$("#tz-save").addEventListener("click", () => act(() => api("POST", "/api/system/timezone", { timezone: $("#tz").value.trim() }), "Timezone set"));
$("#token-show").addEventListener("click", (e) => {
  const t = $("#api-token");
  const shown = e.target.textContent === "Hide";
  t.value = shown ? "•".repeat(24) : t.dataset.token || "";
  e.target.textContent = shown ? "Show" : "Hide";
});
$("#token-new").addEventListener("click", async () => {
  if (!confirm("Regenerate the API token?")) return;
  const r = await act(() => api("POST", "/api/token"), "Token regenerated");
  if (r) { $("#api-token").value = r.api_token; $("#api-token").dataset.token = r.api_token; $("#token-show").textContent = "Hide"; }
});
$("#sys-reboot").addEventListener("click", () => confirm("Reboot the kiosk?") && act(() => api("POST", "/api/system/reboot"), "Rebooting"));
$("#sys-rollback").addEventListener("click", () =>
  confirm("Roll back?") && act(() => api("POST", "/api/system/rollback"), "Rolling back"));
$("#sys-update").addEventListener("click", async () => {
  if (!confirm("Update now?")) return;
  await act(() => api("POST", "/api/system/update"), "Update started");
  pollUpdate();
});

async function pollUpdate() {
  const log = $("#update-log");
  log.hidden = false;
  for (;;) {
    let st;
    try {
      st = await api("GET", "/api/system/update");
    } catch {
      await new Promise((r) => setTimeout(r, 5000));
      continue;
    }
    log.textContent = `${st.active_state} (${st.result})\n\n${st.log}`;
    log.scrollTop = log.scrollHeight;
    if (st.active_state !== "activating" && st.active_state !== "active") break;
    await new Promise((r) => setTimeout(r, 3000));
  }
}

// --- activity ---

async function loadEvents() {
  if ($('[data-panel="control"]').hidden) return;
  let evs;
  try {
    evs = await api("GET", "/api/events");
  } catch {
    return;
  }
  $("#events").replaceChildren(...(evs || []).slice().reverse().map((e) => {
    const li = el("li");
    li.append(el("time", new Date(e.time).toLocaleTimeString()), el("span", e.type, "t"), el("span", e.message, "m"));
    return li;
  }));
  if (!evs || !evs.length) $("#events").replaceChildren(el("li", "Nothing yet", "muted"));
}

boot().catch((e) => toast(e.message, true));
