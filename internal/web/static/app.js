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

// --- auth ---

async function boot() {
  const a = await api("GET", "/api/auth");
  document.title = a.device_name + " · pikiosk";
  if (a.setup_required) return showAuth(true);
  if (!a.authenticated) return showAuth(false);
  startApp();
}

function showAuth(setup) {
  stopTimers();
  $("#app").hidden = true;
  $("#auth").hidden = false;
  $("#auth-title").textContent = setup ? "Set up pikiosk" : "pikiosk";
  $("#auth-hint").textContent = setup ? "Choose an admin password for this kiosk." : "";
  $("#auth-confirm").hidden = !setup;
  $("#auth-confirm").required = setup;
  $("#auth-password").autocomplete = setup ? "new-password" : "current-password";
  $("#auth-submit").textContent = setup ? "Save password" : "Log in";
  $("#auth-form").dataset.setup = setup ? "1" : "";
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
    startApp();
  } catch (err) {
    $("#auth-error").textContent = err.message;
  }
});

$("#logout").addEventListener("click", async () => {
  await api("POST", "/api/logout");
  showAuth(false);
});

// --- app shell ---

function startApp() {
  $("#auth").hidden = true;
  $("#app").hidden = false;
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

$$("#tabs button").forEach((b) => b.addEventListener("click", () => selectTab(b.dataset.tab)));
window.addEventListener("hashchange", () => !$("#app").hidden && selectTab(location.hash.slice(1)));

function selectTab(tab) {
  if (!$(`[data-panel="${tab}"]`)) tab = "control";
  history.replaceState(null, "", "#" + tab);
  $$("#tabs button").forEach((b) => b.classList.toggle("active", b.dataset.tab === tab));
  $$("[data-panel]").forEach((p) => (p.hidden = p.dataset.panel !== tab));
  clearInterval(liveTimer);
  liveTimer = null;
  if (tab === "control") startLive();
  if (tab === "network") loadNetwork();
  if (tab === "system") loadSystem();
  if (tab === "sounds") loadSounds();
}

async function loadConfig() {
  config = await api("GET", "/api/config");
  $("#device-name").textContent = config.device_name;
  fillViews();
  fillForm($("#mqtt-form"), config.mqtt);
  fillForm($("#display-form"), config.display);
  fillForm($("#sleep-form"), config.sleep);
  fillWeekdays(config.sleep.schedule_weekdays || []);
  fillForm($("#general-form"), config);
  fillForm($("#cast-form"), config.cast || {});
  renderTopics();
  fillRotations();
  $("#volume-save").checked = (config.audio || {}).volume >= 0;
}

function fillForm(form, obj) {
  for (const el of form.elements) {
    if (!el.name || !(el.name in obj)) continue;
    if (el.type === "checkbox") el.checked = !!obj[el.name];
    else el.value = obj[el.name] ?? "";
  }
}

function readForm(form, template) {
  const out = {};
  for (const el of form.elements) {
    if (!el.name || !(el.name in template)) continue;
    const t = typeof template[el.name];
    if (el.type === "checkbox") out[el.name] = el.checked;
    else if (t === "number") out[el.name] = el.value === "" ? 0 : Number(el.value);
    else out[el.name] = el.value;
  }
  return out;
}

// --- control ---

async function refreshState() {
  let s;
  try {
    s = await api("GET", "/api/state");
  } catch {
    return;
  }
  const st = s.state;
  const rows = [
    ["Mode", st.mode],
    ["View", st.view || "—"],
    ["URL", st.url || "—"],
    ["Screen", (st.screen ? "on" : "off") + (st.screen_reason ? ` (${st.screen_reason})` : "")],
    ["Brightness", st.brightness >= 0 ? st.brightness + "%" : "not set"],
    ["Reverts", st.revert_at ? new Date(st.revert_at).toLocaleTimeString() : "—"],
    ["Browser", s.browser.connected ? "connected" : s.browser.running ? "starting" : "stopped"],
    ["MQTT", s.mqtt],
  ];
  if (s.browser.last_error) rows.push(["Browser error", s.browser.last_error]);
  if (s.player.last_error) rows.push(["Stream error", s.player.last_error]);
  rows.splice(6, 0,
    ["Rotation", st.rotation ? `${st.rotation} (${st.rotation_active ? "running, view " + (st.rotation_index + 1) : "stopped"})` : "—"],
    ["Sound", st.sound || "—"],
    ["Casting", st.casting ? "AirPlay" : "—"]);
  const dev = s.device || {};
  if (dev.cpu_temp_c !== undefined) rows.push(["CPU", `${dev.cpu_temp_c} °C, load ${dev.load ?? "?"}`]);
  $("#status").replaceChildren(...rows.flatMap(([k, v]) => [el("dt", k), el("dd", v)]));
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
  cs.textContent = !c.available ? "not installed" : !c.enabled ? "off" : c.active ? "casting" : c.running ? "ready" : "starting";
  cs.className = "badge " + (c.active || (c.enabled && c.running) ? "ok" : c.enabled ? "bad" : "");
  if (c.last_error) cs.title = c.last_error;
  const ms = $("#mqtt-status");
  ms.textContent = s.mqtt;
  ms.className = "badge " + (s.mqtt === "connected" ? "ok" : s.mqtt === "disabled" ? "" : "bad");
}

function el(tag, text) {
  const e = document.createElement(tag);
  if (text !== undefined) e.textContent = text;
  return e;
}

function duration() {
  return Number($("#ctl-duration").value) || 0;
}

$("#ctl-show").addEventListener("click", () =>
  act(() => api("POST", "/api/view", { name: $("#ctl-view").value, duration: duration() }), "Showing view").then(refreshState));
$("#ctl-open").addEventListener("click", () => {
  const url = $("#ctl-url").value.trim();
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
  if (confirm("Log out of every site and delete all cookies and site data on the kiosk?"))
    act(() => api("POST", "/api/browser/clear"), "Browser data cleared");
});

// --- views ---

function fillViews() {
  $("#views-body").replaceChildren();
  for (const v of config.views || []) addViewRow(v);
  syncViewSelects();
}

// Settings edited in the Customize dialog live on the row, not in inputs.
const viewExtras = new WeakMap();

function extrasOf(v) {
  return {
    auth: v.auth ? { ...v.auth } : {},
    headers: (v.headers || []).map((h) => ({ ...h })),
    css: v.css || "",
    zoom: v.zoom || 0,
    refresh_seconds: v.refresh_seconds || 0,
  };
}

function customized(x) {
  return !!(x.auth.type || x.headers.length || x.css || x.zoom || x.refresh_seconds);
}

function markCustomized(row) {
  const btn = $("[data-act=customize]", row);
  const on = customized(viewExtras.get(row));
  btn.classList.toggle("on", on);
  btn.textContent = on ? "Customize •" : "Customize";
}

function addViewRow(v = { name: "", type: "web", url: "" }) {
  const row = $("#view-row").content.firstElementChild.cloneNode(true);
  $("[data-f=enabled]", row).checked = !v.disabled;
  $("[data-f=name]", row).value = v.name;
  $("[data-f=type]", row).value = v.type || "web";
  $("[data-f=url]", row).value = v.url;
  viewExtras.set(row, extrasOf(v));
  markCustomized(row);
  $("[data-f=name]", row).addEventListener("input", syncViewSelects);
  $("[data-act=del]", row).addEventListener("click", () => { row.remove(); syncViewSelects(); });
  $("[data-act=up]", row).addEventListener("click", () => {
    if (row.previousElementSibling) row.parentNode.insertBefore(row, row.previousElementSibling);
    syncViewSelects();
  });
  $("[data-act=show]", row).addEventListener("click", () =>
    act(() => api("POST", "/api/view", { name: $("[data-f=name]", row).value }), "Showing view (save first if changed)"));
  $("[data-act=customize]", row).addEventListener("click", () => openCustomize(row));
  $("#views-body").append(row);
}

function readViews() {
  return $$("#views-body tr").map((row) => {
    const x = viewExtras.get(row);
    const type = $("[data-f=type]", row).value;
    const v = {
      name: $("[data-f=name]", row).value.trim(),
      type,
      url: $("[data-f=url]", row).value.trim(),
      disabled: !$("[data-f=enabled]", row).checked,
    };
    if (type === "web") {
      if (x.auth.type) v.auth = x.auth;
      if (x.headers.length) v.headers = x.headers;
      if (x.css) v.css = x.css;
      if (x.zoom) v.zoom = x.zoom;
      if (x.refresh_seconds) v.refresh_seconds = x.refresh_seconds;
    }
    return v;
  });
}

let czRow = null;

function addHeaderRow(h = { name: "", value: "" }) {
  const r = $("#header-row").content.firstElementChild.cloneNode(true);
  $("[data-f=name]", r).value = h.name;
  $("[data-f=value]", r).value = h.value;
  $("[data-act=del]", r).addEventListener("click", () => r.remove());
  $("#cz-headers").append(r);
}

function syncAuthFields() {
  const t = $("#customize-form").auth_type.value;
  $$("[data-auth]").forEach((e) => {
    const k = e.dataset.auth;
    e.hidden = !(k === t
      || (k === "token" && (t === "bearer" || t === "home_assistant"))
      || (k === "header" && t && t !== "home_assistant"));
  });
}

function openCustomize(row) {
  czRow = row;
  const x = viewExtras.get(row);
  const f = $("#customize-form");
  const web = $("[data-f=type]", row).value === "web";
  $("#cz-name").textContent = $("[data-f=name]", row).value || "view";
  $("#cz-web").hidden = !web;
  $("#cz-stream").hidden = web;
  f.auth_type.value = x.auth.type || "";
  f.auth_username.value = x.auth.username || "";
  f.auth_password.value = x.auth.password || "";
  f.auth_token.value = x.auth.token || "";
  f.zoom.value = x.zoom || "";
  f.refresh_seconds.value = x.refresh_seconds || "";
  f.css.value = x.css;
  $("#cz-headers").replaceChildren();
  x.headers.forEach(addHeaderRow);
  syncAuthFields();
  $("#customize").showModal();
}

$("#customize-form").auth_type.addEventListener("change", syncAuthFields);
$("#cz-header-add").addEventListener("click", () => addHeaderRow());
$("#customize").addEventListener("close", () => {
  const row = czRow;
  czRow = null;
  if ($("#customize").returnValue !== "ok" || !row) return;
  const f = $("#customize-form");
  const t = f.auth_type.value;
  const auth = !t ? {} : t === "basic"
    ? { type: t, username: f.auth_username.value.trim(), password: f.auth_password.value }
    : { type: t, token: f.auth_token.value.trim() };
  viewExtras.set(row, {
    auth,
    headers: $$("#cz-headers .row")
      .map((r) => ({ name: $("[data-f=name]", r).value.trim(), value: $("[data-f=value]", r).value }))
      .filter((h) => h.name),
    css: f.css.value,
    zoom: Number(f.zoom.value) || 0,
    refresh_seconds: Number(f.refresh_seconds.value) || 0,
  });
  markCustomized(row);
});

function fillSelect(select, names, empty) {
  const cur = select.value;
  if (!names.length) {
    select.replaceChildren(new Option(empty, ""));
    select.disabled = true;
    return;
  }
  select.disabled = false;
  select.replaceChildren(...names.map((n) => new Option(n, n)));
  if (names.includes(cur)) select.value = cur;
}

function syncViewSelects() {
  const names = readViews().map((v) => v.name).filter(Boolean);
  const def = $("#views-default");
  const cur = def.value || config.default_view;
  def.replaceChildren(new Option("(none)", ""), ...names.map((n) => new Option(n, n)));
  def.value = names.includes(cur) ? cur : "";
  const saved = (config.views || []).map((v) => v.name);
  fillSelect($("#ctl-view"), saved, "(no views yet)");
  $("#ctl-show").disabled = !saved.length;
}

$("#view-add").addEventListener("click", () => addViewRow());
$("#views-save").addEventListener("click", async () => {
  await act(() => api("PUT", "/api/config/views", { views: readViews(), default_view: $("#views-default").value }), "Views saved");
  await loadConfig();
});

$("#cast-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  await act(() => api("PUT", "/api/config/cast", readForm(e.target, config.cast || {})), "Casting saved");
  await loadConfig();
});

