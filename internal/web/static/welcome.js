"use strict";

async function refresh() {
  let w;
  try {
    w = await (await fetch("/api/welcome", { cache: "no-store" })).json();
  } catch {
    return;
  }
  const port = w.port && w.port !== "80" ? ":" + w.port : "";
  const urls = [];
  if (w.hostname) urls.push(`http://${w.hostname}.local${port}`);
  for (const a of w.addresses || []) urls.push(`http://${a}${port}`);

  document.getElementById("name").textContent = w.device_name;
  document.getElementById("lead").textContent = urls.length
    ? (w.setup_required ? "To set up this kiosk, open one of these on a phone or computer:" : "Manage this kiosk at:")
    : "Waiting for a network connection…";
  document.getElementById("urls").replaceChildren(...urls.map((u) => Object.assign(document.createElement("li"), { textContent: u })));
  document.getElementById("hint").textContent = urls.length
    ? (w.setup_required ? "You'll choose an admin password, then add the pages or camera streams to show." : "No default view is set. Add one under Views.")
    : "Plug in Ethernet, or put a wifi.txt file on the SD card's boot partition (see the README).";
  document.getElementById("version").textContent = "pikiosk " + w.version;
}

refresh();
setInterval(refresh, 5000);
