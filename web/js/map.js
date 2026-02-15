// --- Map initialization ---
const map = L.map('map', {
  zoomControl: true,
  attributionControl: false,
}).setView([50.45, 30.52], 6);

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
});
map.addLayer(clusterGroup);

const COLORS = {
  online:      '#00ff66',
  onlineRing:  'rgba(22, 163, 74, 0.25)',
  offline:     '#dc2626',
  offlineRing: 'rgba(220, 38, 38, 0.25)',
};

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
  const marker = L.marker([monitor.lat, monitor.lng], { icon });

  const statusText = monitor.is_online ? 'Світло є' : 'Світла немає';
  const statusColor = monitor.is_online ? COLORS.online : COLORS.offline;
  const channel = monitor.channel_name
    ? `<div style="margin-top:6px;font-size:0.8em;color:#78716c;">@${escapeHtml(monitor.channel_name)}</div>`
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
let initialLoad = true;

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

    // Fit bounds only on first load.
    if (initialLoad && data.length > 0) {
      const bounds = L.latLngBounds(data.map(m => [m.lat, m.lng]));
      map.fitBounds(bounds, { padding: [60, 60], maxZoom: 12 });
      initialLoad = false;
    }
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
const svitlobotMarkers = {};
const svitlobotClusterGroup = L.markerClusterGroup({
  maxClusterRadius: 50,
  spiderfyOnMaxZoom: true,
  showCoverageOnHover: false,
  disableClusteringAtZoom: 14,
});
map.addLayer(svitlobotClusterGroup);
let svitlobotVisible = true;

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

function createSvitlobotMarker(point) {
  let color, statusText;
  if (point.light_status === 1) {
    color = COLORS.online;
    statusText = 'Світло є';
  } else if (point.light_status === 2) {
    color = COLORS.offline;
    statusText = 'Світла немає';
  } else {
    color = '#f59e0b';
    statusText = 'Тех. перерва';
  }

  const size = 18;
  const icon = L.divIcon({
    className: '',
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
    html: `<div style="
      width:${size}px;height:${size}px;
      background:${color};
      border:3px solid white;
      transform:rotate(45deg);
      box-shadow:0 4px 12px rgba(0,0,0,0.8);
    "></div>`,
  });
  const marker = L.marker([point.lat, point.lng], { icon });

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
      ${groupInfo}
      ${channelLink}
      <div style="margin-top:4px;font-size:0.7em;color:#a8a29e;">svitlobot</div>
    </div>
  `);

  return marker;
}

async function loadSvitlobot() {
  try {
    const res = await fetch(SVITLOBOT_API);
    const raw = await res.text();
    const points = parseSvitlobotData(raw);

    let sbOnline = 0;
    let sbOffline = 0;

    for (const point of points) {
      const existing = svitlobotMarkers[point.id];
      if (existing) {
        svitlobotClusterGroup.removeLayer(existing);
      }
      const marker = createSvitlobotMarker(point);
      svitlobotMarkers[point.id] = marker;
      svitlobotClusterGroup.addLayer(marker);

      if (point.is_online) sbOnline++;
      else sbOffline++;
    }

    updateSvitlobotStats(points.length, sbOnline, sbOffline);
  } catch (e) {
    console.error('Failed to load Svitlobot data:', e);
  }
}

function updateSvitlobotStats(total, online, offline) {
  let badge = document.getElementById('svitlobot-stats');
  if (!badge) {
    badge = document.createElement('div');
    badge.id = 'svitlobot-stats';
    const statsBadge = document.getElementById('stats-badge');
    if (statsBadge) {
      badge.style.cssText = statsBadge.style.cssText || '';
      badge.className = statsBadge.className || '';
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
