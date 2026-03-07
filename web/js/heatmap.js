// --- Map init ---
const map = L.map('map', {
  zoomControl: true,
  attributionControl: false,
}).setView([48.5, 31.2], 6);

const baseLayers = {
  'CARTO Voyager': L.tileLayer('https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png', { maxZoom: 20, subdomains: 'abcd' }),
  'OpenStreetMap': L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19 }),
  'Dark': L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', { maxZoom: 20, subdomains: 'abcd' }),
};
baseLayers['CARTO Voyager'].addTo(map);
L.control.layers(baseLayers, null, { position: 'topright' }).addTo(map);
map.invalidateSize();
setTimeout(() => map.invalidateSize(), 200);

// --- Layers (order matters: choro behind markers) ---
const choroLayer     = L.featureGroup().addTo(map);
const ownMarkerLayer = L.featureGroup().addTo(map);
const sbMarkerLayer  = L.featureGroup().addTo(map);

// --- State ---
let markersVisible = true;
let choroVisible   = true;

let ownPoints       = [];
let sbPoints        = [];
let sbGrid          = {};
let sbActiveMarkers = {};

// Markers appear only when zoomed in to street level.
const MARKER_ZOOM = 12;

// Hex size in the normalized coordinate space (lng * LAT_CORR, lat).
// 0.135 ≈ ~30 km tip-to-tip at Ukraine's latitude.
let HEX_SIZE = 0.135;
const LAT_CORR = Math.cos(48.5 * Math.PI / 180); // ~0.661 for Ukraine (~48°N)

// Minimum monitors per hex to draw it (filters single-point noise).
const MIN_CELL = 1;


// --- Hex grid math (pointy-top axial coordinates) ---
function latlngToAxial(lat, lng) {
  const x = lng * LAT_CORR, y = lat;
  const qf = (Math.sqrt(3) / 3 * x - y / 3) / HEX_SIZE;
  const rf = (2 / 3 * y) / HEX_SIZE;
  // Cube-coordinate rounding
  const sf = -qf - rf;
  let q = Math.round(qf), r = Math.round(rf), s = Math.round(sf);
  const dq = Math.abs(q - qf), dr = Math.abs(r - rf), ds = Math.abs(s - sf);
  if (dq > dr && dq > ds) q = -r - s;
  else if (dr > ds) r = -q - s;
  return { q, r };
}

function axialToLatLng(q, r) {
  return {
    lat: HEX_SIZE * (3 / 2 * r),
    lng: HEX_SIZE * (Math.sqrt(3) * q + Math.sqrt(3) / 2 * r) / LAT_CORR,
  };
}

function hexVertices(lat, lng) {
  const pts = [];
  for (let i = 0; i < 6; i++) {
    const a = Math.PI / 180 * (60 * i + 30); // pointy-top: 30° offset
    pts.push([lat + HEX_SIZE * Math.sin(a), lng + HEX_SIZE * Math.cos(a) / LAT_CORR]);
  }
  return pts;
}

// --- Duration helpers (for markers only) ---
const MAX_HOURS = 12;

function getDurationHours(since) {
  if (!since) return 0;
  return Math.max(0, (Date.now() - new Date(since).getTime()) / 3_600_000);
}

function formatDuration(since) {
  if (!since) return '';
  const diff = Math.floor((Date.now() - new Date(since).getTime()) / 1000);
  if (diff < 0) return '';
  const days  = Math.floor(diff / 86400);
  const hours = Math.floor((diff % 86400) / 3600);
  const mins  = Math.floor((diff % 3600) / 60);
  const parts = [];
  if (days  > 0) parts.push(days  + ' д');
  if (hours > 0) parts.push(hours + ' год');
  parts.push(mins + ' хв');
  return parts.join(' ');
}

// --- Color helpers ---
function lerpColor(a, b, t) {
  return [
    Math.round(a[0] + (b[0] - a[0]) * t),
    Math.round(a[1] + (b[1] - a[1]) * t),
    Math.round(a[2] + (b[2] - a[2]) * t),
  ];
}
function rgbHex([r, g, b]) {
  return '#' + [r, g, b].map(v => v.toString(16).padStart(2, '0')).join('');
}

