# DTEK Outage Service(shutdown msg checker)

HTTP service with two endpoints: outage status check and address autocomplete. It keeps a persistent Playwright browser with pre-loaded tabs — one per region — so outage requests get a fast AJAX call instead of a cold browser launch. The suggest endpoint drives DTEK's form UI directly to resolve city/street/house names.

## Start

```bash
npm run service
# or
node ./service/index.js
```

Default port is `3000`. Override with `PORT` env var.

## Endpoints

### `GET /outage` — outage status

```
GET /outage?region=<region>&city=<city>&street=<street>&house=<house>
```

### Parameters

| Param    | Required | Default | Description |
|----------|----------|---------|-------------|
| `region` | no       | `kr`    | DTEK region code (see table below) |
| `city`   | no*      | —       | Full city name as shown on the DTEK site, e.g. `м. Одеса`. Can be omitted for region `k` (Kyiv city). |
| `street` | yes      | —       | Full street name as shown on the DTEK site, e.g. `вул. Корольова Академіка` |
| `house`  | yes      | —       | House number, e.g. `12` |

### Region codes

| Code | Company | Coverage |
|------|---------|----------|
| `k`  | DTEK KEM  | Kyiv city |
| `kr` | DTEK KREM | Kyiv region |
| `dn` | DTEK DNEM | Dnipro |
| `o`  | DTEK OEM  | Odesa |
| `d`  | DTEK DEM  | Donetsk region |

### Examples

Odesa:
```
GET /outage?region=o&city=м.%20Одеса&street=вул.%20Корольова%20Академіка&house=12
```

Kyiv city (region `k`, `city` can be left empty):
```
GET /outage?region=k&city=&street=вул.%20Хрещатик&house=22
```

### Response — 200 OK

Stabilisation outage (scheduled):
```json
{
  "isOutage": true,
  "data": {
    "sub_type": "Стабілізаційне відключення (Згідно графіку погодинних відключень)",
    "start_date": "14:46 01.03.2026",
    "end_date": "20:00 01.03.2026",
    "sub_type_reason": ["GPV45.1"]
  },
  "updateTimestamp": "16:18 01.03.2026"
}
```

Emergency outage (no schedule):
```json
{
  "isOutage": true,
  "data": {
    "sub_type": "Екстренні відключення (Аварійне без застосування графіку погодинних відключень)",
    "start_date": "15:00 01.03.2026",
    "end_date": "21:26 01.03.2026",
    "sub_type_reason": ["GPV2.1"]
  },
  "updateTimestamp": "16:32 01.03.2026"
}
```

No outage, address belongs to multiple GPV groups:
```json
{
  "isOutage": false,
  "data": {
    "sub_type": "",
    "start_date": "",
    "end_date": "",
    "sub_type_reason": ["GPV6.1", "GPV4.1"]
  },
  "updateTimestamp": "16:32 01.03.2026"
}
```

`isOutage` is `true` when any of `sub_type`, `start_date`, or `end_date` is non-empty.

`sub_type_reason` is an array of GPV group codes assigned to the address. An address can belong to more than one GPV group simultaneously — in that case the array contains multiple entries (e.g. `["GPV6.1", "GPV4.1"]`). The array may be empty when no group data is available.

### Error responses

| Status | Meaning |
|--------|---------|
| 400    | Missing required param (`city`, `street`, or `house`) |
| 404    | Unknown path or method |
| 500    | Unexpected scraper error (message in `error` field) |
| 502    | DTEK returned no usable data (possible site issue) |

---

### `GET /suggest` — address autocomplete

Drives the DTEK address form UI to return autocomplete suggestions. Used to resolve a partial user input into the exact string that `/outage` expects. The form is a three-level cascade: city → street → house. Which level is queried depends on which params are present.

```
GET /suggest?region=<region>&q=<query>                        — city suggestions
GET /suggest?region=<region>&city=<city>&q=<query>            — street suggestions
GET /suggest?region=<region>&city=<city>&street=<street>&q=<query>  — house suggestions
```

For region `k` (Kyiv), there is no city step — the form starts at street:

```
GET /suggest?region=k&q=<query>                          — street suggestions
GET /suggest?region=k&street=<selected-street>&q=<query> — house suggestions
```

#### Parameters

| Param    | Required | Description |
|----------|----------|-------------|
| `region` | no (default `kr`) | Region code, same as `/outage` |
| `q`      | yes      | Partial search string |
| `city`   | no       | Exact city name (from a previous city suggestion). Presence triggers street-level lookup. Not used for Kyiv. |
| `street` | no       | Exact street name (from a previous street suggestion). Presence (with `city`) triggers house-level lookup. For Kyiv: presence alone triggers house-level lookup. |

#### Response — 200 OK

```json
{ "suggestions": ["м. Одеса"] }
```

```json
{ "suggestions": ["вул. Корольова Академіка", "вул. Корольова Академіка (Пересип)"] }
```