// --- MQTT ---

$("#mqtt-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  await act(() => api("PUT", "/api/config/mqtt", readForm(e.target, config.mqtt)), "MQTT saved");
  await loadConfig();
});

function renderTopics() {
  const b = config.mqtt.base_topic;
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
    list.textContent = e.message;
    return;
  }
  list.replaceChildren(...(ifs || []).map(netCard));
}

function netCard(it) {
  const div = el("div");
  div.className = "net";
  const head = el("p");
  head.append(el("strong", `${it.device} (${it.type})`), ` — ${it.state}${it.connection ? ", " + it.connection : ""}`);
  const kv = el("dl");
  kv.className = "kv";
  for (const [k, v] of [["Addresses", (it.addresses || []).join(", ") || "—"], ["Gateway", it.gateway || "—"], ["DNS", (it.dns || []).join(", ") || "—"]])
    kv.append(el("dt", k), el("dd", v));
  div.append(head, kv);
  if (!it.connection) return div;

  const form = document.createElement("form");
  form.innerHTML = `
    <div class="row">
      <select name="method"><option value="auto">DHCP</option><option value="manual">Static</option></select>
      <input name="address" placeholder="192.168.1.50/24">
      <input name="gateway" placeholder="Gateway">
      <input name="dns" placeholder="DNS, comma separated">
      <button class="primary">Apply</button>
    </div>`;
  form.method.value = it.method === "manual" ? "manual" : "auto";
  form.address.value = it.method === "manual" ? (it.addresses || [])[0] || "" : "";
  form.gateway.value = it.method === "manual" ? it.gateway || "" : "";
  form.dns.value = (it.dns || []).join(", ");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const body = {
      connection: it.connection,
      method: form.method.value,
      address: form.address.value.trim(),
      gateway: form.gateway.value.trim(),
      dns: form.dns.value.split(",").map((s) => s.trim()).filter(Boolean),
    };
    if (!confirm(`Apply to ${it.connection}? If the address changes, reconnect at the new address.`)) return;
    await act(() => api("POST", "/api/network/ipv4", body), "Applying network settings");
    setTimeout(loadNetwork, 5000);
  });
  div.append(form);
  return div;
}

