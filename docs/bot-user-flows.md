# Telegram Bot — User Flows

Bot for monitoring power outages. Sends notifications to a Telegram channel when power goes on/off.

---

## Commands

| Command | Description |
|---------|-------------|
| `/start` | Welcome message + command overview |
| `/help` | How the system works, full command list |
| `/create` | Create a new monitor (multi-step flow) |
| `/info` | List all monitors with status; tap any for details |
| `/edit` | Select a monitor to change its settings |
| `/stop` | Pause monitoring for a selected monitor |
| `/resume` | Resume a paused monitor |
| `/test` | Send a test notification to a monitor's channel |
| `/delete` | Permanently delete a monitor |
| `/cancel` | Abort any active conversation flow |

---

## Flow 1 — Create Monitor (`/create`)

### 1a. Heartbeat (ESP / smartphone)

Device sends periodic GET requests to the bot's ping URL.

```
/create
  → Select type: [📡 ESP або смартфон]
  → Enter location address  (or share GPS via 📎)
      ↓ geocoding via OSM
  → Confirm found address, then enter display address text
  → Enter @channel_username
      ↓ bot verifies: channel exists, bot is admin, can post messages
  → Monitor created
      ← Confirmation with unique ping URL:
         GET {base_url}/api/ping/{token}  every 5 min
```

### 1b. Ping (router / server IP)

Bot pings a public IP/hostname itself every 5 minutes.

```
/create
  → Select type: [🌐 Пінг айпі роутера]
  → Enter public IP or hostname
      ↓ DNS resolution → private IP check → ICMP ping test
  → Enter location address  (or share GPS via 📎)
      ↓ geocoding via OSM
  → Confirm found address, then enter display address text
  → Enter @channel_username
      ↓ bot verifies: channel exists, bot is admin, can post messages
  → Monitor created
      ← Confirmation with target IP
```

**Ping target validation errors:**
- Input too short
- Private / NAT IP detected
- DNS resolution failed
- ICMP ping not responding

**Address validation errors:**
- Input too short
- Address not found via geocoding
- Geocoding service error

**Channel validation errors:**
- Channel not found / no public username
- Bot is not an admin
- Bot has no "Post Messages" right

---

## Flow 2 — Edit Monitor (`/edit`)

```
/edit
  → List of monitors (inline buttons)
  → Select monitor
  → Edit menu with options:
```

| Button | Action |
|--------|--------|
| ✏️ Змінити назву | Enter new name |
| 📍 Змінити адресу | Enter new address or share GPS |
| 🔄 Оновити тег каналу | Re-fetches channel username (if channel was renamed) |
| 📍 Показувати / Приховати адресу в сповіщеннях | Toggle address line in status notifications |
| 📊 Публікувати / Не публікувати графік аптайму | Toggle uptime graph posts to channel |
| 🗺 Прибрати / Додати на карту | Toggle visibility on public map |
| ⚡ Група відключень | Configure scheduled outage group (see Flow 3) |

### Edit Name sub-flow
```
[✏️ Змінити назву]
  → Shows current name
  → Enter new name (min length enforced)
  ← "✅ Назву оновлено: {name}"
```

### Edit Address sub-flow
```
[📍 Змінити адресу]
  → Shows current address
  → Enter new address or share GPS
      ↓ geocoding / coordinates extracted
  → Enter display address text
  ← "✅ Адресу оновлено: {address}"
```

---

## Flow 3 — Outage Group Setup (inside Edit)

Links a monitor to a scheduled power outage calendar.

```
[⚡ Група відключень]
  → Select region  (inline buttons from outage-data-ua service)
  → Select outage group within region
  ← "✅ Групу встановлено: {group} ({region})"

Additional toggles appear in edit menu:
  ⚡ Показувати / Приховати графік зі сповіщень
      → Shows outage schedule in status notifications
  🖼 Публікувати / Не публікувати фото графіка в каналі
      → Posts outage schedule photo to channel
```

---

## Flow 4 — Stop / Resume

### Stop
```
/stop
  → List of active monitors
  → Select monitor
  ← "✅ Моніторинг призупинено"
  → Bot posts to channel: "⏸ Моніторинг призупинено"
```

### Resume
```
/resume
  → List of paused monitors
  → Select monitor
      ↓ bot re-checks channel access
  → If access OK:
      ← "✅ Моніторинг відновлено"
      → Bot posts to channel: "▶️ Моніторинг відновлено"
  → If no access:
      ← Error with instructions to re-add bot as admin
```

---

## Flow 5 — Test Notification

```
/test
  → List of monitors with channels
  → Select monitor
  ← "✅ Тест відправлено"
  → Channel receives:
      "🧪 Тестове повідомлення
       Монітор: {name}
       Адреса: {address}
       Якщо ви бачите це — налаштування працює ✅"
```

---

## Flow 6 — Delete Monitor

```
/delete
  → List of all monitors  (with irreversibility warning)
  → Select monitor  → confirmation button
  ← "✅ {name} успішно видалено"
  (all status history permanently deleted)
```

---

## Flow 7 — Info

```
/info
  → Numbered list of all monitors with status:
      🟢 Online | 🔴 Offline | ⏸ Paused
  → Tap monitor
  ← Detail card:
      Name, address, coordinates
      Status + last ping time
      Channel @tag
      Type (ESP Heartbeat / Server Ping)
      For Heartbeat: ping URL
      For Ping: target IP
      Settings panel URL
```

---

## Automatic Notifications (no user action needed)

Sent to the linked channel when monitor status changes.

### Power off
```
🔴 {address} Світла немає
   (воно було {duration})
[📍 address line — if enabled]
[⚡ estimated restoration time — if outage group set]
```

### Power on
```
🟢 {address} Світло з'явилося
   (не було {duration})
[📍 address line — if enabled]
[⚡ next planned outage window — if outage group set]
```

**Quiet hours:** no notifications sent between **23:00–07:00 Kyiv time**.

### Channel access lost (auto-pause)
```
Bot detects it can no longer post to channel
  → Auto-pauses the monitor
  → Posts to channel: "⚠️ Моніторинг призупинено автоматично"
  → DMs the owner:
      "⚠️ Монітор {name} призупинено — бот втратив доступ до каналу.
       Відновіть через /resume після додавання бота як адміна."
```

---

## State Machine (text input routing)

The bot maintains per-user conversation state. Incoming text messages are routed by current state:

| State | Expected input |
|-------|---------------|
| `AwaitingType` | Inline button: Heartbeat or Ping |
| `AwaitingPingTarget` | Public IP address or hostname |
| `AwaitingAddress` | Location string or `lat,lng` raw coordinates |
| `AwaitingManualAddress` | Display address text (after geocoding or GPS) |
| `AwaitingChannel` | `@channel_username` |
| `AwaitingEditName` | New monitor name |
| `AwaitingEditAddress` | New location string or GPS |
| `AwaitingEditManualAddress` | New display address text |

`/cancel` resets state to idle at any point.

---

## Authorisation

- Each monitor has a unique `Token` — used in the heartbeat ping URL.
- Each monitor has a `SettingsToken` — used for the web settings panel.
- Users can only manage their own monitors (matched by Telegram user ID).