// Marker color: 4-hour discrete steps (0–4h, 4–8h, 8–12h, 12h+).
const OFFLINE_STEPS = ['#fb923c', '#f97316', '#dc2626', '#7f1d1d']; // orange → deep red
const ONLINE_STEPS  = ['#86efac', '#4ade80', '#16a34a', '#14532d']; // light → deep green

function durationColor(isOnline, hours) {
  const step = Math.min(Math.floor(hours / 4), 3); // 0, 1, 2, 3
  return isOnline ? ONLINE_STEPS[step] : OFFLINE_STEPS[step];
}

// Choropleth color: green (0% offline) → amber (50%) → red (100%).
function choroColor(offlinePct) {
  if (offlinePct <= 0.5) {
    return rgbHex(lerpColor([74, 222, 128], [251, 191, 36], offlinePct * 2)); // green-400 → amber-300
  }
  return rgbHex(lerpColor([251, 191, 36], [220, 38, 38], (offlinePct - 0.5) * 2)); // amber-300 → red-600
}

// --- Shared helpers ---
function escapeHtml(text) {
  const d = document.createElement('div');
  d.textContent = text || '';
  return d.innerHTML;
}

function buildPopupHtml({ isOnline, since, name, address, channel, sourceTag }) {
  const hours  = getDurationHours(since);
  const color  = durationColor(isOnline, hours);
  const status = isOnline ? 'Світло є' : 'Світла немає';
  const dur    = formatDuration(since);
  return `
    <div style="font-family:Inter,system-ui,sans-serif;min-width:170px;line-height:1.5;">
      ${name    ? `<div style="font-weight:600;font-size:0.95em;">${escapeHtml(name)}</div>` : ''}
      ${address ? `<div style="font-size:0.85em;color:#78716c;margin-bottom:6px;">${escapeHtml(address)}</div>` : ''}
      <div style="font-weight:500;color:${color};">${status}</div>
      ${dur     ? `<div style="font-size:0.83em;color:#78716c;">${dur}</div>` : ''}
      ${channel ? `<div style="margin-top:6px;font-size:0.8em;"><a href="https://t.me/${escapeHtml(channel)}" target="_blank" style="color:#0ea5e9;text-decoration:none;">@${escapeHtml(channel)}</a></div>` : ''}
      ${sourceTag ? `<div style="margin-top:4px;font-size:0.7em;color:#a8a29e;">${escapeHtml(sourceTag)}</div>` : ''}
    </div>`;
}

function makeMarker(lat, lng, isOnline, since, popupFn) {
  const color = durationColor(isOnline, getDurationHours(since));
  const size  = 14;
  const icon  = L.divIcon({
    className: '',
    iconSize:   [size, size],
    iconAnchor: [size / 2, size / 2],
    html: `<div style="width:${size}px;height:${size}px;background:${color};border:2px solid rgba(255,255,255,0.85);border-radius:50%;box-shadow:0 1px 4px rgba(0,0,0,0.5);"></div>`,
  });
  const marker = L.marker([lat, lng], { icon });
  marker.on('click', () => {
    if (marker.getPopup()) marker.unbindPopup();
    marker.bindPopup(popupFn()).openPopup();
  });
  return marker;
}

// --- Choropleth ---
function buildChoropleth() {
  choroLayer.clearLayers();
  if (!choroVisible) return;

  // Bin every point into a hex cell.
  const cells = {};
  const all = [
    ...ownPoints.map(m => ({ lat: m.lat, lng: m.lng, is_online: m.is_online })),
    ...sbPoints.map(p  => ({ lat: p.lat, lng: p.lng, is_online: p.is_online })),
  ];

  for (const p of all) {
    const { q, r } = latlngToAxial(p.lat, p.lng);
    const key = q + ':' + r;
    if (!cells[key]) cells[key] = { q, r, total: 0, offline: 0 };
    cells[key].total++;
    if (!p.is_online) cells[key].offline++;
  }

  for (const key in cells) {
    const c = cells[key];
    if (c.total < MIN_CELL) continue;

    const pct     = c.offline / c.total;
    const color   = choroColor(pct);
    // More monitors → more opaque (more trustworthy), capped at 0.75.
    const opacity = 0.25 + Math.min(c.total / 25, 1) * 0.5;

    const center = axialToLatLng(c.q, c.r);
    const verts  = hexVertices(center.lat, center.lng);

    const hex = L.polygon(verts, {
      stroke: false,
      fillColor: color,
      fillOpacity: opacity,
    });

    const offPct = Math.round(pct * 100);
    hex.bindTooltip(
      `<strong>${offPct}%</strong> без світла<br><span style="color:#78716c;font-size:0.85em;">${c.offline} з ${c.total} моніторів</span>`,
      { sticky: true },
    );
    choroLayer.addLayer(hex);
  }
}

