// --- Map initialization ---
const map = L.map('map', {
  zoomControl: true,
  attributionControl: false,
}).setView([48.5, 31.2], 6);

const baseLayers = {
  'Google': L.tileLayer('https://mt{s}.google.com/vt/lyrs=m&x={x}&y={y}&z={z}', {
    maxZoom: 20,
    subdomains: '0123',
  }),
  'OpenStreetMap': L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
  }),
  'CARTO Voyager': L.tileLayer('https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png', {
    maxZoom: 20,
    subdomains: 'abcd',
  }),
  'Dark': L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
    maxZoom: 20,
    subdomains: 'abcd',
  }),
};
baseLayers['OpenStreetMap'].addTo(map);
L.control.layers(baseLayers, null, { position: 'topright' }).addTo(map);

map.invalidateSize();
setTimeout(() => map.invalidateSize(), 200);

// --- Marker management ---
const markers = {};
const clusterGroup = L.markerClusterGroup({
  maxClusterRadius: 50,
  spiderfyOnMaxZoom: true,
  showCoverageOnHover: false,
  disableClusteringAtZoom: 14,
  iconCreateFunction: createClusterIcon,
});
map.addLayer(clusterGroup);

const COLORS = {
  online:      '#00ff66',
  onlineText:  '#15803d',
  offline:     '#dc2626',
};

function createClusterIcon(cluster) {
  const children = cluster.getAllChildMarkers();
  const total = children.length;
  let online = 0;
  for (const m of children) {
    if (m.options.isOnline) online++;
  }
  const pct = Math.round((online / total) * 100);
  const size = total < 10 ? 36 : total < 100 ? 42 : 50;
  const ring = Math.round(size * 0.1);
  const inner = size - ring * 2;

  return L.divIcon({
    className: '',
    iconSize: [size, size],
    html: `<div style="
      width:${size}px;height:${size}px;
      border-radius:50%;
      background:conic-gradient(${COLORS.online} 0% ${pct}%, ${COLORS.offline} ${pct}% 100%);
      box-shadow:0 2px 8px rgba(0,0,0,0.4);
      display:flex;align-items:center;justify-content:center;
    "><div style="
      width:${inner}px;height:${inner}px;
      border-radius:50%;
      background:white;
      display:flex;align-items:center;justify-content:center;
      font-family:Inter,system-ui,sans-serif;
      font-size:${size < 42 ? 11 : 13}px;font-weight:700;color:#333;
    ">${total}</div></div>`,
  });
}

function createMarker(monitor) {
  const color = monitor.is_online ? COLORS.online : COLORS.offline;

  const size = 18;
  const icon = L.divIcon({
    className: '',
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
    html: `<div style="
      width:${size}px;height:${size}px;
      background:${color};
      border:3px solid white;
      border-radius:50%;
      box-shadow:0 4px 12px rgba(0,0,0,0.8);
    "></div>`,
  });
  const marker = L.marker([monitor.lat, monitor.lng], { icon, isOnline: monitor.is_online });

  const statusText = monitor.is_online ? 'Світло є' : 'Світла немає';
  const statusColor = monitor.is_online ? COLORS.onlineText : COLORS.offline;
  const channel = monitor.channel_name
    ? `<div style="margin-top:6px;font-size:0.8em;"><a href="https://t.me/${escapeHtml(monitor.channel_name)}" target="_blank" style="color:#0ea5e9;text-decoration:none;">@${escapeHtml(monitor.channel_name)}</a></div>`
    : '';

  marker.bindPopup(`
    <div style="font-family:Inter,system-ui,sans-serif;min-width:170px;line-height:1.5;">
      <div style="font-weight:600;font-size:0.95em;">${escapeHtml(monitor.name)}</div>
      <div style="font-size:0.85em;color:#78716c;margin-bottom:8px;">${escapeHtml(monitor.address)}</div>
      <div style="font-weight:500;color:${statusColor};">${statusText}</div>
      <div style="font-size:0.83em;color:#78716c;">${monitor.status_duration}</div>
      ${channel}
    </div>
  `);

  return marker;
}

function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

function formatDuration(since) {
  const diff = Math.floor((Date.now() - since.getTime()) / 1000);
  if (diff < 0) return '';
  const days = Math.floor(diff / 86400);
  const hours = Math.floor((diff % 86400) / 3600);
  const mins = Math.floor((diff % 3600) / 60);
  const parts = [];
  if (days > 0) parts.push(days + ' д');
  if (hours > 0) parts.push(hours + ' год');
  parts.push(mins + ' хв');
  return parts.join(' ');
}

function updateMarker(monitor) {
  const existing = markers[monitor.id];
  if (existing) {
    clusterGroup.removeLayer(existing);
  }
  const marker = createMarker(monitor);
  markers[monitor.id] = marker;
  clusterGroup.addLayer(marker);
}

