// --- Map initialization ---
const map = L.map('map', {
  zoomControl: true,
  attributionControl: false,
}).setView([50.45, 30.52], 6);

L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
  maxZoom: 19,
}).addTo(map);

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
  online:      '#16a34a',
  onlineRing:  'rgba(22, 163, 74, 0.25)',
  offline:     '#dc2626',
  offlineRing: 'rgba(220, 38, 38, 0.25)',
};

function createMarker(monitor) {
  const color = monitor.is_online ? COLORS.online : COLORS.offline;
  const ring  = monitor.is_online ? COLORS.onlineRing : COLORS.offlineRing;

  const marker = L.circleMarker([monitor.lat, monitor.lng], {
    radius: 7,
    fillColor: color,
    fillOpacity: 0.85,
    color: ring,
    weight: 5,
    opacity: 0.7,
  });

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

// --- Initialize ---
loadMonitors();

// Poll every 30 seconds.
setInterval(loadMonitors, 30000);
