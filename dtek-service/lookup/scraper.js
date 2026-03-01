const shutdownsPages = {
  k:  "https://www.dtek-kem.com.ua/ua/shutdowns",
  kr: "https://www.dtek-krem.com.ua/ua/shutdowns",
  dn: "https://www.dtek-dnem.com.ua/ua/shutdowns",
  o:  "https://www.dtek-oem.com.ua/ua/shutdowns",
  d:  "https://www.dtek-dem.com.ua/ua/shutdowns",
}

// Persistent lookup page per region, reused between requests.
// A promise queue (tail) ensures only one lookup runs per region at a time.
const pool = new Map() // region → { page: Page | null, tail: Promise }

function getEntry(region) {
  if (!pool.has(region)) {
    pool.set(region, { page: null, tail: Promise.resolve() })
  }
  return pool.get(region)
}

async function openPage(browser, region) {
  console.log(`[lookup:${region}] Opening new lookup tab...`)
  const t0 = Date.now()
  const page = await browser.newPage()
  const waitUntil = region === "k" ? "networkidle" : "load"
  await page.goto(shutdownsPages[region] ?? shutdownsPages["kr"], { waitUntil, timeout: 60_000 })
  const closeBtn = await page.waitForSelector("button.modal__close", { timeout: 3000 }).catch(() => null)
  if (closeBtn) {
    await closeBtn.click()
    await page.waitForTimeout(500)
  }
  console.log(`[lookup:${region}] Lookup tab ready (${Date.now() - t0}ms)`)
  return page
}

async function clearForm(page, region) {
  // Dismiss any open dropdown, then clear the top-level field to reset the whole form.
  await page.keyboard.press("Escape")
  if (region === "k") {
    // Kyiv has no city input — clear street to cascade-reset house_num
    await page.fill('.discon-inputs input[name="street"]', "")
    await page
      .waitForSelector('.discon-inputs input[name="house_num"][disabled]', { timeout: 1000 })
      .catch(() => null)
  } else {
    await page.fill('.discon-inputs input[name="city"]', "")
    // Wait for the street input to become disabled (cascading reset)
    await page
      .waitForSelector('.discon-inputs input[name="street"][disabled]', { timeout: 1000 })
      .catch(() => null)
  }
}

async function withPage(browser, region, fn) {
  const entry = getEntry(region)

  let release
  const slot = new Promise((r) => {
    release = r
  })
  const prev = entry.tail
  entry.tail = slot

  // Wait for any ongoing operation on this region's page
  await prev

  const t0 = Date.now()
  try {
    if (!entry.page || entry.page.isClosed()) {
      entry.page = await openPage(browser, region)
    } else {
      await clearForm(entry.page, region)
    }
    const result = await fn(entry.page)
    console.log(`[lookup:${region}] Done (${Date.now() - t0}ms)`)
    return result
  } catch (err) {
    // Force page recreation on next use if something went wrong
    console.error(`[lookup:${region}] Failed (${Date.now() - t0}ms):`, err.message)
    entry.page = null
    throw err
  } finally {
    release()
  }
}

async function readSuggestions(page, inputName) {
  await page
    .waitForSelector(".autocomplete-items, .autocomplete > div:not(:empty)", {
      timeout: 3000,
    })
    .catch(() => null)

  // innerText respects CSS text-transform:capitalize, giving "М. Одеса".
  // DTEK's API wants lowercase prefixes: "м. Одеса", "вул. Корольова Академіка".
  // So we lowercase only the leading abbreviation (е.g. "М." → "м.").
  const raw = await page.evaluate((name) => {
    const input = document.querySelector(`.discon-inputs input[name="${name}"]`)
    const autocomplete = input?.closest(".autocomplete")
    if (!autocomplete) return []
    return Array.from(autocomplete.querySelectorAll("div, li"))
      .filter((el) => el.querySelector("div, li") === null)
      .map((el) => el.innerText.trim())
      .filter(Boolean)
  }, inputName)

  return raw.map((s) => s.replace(/^([^\s.]+\.)/, (p) => p.toLowerCase()))
}

async function selectSuggestion(page, inputName) {
  const selector = `.discon-inputs input[name="${inputName}"] ~ * div, .discon-inputs input[name="${inputName}"] ~ * li, .autocomplete-items div`
  const item = await page.$(selector)
  if (!item) throw new Error(`No suggestion found to select for input "${inputName}"`)
  await item.click()
}

export async function getCitySuggestions(browser, region, query) {
  return withPage(browser, region, async (page) => {
    await page.fill('.discon-inputs input[name="city"]', query)
    return readSuggestions(page, "city")
  })
}

export async function getStreetSuggestions(browser, region, city, query) {
  return withPage(browser, region, async (page) => {
    await page.fill('.discon-inputs input[name="city"]', city)
    await readSuggestions(page, "city")
    await selectSuggestion(page, "city")

    await page.waitForSelector('.discon-inputs input[name="street"]:not([disabled])', { timeout: 3000 })
    await page.fill('.discon-inputs input[name="street"]', query)
    return readSuggestions(page, "street")
  })
}

export async function getHouseSuggestions(browser, region, city, street, query) {
  return withPage(browser, region, async (page) => {
    await page.fill('.discon-inputs input[name="city"]', city)
    await readSuggestions(page, "city")
    await selectSuggestion(page, "city")

    await page.waitForSelector('.discon-inputs input[name="street"]:not([disabled])', { timeout: 3000 })
    await page.fill('.discon-inputs input[name="street"]', street)
    await readSuggestions(page, "street")
    await selectSuggestion(page, "street")

    await page.waitForSelector('.discon-inputs input[name="house_num"]:not([disabled])', { timeout: 3000 })
    await page.fill('.discon-inputs input[name="house_num"]', query)
    return readSuggestions(page, "house_num")
  })
}

// Kyiv (region "k") has no city input — the form starts directly at street.
export async function getKyivStreetSuggestions(browser, query) {
  return withPage(browser, "k", async (page) => {
    await page.fill('.discon-inputs input[name="street"]', query)
    return readSuggestions(page, "street")
  })
}

export async function getKyivHouseSuggestions(browser, street, query) {
  return withPage(browser, "k", async (page) => {
    await page.fill('.discon-inputs input[name="street"]', street)
    await readSuggestions(page, "street")
    await selectSuggestion(page, "street")

    await page.waitForSelector('.discon-inputs input[name="house_num"]:not([disabled])', { timeout: 3000 })
    await page.fill('.discon-inputs input[name="house_num"]', query)
    return readSuggestions(page, "house_num")
  })
}
