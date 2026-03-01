import http from "node:http"
import { initBrowser, closeBrowser, getOutageInfo, getBrowser, getKyivContext } from "./scraper.js"
import { getCitySuggestions, getStreetSuggestions, getHouseSuggestions, getKyivStreetSuggestions, getKyivHouseSuggestions } from "./lookup/scraper.js"

const PORT = process.env.PORT || 3000
const VALID_REGIONS = new Set(["k", "kr", "dn", "o", "d"])
const LOOKUP_TIMEOUT_MS = 30_000

console.log("Launching browser...")
await initBrowser()
console.log("Browser ready. Region tabs will open on first request.")

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const start = Date.now()

  const log = (status, extra = "") =>
    console.log(`${new Date().toISOString()} ${req.method} ${url.pathname}${url.search} → ${status} (${Date.now() - start}ms)${extra}`)

  if (url.pathname === "/health" && req.method === "GET") {
    res.writeHead(200, { "Content-Type": "application/json" })
    res.end(JSON.stringify({ status: "ok" }))
    log(200)
    return
  }

  if (url.pathname === "/suggest" && req.method === "GET") {
    const region = (url.searchParams.get("region") || "kr").toLowerCase()
    const city = url.searchParams.get("city") || ""
    const street = url.searchParams.get("street") || ""
    const q = url.searchParams.get("q") || ""

    if (!VALID_REGIONS.has(region)) {
      res.writeHead(400, { "Content-Type": "application/json" })
      res.end(JSON.stringify({ error: `Unknown region "${region}". Valid: k, kr, dn, o, d` }))
      log(400)
      return
    }

    if (!q) {
      res.writeHead(400, { "Content-Type": "application/json" })
      res.end(JSON.stringify({ error: "Missing required param: q (partial search query)" }))
      log(400)
      return
    }

    // Kyiv (region "k") has no city step — form starts at street.
    // city param holds the selected street value when querying house_num.
    //
    // Other regions:
    //   city + street → house suggestions
    //   city only     → street suggestions (city is the exact selected value)
    //   neither       → city suggestions
    let what, scraperCall
    if (region === "k") {
      const kCtx = getKyivContext()
      if (!kCtx) {
        res.writeHead(503, { "Content-Type": "application/json" })
        res.end(JSON.stringify({ error: "Kyiv lookup unavailable — context not initialized" }))
        log(503)
        return
      }
      if (city || street) {
        what = "houses"
        console.log(`[k] House lookup (Kyiv) — street="${city || street}" q="${q}"`)
        scraperCall = getKyivHouseSuggestions(kCtx, city || street, q)
      } else {
        what = "streets"
        console.log(`[k] Street lookup (Kyiv) — q="${q}"`)
        scraperCall = getKyivStreetSuggestions(kCtx, q)
      }
    } else if (city && street) {
      const browser = getBrowser()
      what = "houses"
      console.log(`[${region}] House lookup — city="${city}" street="${street}" q="${q}"`)
      scraperCall = getHouseSuggestions(browser, region, city, street, q)
    } else if (city) {
      const browser = getBrowser()
      what = "streets"
      console.log(`[${region}] Street lookup — city="${city}" q="${q}"`)
      scraperCall = getStreetSuggestions(browser, region, city, q)
    } else {
      const browser = getBrowser()
      what = "cities"
      console.log(`[${region}] City lookup — q="${q}"`)
      scraperCall = getCitySuggestions(browser, region, q)
    }

    try {
      let timer
      const suggestions = await Promise.race([
        scraperCall,
        new Promise((_, reject) => {
          timer = setTimeout(() => reject(new Error("Lookup timed out")), LOOKUP_TIMEOUT_MS)
        }),
      ]).finally(() => clearTimeout(timer))

      console.log(`[${region}] ${suggestions.length} ${what} suggestion(s) found`)
      res.writeHead(200, { "Content-Type": "application/json" })
      res.end(JSON.stringify({ suggestions }))
      log(200)
    } catch (error) {
      console.error(`[${region}] Lookup failed:`, error.message)
      res.writeHead(500, { "Content-Type": "application/json" })
      res.end(JSON.stringify({ error: error.message }))
      log(500)
    }
    return
  }

  if (url.pathname !== "/outage" || req.method !== "GET") {
    res.writeHead(404, { "Content-Type": "application/json" })
    res.end(JSON.stringify({ error: "Not found" }))
    log(404)
    return
  }

  const region = url.searchParams.get("region") || "kr"
  const city = url.searchParams.get("city")
  const street = url.searchParams.get("street")
  const house = url.searchParams.get("house")

  if (!VALID_REGIONS.has(region)) {
    res.writeHead(400, { "Content-Type": "application/json" })
    res.end(JSON.stringify({ error: `Unknown region "${region}". Valid values: k, kr, dn, o, d` }))
    log(400)
    return
  }

  if ((region !== "k" && !city) || !street || !house) {
    res.writeHead(400, { "Content-Type": "application/json" })
    res.end(
      JSON.stringify({ error: "Missing required params: city (except Kyiv), street, house" })
    )
    log(400)
    return
  }

  try {
    const info = await getOutageInfo({ region, city, street })

    if (!info?.data) {
      console.error(`[${region}] Unexpected DTEK response:`, JSON.stringify(info))
      res.writeHead(502, { "Content-Type": "application/json" })
      res.end(JSON.stringify({ error: "Failed to get outage data from DTEK" }))
      log(502)
      return
    }

    const houseData = info.data[house] || {}
    const { sub_type, start_date, end_date, type, sub_type_reason } = houseData
    const isOutage =
      sub_type !== "" || start_date !== "" || end_date !== "" || type !== ""

    res.writeHead(200, { "Content-Type": "application/json" })
    res.end(
      JSON.stringify({ isOutage, data: { sub_type, start_date, end_date, sub_type_reason }, updateTimestamp: info.updateTimestamp })
    )
    log(200, ` isOutage=${isOutage}`)
  } catch (error) {
    res.writeHead(500, { "Content-Type": "application/json" })
    res.end(JSON.stringify({ error: error.message }))
    log(500)
    console.error(error)
  }
})

server.listen(PORT, () => {
  console.log(`Service running on http://localhost:${PORT}`)
  console.log(`  GET /outage?region=o&city=...&street=...&house=...`)
  console.log(`  GET /suggest?region=o&q=...`)
  console.log(`  GET /suggest?region=o&city=...&q=...`)
  console.log(`  GET /suggest?region=o&city=...&street=...&q=...`)
})

async function shutdown() {
  console.log("Shutdown signal received — closing browser tabs and exiting...")
  await closeBrowser()
  process.exit(0)
}

process.on("SIGTERM", shutdown)
process.on("SIGINT", shutdown)

process.on("unhandledRejection", (reason) => {
  console.error("Unhandled rejection:", reason)
})
process.on("uncaughtException", (err) => {
  console.error("Uncaught exception:", err)
})
