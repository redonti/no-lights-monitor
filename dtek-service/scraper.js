import { chromium } from "playwright"

const shutdownsPages = {
  k:  "https://www.dtek-kem.com.ua/ua/shutdowns",
  kr: "https://www.dtek-krem.com.ua/ua/shutdowns",
  dn: "https://www.dtek-dnem.com.ua/ua/shutdowns",
  o:  "https://www.dtek-oem.com.ua/ua/shutdowns",
  d:  "https://www.dtek-dem.com.ua/ua/shutdowns",
}

const SCRAPE_TIMEOUT_MS = 15_000

// Kyiv's DTEK site (dtek-kem.com.ua) uses Imperva/Incapsula WAF which checks timezone
// consistency against the locale. A plain headless browser gets blocked, but setting
// timezoneId: "Europe/Kyiv" + waitUntil: "networkidle" lets the JS challenge complete.
const KYIV_CONTEXT_OPTIONS = {
  userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
  locale: "uk-UA",
  timezoneId: "Europe/Kyiv",
}

let browser = null
let kyivContext = null          // isolated context with Kyiv timezone, needed for Imperva bypass
const regionPages = new Map()   // region -> page
const reloadLocks = new Map()   // region -> Promise (prevents concurrent reloads)
let reinitLock = null            // Promise (prevents concurrent browser reinits)
const lastUsed = new Map()      // region -> Date.now() of last access
const IDLE_TAB_MS = 15 * 60 * 1000
let idleCloserTimer = null

function logCookies(region, cookies) {
  const now = Date.now() / 1000
  console.log(`Cookies for region "${region}":`)
  for (const c of cookies) {
    if (c.expires === -1) {
      console.log(`  ${c.name}: session cookie (no expiry)`)
    } else {
      const expiresIn = Math.round((c.expires - now) / 60)
      const expiresAt = new Date(c.expires * 1000).toISOString()
      console.log(`  ${c.name}: expires at ${expiresAt} (in ~${expiresIn} min)`)
    }
  }
}

async function ensureBrowser() {
  if (browser?.isConnected()) return
  if (reinitLock) {
    await reinitLock
    return
  }
  reinitLock = (async () => {
    console.log("Chromium process disconnected — reinitializing browser and reloading all region tabs...")
    browser = null
    regionPages.clear()
    await initBrowser()
  })()
  try {
    await reinitLock
  } finally {
    reinitLock = null
  }
}

export async function initBrowser() {
  browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] })
  // Kyiv requires timezone-matched context to pass Imperva's WAF JS challenge.
  // Creating the context is cheap (no tab); the page opens lazily on first request.
  kyivContext = await browser.newContext(KYIV_CONTEXT_OPTIONS)
  idleCloserTimer = setInterval(closeIdleTabs, 5 * 60 * 1000)
}

async function closeIdleTabs() {
  const now = Date.now()
  for (const [region, page] of regionPages) {
    if (now - (lastUsed.get(region) ?? 0) > IDLE_TAB_MS && !page.isClosed()) {
      console.log(`[${region}] Tab idle for 15min — closing to free memory`)
      await page.close().catch(() => {})
      regionPages.delete(region)
      lastUsed.delete(region)
    }
  }
}

async function reloadPage(region, url) {
  // If a reload is already in progress for this region, wait for it and return
  if (reloadLocks.has(region)) {
    console.log(`[${region}] Tab reload already in progress, waiting for it to finish...`)
    await reloadLocks.get(region)
    return
  }

  const promise = (async () => {
    const existing = regionPages.get(region)
    if (existing && !existing.isClosed()) {
      await existing.reload({ waitUntil: "load" }).catch((err) => {
        console.error(`[${region}] Tab reload failed — page may be in a broken state:`, err.message)
      })
      console.log(`[${region}] Tab reloaded — session cookies refreshed`)
    } else {
      const page = region === "k"
        ? await kyivContext.newPage()
        : await browser.newPage()
      const waitUntil = region === "k" ? "networkidle" : "load"
      await page.goto(url ?? shutdownsPages[region], { waitUntil, timeout: 60_000 })
      regionPages.set(region, page)
      console.log(`[${region}] New tab opened and page loaded: ${url ?? shutdownsPages[region]}`)
    }
  })()

  reloadLocks.set(region, promise)
  try {
    await promise
  } finally {
    reloadLocks.delete(region)
  }
}