$("#wifi-scan").addEventListener("click", async () => {
  const list = $("#wifi-list");
  list.textContent = "Scanning…";
  const nets = await act(() => api("POST", "/api/network/wifi/scan"));
  list.replaceChildren(...(nets || []).map((n) => {
    const d = el("div");
    d.className = "wifi-item";
    d.append(el("span", (n.active ? "● " : "") + n.ssid), el("span", `${n.signal}% ${n.security || "open"}`));
    d.addEventListener("click", () => { $("#wifi-ssid").value = n.ssid; $("#wifi-pass").focus(); });
    return d;
  }));
  if (!nets || !nets.length) list.textContent = "No networks found";
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
    l.className = "inline";
    const c = document.createElement("input");
    c.type = "checkbox";
    c.value = i;
    c.checked = selected.includes(i);
    l.append(c, " " + d);
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
    ["Hostname", i.hostname], ["pikiosk", i.version], ["OS", i.os_version || "—"], ["Hardware", i.model || "—"],
    ["Addresses", (i.addresses || []).join(", ")], ["Uptime", i.uptime], ["Timezone", i.timezone],
  ];
  $("#sys-info").replaceChildren(...rows.flatMap(([k, v]) => [el("dt", k), el("dd", v || "—")]));
  $("#tz").value = i.timezone || "";
  $("#sys-rollback").hidden = !i.rollback;
  $("#api-token").value = "•".repeat(16);
  $("#api-token").dataset.token = s.api_token;
}

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
  await act(() => api("POST", "/api/password", { current: f.current.value, new: f.new.value }), "Password changed");
  f.reset();
});

