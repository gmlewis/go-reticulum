// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the captive portal's single page: the touch-optimized
// survival dashboard the traveler's phone opens by itself.
//
// The page is deliberately one string with no external reference of any kind —
// no stylesheet, no font, no image, no script from a network — because the
// network it is served over is the device's own Wi-Fi and has no route out.
// The @@TOKENS@@ are substituted by renderDashboard before the page is served,
// which is what makes the panel readable before any JavaScript runs.

package main

// portalDashboardHTML is the dashboard template. The @@TOKENS@@ are replaced
// per request with the live position; nothing else in the page is dynamic.
const portalDashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>GRL Survival Portal</title>
<style>
:root {
  color-scheme: dark;
  --bg: #0b0f14;
  --card: #141b24;
  --ink: #e8eef6;
  --muted: #93a4b8;
  --accent: #4fc3f7;
  --danger: #ff3b30;
  --rule: #223040;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  padding: 0 0 env(safe-area-inset-bottom);
  background: var(--bg);
  color: var(--ink);
  font: 16px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
}
header { padding: 18px 16px 8px; }
h1 { margin: 0; font-size: 1.25rem; letter-spacing: .02em; }
h2 { margin: 0 0 10px; font-size: .8rem; text-transform: uppercase; letter-spacing: .12em; color: var(--muted); }
header p { margin: 6px 0 0; color: var(--muted); font-size: .85rem; }
main { padding: 8px 12px 32px; display: grid; gap: 12px; }
.card { background: var(--card); border: 1px solid var(--rule); border-radius: 14px; padding: 14px; }
.plus {
  font-size: 2rem; font-weight: 700; letter-spacing: .04em;
  color: var(--accent); word-break: break-all; margin-bottom: 10px;
}
dl { margin: 0; display: grid; gap: 8px; }
dl div { display: flex; justify-content: space-between; gap: 12px; border-top: 1px solid var(--rule); padding-top: 8px; }
dt { color: var(--muted); font-size: .82rem; }
dd { margin: 0; text-align: right; font-variant-numeric: tabular-nums; }
button {
  width: 100%; min-height: 48px; border-radius: 12px; border: 1px solid var(--rule);
  background: #1d2735; color: var(--ink); font-size: 1rem; font-weight: 600;
}
button.primary { background: var(--accent); color: #04222f; border-color: var(--accent); }
button.sos {
  background: var(--danger); border-color: var(--danger); color: #fff;
  font-size: 1.15rem; letter-spacing: .06em; min-height: 64px;
}
form { display: flex; gap: 8px; }
input[type=text] {
  flex: 1 1 auto; min-height: 48px; border-radius: 12px; padding: 0 12px;
  background: #0e141c; color: var(--ink); border: 1px solid var(--rule); font-size: 1rem;
}
#replies { margin-top: 12px; display: grid; gap: 8px; }
.reply { border-left: 3px solid var(--accent); padding: 6px 10px; background: #0e141c; border-radius: 0 8px 8px 0; }
.reply .who { color: var(--muted); font-size: .78rem; display: block; margin-bottom: 4px; }
.reply pre { margin: 0; white-space: pre-wrap; word-break: break-word; font: inherit; }
.hint { color: var(--muted); font-size: .8rem; margin: 8px 0 0; }
.compass-wrap { display: flex; gap: 14px; align-items: center; flex-wrap: wrap; }
.compass { flex: 0 0 auto; width: 44vw; max-width: 220px; min-width: 150px; height: auto; }
.compass .dial { fill: none; stroke: var(--rule); stroke-width: 2; }
.compass .dial.inner { stroke-dasharray: 2 5; }
.compass text { fill: var(--muted); font-size: 15px; font-weight: 700; text-anchor: middle; }
.compass .target-needle path { fill: var(--danger); }
.compass .heading-needle path { fill: var(--accent); }
.compass .hub { fill: var(--ink); }
.compass-readout { flex: 1 1 140px; display: grid; gap: 6px; }
.compass-readout .deg { font-size: 1.7rem; font-weight: 700; color: var(--accent); font-variant-numeric: tabular-nums; }
.compass-readout .frame { color: var(--muted); font-size: .85rem; font-variant-numeric: tabular-nums; }
.compass-readout .vector { font-size: .9rem; }
.compass-readout .vector strong { color: var(--danger); }
.legend { display: flex; gap: 12px; color: var(--muted); font-size: .75rem; margin-top: 10px; }
.legend span::before { content: "■ "; }
.legend .key-heading::before { color: var(--accent); }
.legend .key-target::before { color: var(--danger); }
</style>
</head>
<body>
<header>
  <h1>GRL Survival Portal</h1>
  <p id="link-state">Offline field assistant — no internet, no app, no account required.</p>
</header>
<main>
  <section class="card" id="whereami">
    <h2>Where Am I</h2>
    <div class="plus" id="plus-code">@@PLUS_CODE@@</div>
    <button class="primary" id="copy-plus" type="button">Copy Plus Code</button>
    <dl>
      <div><dt>Coordinates</dt><dd id="coords">@@COORDINATES@@</dd></div>
      <div><dt>Maidenhead grid</dt><dd id="grid">@@MAIDENHEAD@@</dd></div>
      <div><dt>Elevation</dt><dd id="elevation">@@ELEVATION@@</dd></div>
      <div><dt>GNSS fix</dt><dd id="fix-status">@@FIX_STATUS@@</dd></div>
      <div><dt>Local solar time</dt><dd id="solar-clock">@@SOLAR_CLOCK@@</dd></div>
      <div><dt>Sunset</dt><dd id="sunset">@@SUNSET@@</dd></div>
    </dl>
  </section>

  <section class="card" id="compass">
    <h2>Compass &amp; Direction Finding</h2>
    <div class="compass-wrap">
      <svg class="compass" id="compass-rose" viewBox="0 0 200 200" role="img"
           aria-label="Compass rose showing the device heading and the nearest beacon or site">
        <circle class="dial" cx="100" cy="100" r="94"></circle>
        <circle class="dial inner" cx="100" cy="100" r="72"></circle>
        <text x="100" y="30">N</text>
        <text x="172" y="106">E</text>
        <text x="100" y="182">S</text>
        <text x="28" y="106">W</text>
        <g class="target-needle" id="target-needle" transform="rotate(@@TARGET_ROTATION@@ 100 100)">
          <path d="M100 18 L108 100 L100 116 L92 100 Z"></path>
        </g>
        <g class="heading-needle" id="heading-needle" transform="rotate(@@HEADING_ROTATION@@ 100 100)">
          <path d="M100 40 L107 104 L100 94 L93 104 Z"></path>
        </g>
        <circle class="hub" cx="100" cy="100" r="4"></circle>
      </svg>
      <div class="compass-readout">
        <div class="deg" id="compass-heading">@@COMPASS_HEADING@@</div>
        <div class="frame" id="compass-true">@@COMPASS_TRUE@@</div>
        <div class="frame" id="compass-mag">@@COMPASS_MAG@@</div>
        <div class="vector" id="compass-target">@@COMPASS_TARGET@@</div>
      </div>
    </div>
    <div class="legend">
      <span class="key-heading">Your heading</span>
      <span class="key-target">Beacon or nearest site</span>
    </div>
    <p class="hint">A compass knows which way the device points while you stand still, so you can aim a directional antenna without walking a single step.</p>
  </section>

  <section class="card" id="emergency">
    <h2>Emergency</h2>
    <button class="sos" id="sos" type="button">SOS — SEND DISTRESS</button>
    <p class="hint">Raises a RED distress beacon at the current position and alerts every room the radio is joined to.</p>
  </section>

  <section class="card" id="assistant">
    <h2>Field Assistant</h2>
    <form id="query-form" autocomplete="off">
      <input type="text" id="query" name="command" placeholder="med hypothermia" aria-label="Field assistant command">
      <button class="primary" type="submit">Ask</button>
    </form>
    <p class="hint">Offline commands only, answered in microseconds: med, tower near, tide near, sun, whereami, morse, conv.</p>
    <div id="replies" aria-live="polite"></div>
  </section>
</main>
<script id="portal-data" type="application/json">@@INITIAL_JSON@@</script>
<script>
(function () {
  var data = {};
  try { data = JSON.parse(document.getElementById('portal-data').textContent) || {}; } catch (e) { data = {}; }
  var replies = document.getElementById('replies');

  function setText(id, value) {
    var el = document.getElementById(id);
    if (el) { el.textContent = value && value.length ? value : '—'; }
  }

  function render(view) {
    data = view || {};
    setText('plus-code', view.plus_code);
    setText('coords', view.coordinates);
    setText('grid', view.maidenhead);
    setText('elevation', view.valid ? (view.altitude_m + ' m (' + view.altitude_ft + ' ft) MSL') : '');
    setText('fix-status', view.valid ? view.fix_status : (view.message || 'acquiring a GNSS fix'));
    setText('solar-clock', view.local_solar_time);
    setText('sunset', view.sunset);
    renderCompass(view.heading);
  }

  function bearing(compass) {
    if (compass.has_true) { return compass.true_deg; }
    return compass.mag_deg;
  }

  function frameText(compass, wantTrue) {
    if (wantTrue) {
      if (!compass.has_true) { return ''; }
      if (compass.has_declination) {
        return 'True ' + pad(compass.true_deg) + '° (var ' + signed(compass.declination_deg) + '° ' +
          (compass.declination_deg < 0 ? 'W' : 'E') + ')';
      }
      return 'True ' + pad(compass.true_deg) + '°';
    }
    if (!compass.has_magnetic) { return ''; }
    return 'Mag ' + pad(compass.mag_deg) + '°';
  }

  function pad(value) {
    var text = String(Math.round(value));
    while (text.length < 3) { text = '0' + text; }
    return text;
  }

  function signed(value) {
    return (value >= 0 ? '+' : '-') + Math.abs(value).toFixed(1);
  }

  function targetText(target) {
    if (!target) { return 'no beacon or site within range of the catalog'; }
    var text = target.name + ' · ' + target.distance_km.toFixed(1) + ' km at ' + pad(target.bearing_deg) + '°';
    if (target.frequency) { text += ' · ' + target.frequency; }
    if (target.steering) { text += ' ' + target.steering; }
    return text;
  }

  function rotateNeedle(id, degrees) {
    var needle = document.getElementById(id);
    if (needle) { needle.setAttribute('transform', 'rotate(' + degrees + ' 100 100)'); }
  }

  function renderCompass(compass) {
    if (!compass || !compass.valid) {
      setText('compass-heading', compass && compass.message ? compass.message : 'no compass');
      setText('compass-true', '');
      setText('compass-mag', '');
      setText('compass-target', '');
      rotateNeedle('heading-needle', 0);
      rotateNeedle('target-needle', 0);
      return;
    }
    setText('compass-heading', pad(bearing(compass)) + '° ' + compass.cardinal);
    setText('compass-true', frameText(compass, true));
    setText('compass-mag', frameText(compass, false));
    setText('compass-target', targetText(compass.target));
    rotateNeedle('heading-needle', bearing(compass));
    rotateNeedle('target-needle', compass.target ? compass.target.bearing_deg : 0);
  }

  function refresh() {
    fetch('/api/whereami', { cache: 'no-store' })
      .then(function (res) { return res.json(); })
      .then(render)
      .catch(function () { setText('link-state', 'Portal unreachable — retrying.'); });
  }

  function addReply(command, lines) {
    var box = document.createElement('div');
    box.className = 'reply';
    var who = document.createElement('span');
    who.className = 'who';
    who.textContent = command;
    var pre = document.createElement('pre');
    pre.textContent = (lines && lines.length) ? lines.join('\n') : '(no answer)';
    box.appendChild(who);
    box.appendChild(pre);
    replies.insertBefore(box, replies.firstChild);
  }

  function ask(command) {
    if (!command) { return; }
    return fetch('/api/query', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ command: command })
    }).then(function (res) { return res.json(); })
      .then(function (answer) { addReply(answer.command || command, answer.lines); })
      .catch(function () { addReply(command, ['The field assistant did not answer.']); });
  }

  document.getElementById('query-form').addEventListener('submit', function (event) {
    event.preventDefault();
    var input = document.getElementById('query');
    var value = input.value.trim();
    input.value = '';
    ask(value);
  });

  document.getElementById('copy-plus').addEventListener('click', function () {
    var text = data.plus_code || '';
    if (!text) { return; }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text);
    }
  });

  document.getElementById('sos').addEventListener('click', function () {
    if (!window.confirm('Send a RED distress beacon at the current position?')) { return; }
    var details = window.prompt('Details for the rescue party (optional):', '') || '';
    ask(details.trim() ? 'sos RED ' + details.trim() : 'sos');
    refresh();
  });

  refresh();
  setInterval(refresh, 10000);
})();
</script>
</body>
</html>
`