// --- Load monitors from API ---
async function loadMonitors() {
  try {
    const res = await fetch('/api/monitors');
    const data = await res.json();

    let online = 0;
    let offline = 0;

    data.forEach(monitor => {
      updateMarker(monitor);
      if (monitor.is_online) online++;
      else offline++;
    });

    updateStats(data.length, online, offline);
  } catch (e) {
    console.error('Failed to load monitors:', e);
  }
}

function updateStats(total, online, offline) {
  const badge = document.getElementById('stats-badge');
  badge.innerHTML = `
    <span>${total}</span> локацій &middot;
    <span style="color:#16a34a">${online}</span> зі світлом &middot;
    <span style="color:#dc2626">${offline}</span> без світла
  `;
}

// --- Svitlobot integration ---
const SVITLOBOT_API = 'https://api.svitlobot.in.ua/website/getChannelsForMap';
const svitlobotClusterGroup = L.markerClusterGroup({
  maxClusterRadius: 50,
  spiderfyOnMaxZoom: true,
  showCoverageOnHover: false,
  disableClusteringAtZoom: 14,
  animate: true,
  chunkedLoading: true,
  iconCreateFunction: createClusterIcon,
});
map.addLayer(svitlobotClusterGroup);
let svitlobotVisible = true;
let svitlobotActiveMarkers = {}; // currently visible markers by id

// --- Spatial grid index ---
const GRID_CELL_SIZE = 0.5; // degrees
let spatialGrid = {};

function gridKey(lat, lng) {
  return Math.floor(lat / GRID_CELL_SIZE) + ':' + Math.floor(lng / GRID_CELL_SIZE);
}

function buildSpatialGrid(points) {
  spatialGrid = {};
  for (const point of points) {
    const key = gridKey(point.lat, point.lng);
    if (!spatialGrid[key]) spatialGrid[key] = [];
    spatialGrid[key].push(point);
  }
}

function getPointsInBounds(bounds) {
  const south = bounds.getSouth();
  const north = bounds.getNorth();
  const west = bounds.getWest();
  const east = bounds.getEast();

  const minRow = Math.floor(south / GRID_CELL_SIZE);
  const maxRow = Math.floor(north / GRID_CELL_SIZE);
  const minCol = Math.floor(west / GRID_CELL_SIZE);
  const maxCol = Math.floor(east / GRID_CELL_SIZE);

  const result = [];
  for (let row = minRow; row <= maxRow; row++) {
    for (let col = minCol; col <= maxCol; col++) {
      const cell = spatialGrid[row + ':' + col];
      if (cell) {
        for (const point of cell) {
          if (bounds.contains([point.lat, point.lng])) {
            result.push(point);
          }
        }
      }
    }
  }
  return result;
}

function parseSvitlobotData(raw) {
  const points = [];
  const lines = raw.trim().split('\n');
  for (const line of lines) {
    if (!line.trim()) continue;
    const fields = line.split(';&&&;');
    if (fields.length < 9) continue;

    const lat = parseFloat(fields[6]);
    const lng = parseFloat(fields[7]);
    if (isNaN(lat) || isNaN(lng)) continue;

    const lightStatus = parseInt(fields[1], 10);
    // 1 = light on, 2 = light off, 3 = tech break
    if (lightStatus === 3) continue;
    const isOnline = lightStatus === 1;

    const name = fields[3] || '';
    const channel = fields[4] || '';
    const subscribers = fields[5] || '';
    const timestamp = fields[2] || '';
    const group = fields[9] && fields[9] !== '-' ? fields[9] : '';

    points.push({
      id: 'sb_' + channel,
      name,
      channel,
      subscribers,
      lat,
      lng,
      is_online: isOnline,
      light_status: lightStatus,
      timestamp,
      group,
    });
  }
  return points;
}

// Cached icons — only 2 variants needed.
const svitlobotIcons = {
  1: L.divIcon({
    className: '',
    iconSize: [18, 18],
    iconAnchor: [9, 9],
    html: `<div style="width:18px;height:18px;background:${COLORS.online};border:3px solid white;transform:rotate(45deg);box-shadow:0 4px 12px rgba(0,0,0,0.8);"></div>`,
  }),
  2: L.divIcon({
    className: '',
    iconSize: [18, 18],
    iconAnchor: [9, 9],
    html: `<div style="width:18px;height:18px;background:${COLORS.offline};border:3px solid white;transform:rotate(45deg);box-shadow:0 4px 12px rgba(0,0,0,0.8);"></div>`,
  }),
};