$("#tz-save").addEventListener("click", () => act(() => api("POST", "/api/system/timezone", { timezone: $("#tz").value.trim() }), "Timezone set"));
$("#token-show").addEventListener("click", () => ($("#api-token").value = $("#api-token").dataset.token || ""));
$("#token-new").addEventListener("click", async () => {
  if (!confirm("Regenerate the API token? Anything using the old one stops working.")) return;
  const r = await act(() => api("POST", "/api/token"), "Token regenerated");
  if (r) { $("#api-token").value = r.api_token; $("#api-token").dataset.token = r.api_token; }
});
$("#sys-reboot").addEventListener("click", () => confirm("Reboot the kiosk?") && act(() => api("POST", "/api/system/reboot"), "Rebooting"));
$("#sys-rollback").addEventListener("click", () =>
  confirm("Switch back to the previous system version?") && act(() => api("POST", "/api/system/rollback"), "Rolling back"));
$("#sys-update").addEventListener("click", async () => {
  if (!confirm("Download and apply the latest system version? The kiosk may restart its display.")) return;
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
    const t = el("time", new Date(e.time).toLocaleTimeString());
    const ty = el("span", e.type);
    ty.className = "t";
    li.append(t, ty, el("span", e.message));
    return li;
  }));
}

// --- rotations ---