// --- Own monitors ---
async function loadMonitors() {
  try {
    const res  = await fetch('/api/monitors');
    const data = await res.json();
    ownPoints  = data;
    rebuildOwnMarkers();
    buildChoropleth();
    updateStats();
    const el = document.getElementById('last-updated');
    if (el) {
      const t = new Date();
      el.textContent = 'Оновлено: ' + t.toLocaleTimeString('uk-UA', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    }
  } catch (e) {
    console.error('Failed to load monitors:', e);
  }
}

function rebuildOwnMarkers() {
  ownMarkerLayer.clearLayers();
  if (!markersVisible || map.getZoom() < MARKER_ZOOM) return;
  for (const m of ownPoints) {
    ownMarkerLayer.addLayer(makeMarker(
      m.lat, m.lng, m.is_online, m.status_since,
      () => buildPopupHtml({ isOnline: m.is_online, since: m.status_since, name: m.name, address: m.address, channel: m.channel_name }),
    ));
  }
}

// --- Svitlobot ---
const SVITLOBOT_API = 'https://api.svitlobot.in.ua/website/getChannelsForMap';
const GRID_CELL     = 0.5;

function gridKey(lat, lng) {
  return Math.floor(lat / GRID_CELL) + ':' + Math.floor(lng / GRID_CELL);
}

function buildGrid(points) {
  sbGrid = {};
  for (const p of points) {
    const key = gridKey(p.lat, p.lng);
    if (!sbGrid[key]) sbGrid[key] = [];
    sbGrid[key].push(p);
  }
}

function getInBounds(bounds) {
  const s = bounds.getSouth(), n = bounds.getNorth();
  const w = bounds.getWest(),  e = bounds.getEast();
  const minR = Math.floor(s / GRID_CELL), maxR = Math.floor(n / GRID_CELL);
  const minC = Math.floor(w / GRID_CELL), maxC = Math.floor(e / GRID_CELL);
  const result = [];
  for (let r = minR; r <= maxR; r++) {
    for (let c = minC; c <= maxC; c++) {
      const cell = sbGrid[r + ':' + c];
      if (cell) for (const p of cell) if (bounds.contains([p.lat, p.lng])) result.push(p);
    }
  }
  return result;
}

function parseSvitlobot(raw) {
  const points = [];
  for (const line of raw.trim().split('\n')) {
    if (!line.trim()) continue;
    const f = line.split(';&&&;');
    if (f.length < 10) continue;
    const lat = parseFloat(f[6]), lng = parseFloat(f[7]);
    if (isNaN(lat) || isNaN(lng)) continue;
    const status = parseInt(f[1], 10);
    if (status === 3) continue;
    points.push({ id: 'sb_' + f[4], lat, lng, is_online: status === 1, since: f[2] || null, name: f[3] || '', channel: f[4] || '' });
  }
  return points;
}

async function loadSvitlobot() {
  try {
    const res = await fetch(SVITLOBOT_API);
    const raw = await res.text();
    sbPoints  = parseSvitlobot(raw);
    buildGrid(sbPoints);
    clearSbMarkers();
    renderSbViewport();
    buildChoropleth();
    updateStats();
  } catch (e) {
    console.error('Failed to load Svitlobot:', e);
  }
}

function clearSbMarkers() {
  sbMarkerLayer.clearLayers();
  sbActiveMarkers = {};
}

function renderSbViewport() {
  if (!markersVisible || map.getZoom() < MARKER_ZOOM || Object.keys(sbGrid).length === 0) {
    clearSbMarkers();
    return;
  }

  const bounds  = map.getBounds().pad(0.2);
  const visible = getInBounds(bounds);
  const newActive  = {};
  const toAdd      = [];
  const toRemoveIds = new Set(Object.keys(sbActiveMarkers));

  for (const p of visible) {
    toRemoveIds.delete(p.id);
    if (sbActiveMarkers[p.id]) {
      newActive[p.id] = sbActiveMarkers[p.id];
    } else {
      const marker = makeMarker(
        p.lat, p.lng, p.is_online, p.since,
        () => buildPopupHtml({ isOnline: p.is_online, since: p.since, name: p.name, channel: p.channel, sourceTag: 'svitlobot' }),
      );
      newActive[p.id] = marker;
      toAdd.push(marker);
    }
  }

  for (const id of toRemoveIds) sbMarkerLayer.removeLayer(sbActiveMarkers[id]);
  for (const m of toAdd) sbMarkerLayer.addLayer(m);
  sbActiveMarkers = newActive;
}

// --- Stats ---
function updateStats() {
  const badge = document.getElementById('stats-badge');
  if (!badge) return;
  const all    = [...ownPoints, ...sbPoints];
  const total  = all.length;
  const online = all.filter(p => p.is_online).length;
  badge.innerHTML = `<span>${total}</span> локацій &middot; <span style="color:#16a34a">${online}</span> зі світлом &middot; <span style="color:#dc2626">${total - online}</span> без світла`;
  badge.style.display = '';
}

// --- Controls ---
document.getElementById('toggle-markers').addEventListener('change', function () {
  markersVisible = this.checked;
  localStorage.setItem('hm_markers', markersVisible);
  rebuildOwnMarkers();
  if (!markersVisible) clearSbMarkers();
  else renderSbViewport();
});

document.getElementById('toggle-choro').addEventListener('change', function () {
  choroVisible = this.checked;
  localStorage.setItem('hm_choro', choroVisible);
  buildChoropleth();
});

// Hex size — logarithmic slider from 0.3 km to 100 km.
const HEX_LOG_MIN = Math.log(0.3);
const HEX_LOG_MAX = Math.log(100);

function sliderToKm(val) {
  return Math.exp(HEX_LOG_MIN + (HEX_LOG_MAX - HEX_LOG_MIN) * val / 100);
}
function formatHexSize(km) {
  if (km < 1)  return `${Math.round(km * 1000)} м`;
  if (km < 10) return `${km.toFixed(1)} км`;
  return `${Math.round(km)} км`;
}

document.getElementById('hex-size').addEventListener('input', function () {
  const km = sliderToKm(parseInt(this.value, 10));
  HEX_SIZE = km / (2 * 111);
  localStorage.setItem('hm_hex_size', this.value);
  document.getElementById('hex-size-label').textContent = formatHexSize(km);
  buildChoropleth();
});

// --- Restore saved control state ---
(function restoreControls() {
  const savedChoro   = localStorage.getItem('hm_choro');
  const savedMarkers = localStorage.getItem('hm_markers');
  const savedHexSize = localStorage.getItem('hm_hex_size');

  if (savedChoro !== null) {
    choroVisible = savedChoro !== 'false';
    document.getElementById('toggle-choro').checked = choroVisible;
  }
  if (savedMarkers !== null) {
    markersVisible = savedMarkers !== 'false';
    document.getElementById('toggle-markers').checked = markersVisible;
  }
  if (savedHexSize !== null) {
    const sliderEl = document.getElementById('hex-size');
    sliderEl.value = savedHexSize;
    const km = sliderToKm(parseInt(savedHexSize, 10));
    HEX_SIZE = km / (2 * 111);
    document.getElementById('hex-size-label').textContent = formatHexSize(km);
  }
})();


// Debounced pan/zoom — re-renders svitlobot markers and own markers.
let renderTimeout = null;
function debouncedRender() {
  if (renderTimeout) clearTimeout(renderTimeout);
  renderTimeout = setTimeout(() => {
    rebuildOwnMarkers();
    renderSbViewport();
  }, 150);
}
map.on('moveend', debouncedRender);
map.on('zoomend', debouncedRender);

// --- Init ---
loadMonitors();
loadSvitlobot();
setInterval(loadMonitors,  5 * 60 * 1000);
setInterval(loadSvitlobot, 5 * 60 * 1000);
