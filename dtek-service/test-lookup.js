const BASE = "http://localhost:3000"

// ---- uncomment the region you want to test ----

//const REGION = "o", CITY = "одеса", STREET = "корольова", HOUSE = "70"
//const KNOWN_URL = "http://localhost:3000/outage?region=o&city=%D0%BC.%20%D0%9E%D0%B4%D0%B5%D1%81%D0%B0&street=%D0%B2%D1%83%D0%BB.%20%D0%9A%D0%BE%D1%80%D0%BE%D0%BB%D1%8C%D0%BE%D0%B2%D0%B0%20%D0%90%D0%BA%D0%B0%D0%B4%D0%B5%D0%BC%D1%96%D0%BA%D0%B0&house=70"

 const REGION = "k", CITY = "", STREET = "хрещатик", HOUSE = "22"
 const KNOWN_URL = null

// -----------------------------------------------

async function getJSON(url) {
  const start = Date.now()
  const res = await fetch(url)
  const body = await res.json()
  return { status: res.status, ok: res.ok, body, elapsed: Date.now() - start }
}

// --- Step 1: known correct URL ---
if (KNOWN_URL) {
  console.log("=== Step 1: known correct address ===")
  console.log(`GET ${KNOWN_URL}\n`)
  const step1 = await getJSON(KNOWN_URL)
  if (step1.ok) {
    console.log(`✅ ${step1.status} (${step1.elapsed}ms) — isOutage: ${step1.body.isOutage}`)
  } else {
    console.log(`❌ ${step1.status} (${step1.elapsed}ms) — ${step1.body.error}`)
  }
  console.log()
}

// --- Step 2: resolve address via suggest, then check outage ---
console.log(`=== Step 2: resolving address (REGION=${REGION} CITY=${CITY || "(none)"} STREET=${STREET} HOUSE=${HOUSE}) ===`)

// 2a. city lookup (skipped if CITY is empty — e.g. Kyiv has no city selector)
let correctCity = ""
if (CITY) {
  const cityUrl = `${BASE}/suggest?region=${REGION}&q=${encodeURIComponent(CITY)}`
  console.log(`\nGET ${cityUrl}`)
  const cityRes = await getJSON(cityUrl)
  if (!cityRes.ok || cityRes.body.suggestions?.length === 0) {
    console.log(`❌ City lookup failed (${cityRes.elapsed}ms) — ${cityRes.body.error ?? "no suggestions"}`)
    process.exit(1)
  }
  correctCity = cityRes.body.suggestions[0]
  console.log(`✅ ${cityRes.elapsed}ms — city suggestions: ${JSON.stringify(cityRes.body.suggestions)}`)
  console.log(`   → using "${correctCity}"`)
} else {
  console.log("\n⚠️  No CITY — skipping city lookup")
}

// 2b. street lookup
const streetUrl = correctCity
  ? `${BASE}/suggest?region=${REGION}&city=${encodeURIComponent(correctCity)}&q=${encodeURIComponent(STREET)}`
  : `${BASE}/suggest?region=${REGION}&q=${encodeURIComponent(STREET)}`
console.log(`\nGET ${streetUrl}`)
const streetRes = await getJSON(streetUrl)
if (!streetRes.ok || streetRes.body.suggestions?.length === 0) {
  console.log(`❌ Street lookup failed (${streetRes.elapsed}ms) — ${streetRes.body.error ?? "no suggestions"}`)
  process.exit(1)
}
const correctStreet = streetRes.body.suggestions[0]
console.log(`✅ ${streetRes.elapsed}ms — street suggestions: ${JSON.stringify(streetRes.body.suggestions)}`)
console.log(`   → using "${correctStreet}"`)

// 2c. house lookup
const houseUrl = REGION === "k"
  ? `${BASE}/suggest?region=${REGION}&street=${encodeURIComponent(correctStreet)}&q=${encodeURIComponent(HOUSE)}`
  : `${BASE}/suggest?region=${REGION}&city=${encodeURIComponent(correctCity)}&street=${encodeURIComponent(correctStreet)}&q=${encodeURIComponent(HOUSE)}`
console.log(`\nGET ${houseUrl}`)
const houseRes = await getJSON(houseUrl)
if (!houseRes.ok || houseRes.body.suggestions?.length === 0) {
  console.log(`❌ House lookup failed (${houseRes.elapsed}ms) — ${houseRes.body.error ?? "no suggestions"}`)
  process.exit(1)
}
const correctHouse = houseRes.body.suggestions[0]
console.log(`✅ ${houseRes.elapsed}ms — house suggestions: ${JSON.stringify(houseRes.body.suggestions)}`)
console.log(`   → using "${correctHouse}"`)

// 2d. outage check
const outageUrl = `${BASE}/outage?region=${REGION}&city=${encodeURIComponent(correctCity)}&street=${encodeURIComponent(correctStreet)}&house=${encodeURIComponent(correctHouse)}`
console.log(`\nGET ${outageUrl}`)
const outageRes = await getJSON(outageUrl)
if (outageRes.ok) {
  console.log(`✅ ${outageRes.status} (${outageRes.elapsed}ms) — isOutage: ${outageRes.body.isOutage}`)
} else {
  console.log(`❌ ${outageRes.status} (${outageRes.elapsed}ms) — ${outageRes.body.error}`)
}