$("#rot-start").addEventListener("click", () =>
  act(() => api("POST", "/api/rotation", { name: $("#ctl-rotation").value }), "Rotation started").then(refreshState));
$("#rot-resume").addEventListener("click", () => act(() => api("POST", "/api/rotation/resume"), "Rotation resumed").then(refreshState));
$("#rot-next").addEventListener("click", () => act(() => api("POST", "/api/rotation/next")).then(refreshState));
$("#rot-stop").addEventListener("click", () => act(() => api("POST", "/api/rotation/stop"), "Rotation stopped").then(refreshState));

function fillRotations() {
  $("#rotations").replaceChildren();
  for (const r of config.rotations || []) addRotationCard(r);
  syncRotationSelects();
}

function viewOptions(select, value) {
  select.replaceChildren(...(config.views || []).map((v) => new Option(v.name + (v.disabled ? " (disabled)" : ""), v.name)));
  if (value) select.value = value;
}

function addRotationItem(list, item = { view: "", seconds: 30 }) {
  const li = $("#rotation-item").content.firstElementChild.cloneNode(true);
  viewOptions($("[data-f=view]", li), item.view);
  $("[data-f=seconds]", li).value = item.seconds;
  $("[data-act=del]", li).addEventListener("click", () => li.remove());
  $("[data-act=up]", li).addEventListener("click", () => li.previousElementSibling && list.insertBefore(li, li.previousElementSibling));
  list.append(li);
}

function addRotationCard(r = { name: "", items: [] }) {
  const card = $("#rotation-card").content.firstElementChild.cloneNode(true);
  const list = $(".items", card);
  $("[data-f=name]", card).value = r.name;
  $("[data-f=name]", card).addEventListener("input", syncRotationSelects);
  for (const it of r.items) addRotationItem(list, it);
  $("[data-act=add-item]", card).addEventListener("click", () => addRotationItem(list));
  $("[data-act=del]", card).addEventListener("click", () => { card.remove(); syncRotationSelects(); });
  $("[data-act=start]", card).addEventListener("click", () =>
    act(() => api("POST", "/api/rotation", { name: $("[data-f=name]", card).value }), "Rotation started (save first if changed)"));
  $("#rotations").append(card);
}