function createSvitlobotMarker(point) {
  const icon = svitlobotIcons[point.light_status] || svitlobotIcons[2];
  const marker = L.marker([point.lat, point.lng], { icon, isOnline: point.is_online });

  // Lazy popup — only build HTML on first click.
  marker.on('click', function () {
    if (marker.getPopup()) return;

    const color = point.light_status === 1 ? COLORS.onlineText : COLORS.offline;
    const statusText = point.light_status === 1 ? 'Світло є' : 'Світла немає';
    const duration = point.timestamp ? formatDuration(new Date(point.timestamp)) : '';
    const durationText = duration
      ? `<div style="font-size:0.83em;color:#78716c;">${duration}</div>`
      : '';
    const channelLink = point.channel
      ? `<div style="margin-top:6px;font-size:0.8em;"><a href="https://t.me/${escapeHtml(point.channel)}" target="_blank" style="color:#0ea5e9;text-decoration:none;">@${escapeHtml(point.channel)}</a> (${escapeHtml(point.subscribers)})</div>`
      : '';
    const groupInfo = point.group
      ? `<div style="font-size:0.8em;color:#78716c;">${escapeHtml(point.group)}</div>`
      : '';

    marker.bindPopup(`
      <div style="font-family:Inter,system-ui,sans-serif;min-width:170px;line-height:1.5;">
        <div style="font-weight:600;font-size:0.95em;">${escapeHtml(point.name)}</div>
        <div style="font-weight:500;color:${color};">${statusText}</div>
        ${durationText}
        ${groupInfo}
        ${channelLink}
        <div style="margin-top:4px;font-size:0.7em;color:#a8a29e;">svitlobot</div>
      </div>
    `).openPopup();
  });

  return marker;
}

function renderSvitlobotViewport() {
  if (!svitlobotVisible || Object.keys(spatialGrid).length === 0) return;

  const bounds = map.getBounds().pad(0.3);
  const visiblePoints = getPointsInBounds(bounds);

  const toAdd = [];
  const newActive = {};
  const visibleIds = new Set();

  for (const point of visiblePoints) {
    visibleIds.add(point.id);
    const existing = svitlobotActiveMarkers[point.id];
    if (existing) {
      newActive[point.id] = existing;
    } else {
      const marker = createSvitlobotMarker(point);
      newActive[point.id] = marker;
      toAdd.push(marker);
    }
  }

  // Remove markers no longer in view.
  const toRemove = [];
  for (const id in svitlobotActiveMarkers) {
    if (!visibleIds.has(id)) {
      toRemove.push(svitlobotActiveMarkers[id]);
    }
  }

  if (toRemove.length) svitlobotClusterGroup.removeLayers(toRemove);
  if (toAdd.length) svitlobotClusterGroup.addLayers(toAdd);
  svitlobotActiveMarkers = newActive;
}

// Debounce to avoid rapid re-renders during panning.
let renderTimeout = null;
function debouncedRender() {
  if (renderTimeout) clearTimeout(renderTimeout);
  renderTimeout = setTimeout(renderSvitlobotViewport, 150);
}

async function loadSvitlobot() {
  try {
    const res = await fetch(SVITLOBOT_API);
    const raw = await res.text();
    const allPoints = parseSvitlobotData(raw);
    buildSpatialGrid(allPoints);

    let sbOnline = 0;
    let sbOffline = 0;
    for (const p of allPoints) {
      if (p.is_online) sbOnline++;
      else sbOffline++;
    }
    updateSvitlobotStats(allPoints.length, sbOnline, sbOffline);

    // Clear old markers and render for current viewport.
    svitlobotClusterGroup.clearLayers();
    svitlobotActiveMarkers = {};
    renderSvitlobotViewport();
  } catch (e) {
    console.error('Failed to load Svitlobot data:', e);
  }
}

// Re-render on pan/zoom (debounced).
map.on('moveend', debouncedRender);

function updateSvitlobotStats(total, online, offline) {
  let badge = document.getElementById('svitlobot-stats');
  if (!badge) {
    badge = document.createElement('div');
    badge.id = 'svitlobot-stats';
    const statsBadge = document.getElementById('stats-badge');
    if (statsBadge) {
      badge.className = statsBadge.className;
      statsBadge.parentNode.insertBefore(badge, statsBadge.nextSibling);
    }
  }
  badge.innerHTML = `
    Svitlobot: <span>${total}</span> локацій &middot;
    <span style="color:#16a34a">${online}</span> зі світлом &middot;
    <span style="color:#dc2626">${offline}</span> без світла
  `;
}

// --- Svitlobot toggle ---
document.getElementById('toggle-svitlobot').addEventListener('change', function () {
  svitlobotVisible = this.checked;
  if (svitlobotVisible) {
    map.addLayer(svitlobotClusterGroup);
    renderSvitlobotViewport();
    const badge = document.getElementById('svitlobot-stats');
    if (badge) badge.style.display = '';
  } else {
    map.removeLayer(svitlobotClusterGroup);
    const badge = document.getElementById('svitlobot-stats');
    if (badge) badge.style.display = 'none';
  }
});

// --- Initialize ---
loadMonitors();
loadSvitlobot();

// Poll every 5mins seconds for own monitors.
setInterval(loadMonitors, 60000 * 5);

// Poll Svitlobot every 5 minutes.
setInterval(loadSvitlobot, 5 * 60 * 1000);