export function getBrowser() {
  return browser
}

export function getKyivContext() {
  return kyivContext
}

export async function closeBrowser() {
  clearInterval(idleCloserTimer)
  idleCloserTimer = null
  for (const page of regionPages.values()) {
    await page.close().catch(() => {})
  }
  regionPages.clear()
  await kyivContext?.close()
  kyivContext = null
  await browser?.close()
  browser = null
}

async function getPage(region) {
  const page = regionPages.get(region)

  if (!page || page.isClosed()) {
    const reason = !page ? "Tab not yet open — opening lazily..." : "Tab was closed unexpectedly — reopening..."
    console.log(`[${region}] ${reason}`)
    await reloadPage(region, shutdownsPages[region] ?? shutdownsPages["kr"])
    const opened = regionPages.get(region)
    const cookies = region === "k"
      ? await kyivContext.cookies()
      : await opened.context().cookies()
    logCookies(region, cookies)
    lastUsed.set(region, Date.now())
    return opened
  }

  lastUsed.set(region, Date.now())
  return page
}

function withTimeout(promise, ms) {
  let timer
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`Scraping timed out after ${ms}ms`)), ms)
    }),
  ]).finally(() => clearTimeout(timer))
}

async function fetchInfo(page, { region, city, street }) {
  const t0 = Date.now()
  const csrfTokenTag = await page.waitForSelector('meta[name="csrf-token"]', {
    state: "attached",
  })
  const csrfToken = await csrfTokenTag.getAttribute("content")
  console.log(`[${region}] CSRF token ready (${Date.now() - t0}ms) — sending AJAX for "${street}"...`)

  const result = await page.evaluate(
    async ({ region, city, street, csrfToken }) => {
      const formData = new URLSearchParams()
      formData.append("method", "getHomeNum")

      if (region !== "k") {
        formData.append("data[0][name]", "city")
        formData.append("data[0][value]", city)
      }

      formData.append("data[1][name]", "street")
      formData.append("data[1][value]", street)
      formData.append("data[2][name]", "updateFact")
      const now = new Date()
      const updateFact =
        now.toLocaleDateString("uk-UA").replace(/\//g, ".") +
        " " +
        now.toLocaleTimeString("uk-UA", { hour: "2-digit", minute: "2-digit" })
      formData.append("data[2][value]", updateFact)

      const response = await fetch("/ua/ajax", {
        method: "POST",
        headers: {
          "x-requested-with": "XMLHttpRequest",
          "x-csrf-token": csrfToken,
        },
        body: formData,
      })
      const text = await response.text()
      try {
        return { status: response.status, body: JSON.parse(text) }
      } catch {
        // Response was HTML (redirect to login page) — treat as stale session
        return { status: 0, body: { result: null } }
      }
    },
    { region, city, street, csrfToken }
  )
  const { status, body } = result
  const suffix = body.result === false ? `, text: "${body.text}"` : ""
  console.log(`[${region}] AJAX response in ${Date.now() - t0}ms — HTTP ${status}, result: ${body.result}${suffix}`)
  return { status, body }
}

export async function getOutageInfo({ region, city, street }) {
  if (!browser) throw new Error("Browser not initialized.")
  await ensureBrowser()

  const key = String(region).toLowerCase()
  const page = await getPage(key)
  const { status, body } = await withTimeout(
    fetchInfo(page, { region: key, city, street }),
    SCRAPE_TIMEOUT_MS
  )

  // Non-200 means an auth/session failure (redirect to login, 401, etc.) — reload and retry once.
  // A 200 with result:false means the street/city wasn't recognised by DTEK — no point reloading.
  if (status !== 200) {
    console.log(`[${key}] HTTP ${status} — stale session detected, reloading tab and retrying once...`)
    await reloadPage(key, shutdownsPages[key] ?? shutdownsPages["kr"])
    const freshPage = regionPages.get(key)
    const { body: retryBody } = await withTimeout(
      fetchInfo(freshPage, { region: key, city, street }),
      SCRAPE_TIMEOUT_MS
    )
    return retryBody
  }

  return body
}