function readRotations() {
  return $$("#rotations .rotation").map((card) => ({
    name: $("[data-f=name]", card).value.trim(),
    items: $$(".items li", card).map((li) => ({
      view: $("[data-f=view]", li).value,
      seconds: Number($("[data-f=seconds]", li).value) || 0,
    })),
  }));
}

function syncRotationSelects() {
  const names = readRotations().map((r) => r.name).filter(Boolean);
  const def = $("#rotations-default");
  const cur = def.value || config.default_rotation;
  def.replaceChildren(new Option("(none)", ""), ...names.map((n) => new Option(n, n)));
  def.value = names.includes(cur) ? cur : "";
  const saved = (config.rotations || []).map((r) => r.name);
  fillSelect($("#ctl-rotation"), saved, "(no rotations yet)");
  $("#rot-start").disabled = !saved.length;
}

$("#rotation-add").addEventListener("click", () => {
  if (!(config.views || []).length) return toast("Add some views first", true);
  addRotationCard();
});
$("#rotations-save").addEventListener("click", async () => {
  await act(() => api("PUT", "/api/config/rotations", { rotations: readRotations(), default_rotation: $("#rotations-default").value }), "Rotations saved");
  await loadConfig();
});

// --- sounds ---

async function loadSounds() {
  const list = await act(() => api("GET", "/api/sounds"));
  $("#sounds-body").replaceChildren(...(list || []).map((snd) => {
    const tr = el("tr");
    const name = el("td", snd.name);
    const info = el("td", (snd.builtin ? "built-in · " : "") + Math.max(1, Math.round(snd.size / 1024)) + " KiB");
    info.className = "muted";
    const actions = el("td");
    const play = el("button", "Play");
    play.addEventListener("click", () => act(() => api("POST", "/api/sound/play", { name: snd.name })));
    actions.append(play);
    if (!snd.builtin) {
      const del = el("button", "Delete");
      del.className = "danger";
      del.addEventListener("click", async () => {
        if (!confirm(`Delete ${snd.name}?`)) return;
        await act(() => api("DELETE", "/api/sounds/" + encodeURIComponent(snd.name)), "Deleted");
        loadSounds();
      });
      actions.append(" ", del);
    }
    tr.append(name, info, actions);
    return tr;
  }));
  if (!list || !list.length) $("#sounds-body").replaceChildren(el("tr", "No sounds yet"));
}

$("#sound-upload").addEventListener("submit", async (e) => {
  e.preventDefault();
  const res = await fetch("/api/sounds", { method: "POST", headers: { "X-Pikiosk": "1" }, body: new FormData(e.target) });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) return toast(data.error || res.statusText, true);
  toast("Uploaded " + data.name);
  e.target.reset();
  loadSounds();
});
$("#sound-play").addEventListener("click", () => act(() => api("POST", "/api/sound/play", { path: $("#sound-url").value.trim() })));
$("#sound-stop").addEventListener("click", () => act(() => api("POST", "/api/sound/stop")));
$("#volume").addEventListener("change", async (e) => {
  const v = Number(e.target.value);
  await act(() => api("POST", "/api/volume", { value: v }), `Volume ${v}%`);
  if ($("#volume-save").checked) await act(() => api("PUT", "/api/config/audio", { volume: v }));
});
$("#volume-save").addEventListener("change", (e) =>
  act(() => api("PUT", "/api/config/audio", { volume: e.target.checked ? Number($("#volume").value) : -1 }), "Saved"));

boot().catch((e) => toast(e.message, true));