Kyiv street (no city step):
```
GET /suggest?region=k&q=хрещатик
```
```json
{ "suggestions": ["вул. Хрещатик"] }
```

Full Kyiv flow — resolve street and house, then check outage:
```
GET /suggest?region=k&q=хрещатик                               → ["вул. Хрещатик"]
GET /suggest?region=k&street=вул.%20Хрещатик&q=22              → ["22", ...]
GET /outage?region=k&street=вул.%20Хрещатик&house=22
```

```json
{ "suggestions": ["12", "12А", "12Б"] }
```

`suggestions` is an array of strings ready to pass as param values to the next level or to `/outage`. Empty array means no matches.

#### Error responses

| Status | Meaning |
|--------|---------|
| 400    | Missing `q`, or unknown region |
| 500    | Scraper error or 30-second lookup timeout |
| 503    | Kyiv context not initialized (browser not ready) |

---

## How it works

### Startup (`index.js`)

1. `initBrowser()` is called with top-level `await` before the HTTP server starts.
2. Playwright launches a headless Chromium instance.
3. For each of the 5 regions a new browser tab is opened and navigated to the region's DTEK shutdowns page (`page.goto`, `waitUntil: "load"`).
4. After each tab loads, its cookies are logged so you can see expiry times at a glance.
5. The HTTP server starts listening only after all tabs are ready.

The lookup scraper (`lookup/scraper.js`) maintains its own separate page pool — tabs are opened lazily on the first `/suggest` request for a region, not at startup.

### `/outage` per-request flow (`scraper.js → getOutageInfo`)

```
Request
  └─ getPage(region)          — returns the pre-loaded tab (or reloads if closed)
  └─ fetchInfo(page, ...)     — extracts CSRF token, POSTs to /ua/ajax
       ├─ waitForSelector('meta[name="csrf-token"]')
       ├─ page.evaluate(fetch("/ua/ajax", { method: "getHomeNum", ... }))
       └─ returns raw JSON from DTEK
  └─ result.result === false? — stale session detected
       └─ reloadPage(region)  — reloads the tab to refresh cookies
       └─ fetchInfo(...)      — retries once with fresh session
  └─ derive isOutage, return JSON to caller
```

Every `fetchInfo` call is wrapped in a 15-second timeout. If DTEK doesn't respond in time, the request fails with a 500 and the timeout message.

### Session handling

DTEK uses Incapsula WAF cookies (`incap_ses_*`, `visid_incap_*`) that expire in ~30 minutes, plus a `dtek-oem` session cookie. When a session expires the API returns `{"result": false, "text": "Error"}`.

The scraper detects this and automatically:
1. Logs `Stale session for region "X", reloading tab and retrying...`
2. Reloads the tab (`page.reload`) to get fresh cookies from Incapsula.
3. Retries the same AJAX call once.

### `/suggest` per-request flow (`lookup/scraper.js`)

Each region has one persistent tab in a pool. Operations on the same region's tab are serialised via a Promise queue — the current operation must finish before the next one starts (the tab state must be clean before typing new input). On error the tab is nulled out and recreated on the next request.

For each lookup level the scraper:
1. Clears the form (resets from previous request state).
2. Types the query into the appropriate input and reads back the autocomplete dropdown.
3. For deeper levels (street, house): also clicks the first suggestion to select it and unlock the next field, before moving to the next step.

Suggestions are text-fixed before returning: CSS `text-transform: capitalize` on the page uppercases prefixes like `М.`, `Вул.` — the scraper lowercases the leading abbreviation back to what DTEK's API expects (`м.`, `вул.`).

### Concurrency safety

**Outage scraper:** if two requests arrive simultaneously for the same region while a reload is in progress, only one actual reload runs. The second caller waits on the same `Promise` via a `reloadLocks` Map, then proceeds with the freshly loaded tab.

**Lookup scraper:** a Promise queue (`tail`) on each pool entry ensures only one form interaction runs per region at a time. Concurrent requests queue automatically.

### Logging

Every HTTP request is logged on completion with an ISO timestamp:

```
2026-03-01T16:18:00.123Z GET /outage?region=o&city=...&street=... → 200 (312ms) isOutage=false
2026-03-01T16:18:01.456Z GET /suggest?region=o&city=...&q=... → 200 (87ms)
```

Inside the outage scraper, each fetch is broken down:

```
[o] CSRF token ready (8ms) — sending AJAX for "вул. Корольова Академіка"...
[o] AJAX response in 304ms — HTTP 200, result: true
```

Inside the lookup scraper, tab lifecycle and total form interaction time are logged:

```
[lookup:o] Opening new lookup tab...
[lookup:o] Lookup tab ready (3241ms)
[lookup:o] Done (412ms)
[lookup:o] Failed (3001ms): Lookup timed out
```

Unhandled promise rejections and uncaught exceptions are also logged before the process exits.

### Shutdown

`SIGTERM` and `SIGINT` both trigger a graceful shutdown: all browser tabs are closed, the browser process exits, then the Node process exits with code 0.
