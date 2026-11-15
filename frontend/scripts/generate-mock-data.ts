// Generates synthetic incidents for mock mode (until the real backend is up).
// Points fall inside real country outlines, weighted by where each hazard
// actually happens. Deterministic (seeded) so every run gives the same data.
//
// Usage: npx tsx scripts/generate-mock-data.ts
// Reads  public/data/countries.geojson, public/data/countries-meta.json
// Writes src/mocks/data/incidents.json  (compact rows, see MockRow below)
import fs from 'node:fs'
import path from 'node:path'

type TypeId = 'earthquake' | 'volcano' | 'flood' | 'storm' | 'wildfire' | 'landslide'
/** [id, typeIndex, date YYYY-MM-DD, lon, lat, country|null, magnitude|null] */
type MockRow = [string, number, string, number, number, string | null, number | null]

const TYPES: TypeId[] = ['earthquake', 'volcano', 'flood', 'storm', 'wildfire', 'landslide']
const LAST_DATE = '2026-11-14'
const LAST_YEAR = 2026

const root = path.resolve(import.meta.dirname, '..')
const countries = JSON.parse(fs.readFileSync(path.join(root, 'public/data/countries.geojson'), 'utf8'))
const meta: Record<string, { mainBbox: number[]; bbox: number[] }> = JSON.parse(
  fs.readFileSync(path.join(root, 'public/data/countries-meta.json'), 'utf8'),
)

// --- seeded RNG -------------------------------------------------------------
let seed = 20261115
function rand() {
  seed |= 0
  seed = (seed + 0x6d2b79f5) | 0
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296
}
const between = (a: number, b: number) => a + rand() * (b - a)
function pickWeighted<T>(entries: [T, number][]): T {
  const total = entries.reduce((s, [, w]) => s + w, 0)
  let r = rand() * total
  for (const [v, w] of entries) if ((r -= w) <= 0) return v
  return entries[entries.length - 1][0]
}

// --- geometry ---------------------------------------------------------------
type Ring = number[][]
const polysById = new Map<string, Ring[][]>()
for (const f of countries.features) {
  const g = f.geometry
  polysById.set(f.properties.ADM0_A3, g.type === 'Polygon' ? [g.coordinates] : g.coordinates)
}

function inRing(x: number, y: number, ring: Ring) {
  let inside = false
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const [xi, yi] = ring[i]
    const [xj, yj] = ring[j]
    if (yi > y !== yj > y && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) inside = !inside
  }
  return inside
}
function inPolys(x: number, y: number, polys: Ring[][]) {
  return polys.some((p) => inRing(x, y, p[0]) && !p.slice(1).some((h) => inRing(x, y, h)))
}
function countryAt(x: number, y: number): string | null {
  for (const [id, polys] of polysById) {
    const b = meta[id].bbox
    if (x < b[0] || x > b[2] || y < b[1] || y > b[3]) continue
    if (inPolys(x, y, polys)) return id
  }
  return null
}
function randomPointIn(id: string): [number, number] | null {
  const polys = polysById.get(id)
  if (!polys) return null
  // Mostly the main landmass, sometimes anywhere in the full extent (islands).
  const b = rand() < 0.8 ? meta[id].mainBbox : meta[id].bbox
  for (let i = 0; i < 400; i++) {
    const x = between(b[0], b[2])
    const y = between(b[1], b[3])
    if (inPolys(x, y, polys)) return [x, y]
  }
  return null
}

// Ocean zones for offshore earthquakes and storms: [minLon, minLat, maxLon, maxLat, weight]
const OCEAN_ZONES: Record<'earthquake' | 'storm', number[][]> = {
  earthquake: [
    [140, 30, 160, 50, 4], [120, -10, 160, 10, 4], [-180, -35, -170, -10, 3], [165, -25, 180, -10, 2],
    [-80, -45, -70, -10, 3], [-180, 50, -150, 56, 2], [-130, 35, -120, 50, 1], [-35, -60, -15, 60, 1.5],
    [90, -10, 105, 5, 2], [-95, 5, -80, 15, 1.5],
  ],
  storm: [
    [125, 10, 150, 30, 5], [-80, 15, -50, 35, 4], [-110, 10, -90, 20, 2], [85, 10, 95, 20, 2],
    [55, -25, 75, -10, 1.5], [150, -25, 170, -12, 1.5], [-40, 40, -10, 60, 1],
  ],
}

function randomOceanPoint(zones: number[][]): [number, number] {
  for (let i = 0; i < 200; i++) {
    const z = pickWeighted(zones.map((zz) => [zz, zz[4]] as [number[], number]))
    const x = between(z[0], z[2])
    const y = between(z[1], z[3])
    if (!countryAt(x, y)) return [x, y]
  }
  return [between(-160, -130), between(-20, 20)]
}

// --- hazard profiles --------------------------------------------------------
type Profile = {
  count: number
  fromYear: number
  oceanShare: number
  hotspots: Record<string, number>
  magnitude: () => number | null
}

const W = (s: string) =>
  Object.fromEntries(s.trim().split(/\s+/).map((tok) => { const [k, v] = tok.split(':'); return [k, Number(v)] }))

const PROFILES: Record<TypeId, Profile> = {
  earthquake: {
    count: 5200, fromYear: 1950, oceanShare: 0.35,
    hotspots: W(`IDN:14 JPN:12 CHN:7 PHL:7 CHL:6 USA:6 PER:4 MEX:5 PNG:6 TUR:4 IRN:5 RUS:4 NZL:3 TWN:3
      GRC:2 ITA:2 ECU:2 COL:2 GTM:2 AFG:2 PAK:2 IND:2 NPL:1.5 VUT:2 SLB:2 TON:1.5 FJI:1 ARG:2 BOL:1
      MMR:1.5 IRQ:0.8 HTI:0.5 DZA:0.6 CRI:1 NIC:1 SLV:0.8 TJK:0.8 KGZ:0.6`),
    // Gutenberg–Richter (b≈1) above Mw 5.0
    magnitude: () => Math.min(8.8, Math.round((5 - Math.log10(1 - rand() * 0.999)) * 10) / 10),
  },
  volcano: {
    count: 700, fromYear: 1950, oceanShare: 0,
    hotspots: W(`IDN:14 JPN:10 USA:8 RUS:7 CHL:5 PHL:4 PNG:4 ISL:3 ECU:3 MEX:2 GTM:3 NIC:2 NZL:2 ITA:2
      VUT:2 COL:1.5 CRI:1.5 ETH:1 COD:1 ERI:0.5 TZA:0.5 CMR:0.4 ARG:1 PER:1 SLV:1 TON:1`),
    magnitude: () => pickWeighted([[0, 0.3], [1, 0.3], [2, 0.2], [3, 0.12], [4, 0.06], [5, 0.02]]),
  },
  flood: {
    count: 3600, fromYear: 1985, oceanShare: 0,
    hotspots: W(`IND:10 CHN:10 USA:6 IDN:6 BGD:5 PHL:4 PAK:4 BRA:4 VNM:3 NGA:3 THA:2.5 MMR:2 AFG:2 IRN:2
      KEN:2 ETH:2 SOM:1.5 MOZ:1.5 COL:2 PER:1.5 MEX:2 ARG:1.5 AUS:2 DEU:1 ROU:1 ITA:1 FRA:1 GBR:1 ESP:1
      TUR:1.5 ZAF:1 SDN:1.2 NPL:1.5 LKA:1 MYS:1.5 IRQ:0.8 SSD:1 NER:1 TCD:0.8 MLI:0.6 GHA:0.6`),
    magnitude: () => pickWeighted([[1, 0.6], [2, 0.3], [3, 0.1]]),
  },
  storm: {
    count: 2600, fromYear: 1950, oceanShare: 0.3,
    hotspots: W(`USA:12 PHL:10 CHN:8 JPN:7 IND:4 BGD:3 MEX:5 CUB:2 VNM:4 AUS:3 MDG:2 MOZ:1.5 TWN:3 KOR:2
      HTI:1.5 DOM:1.2 PRI:1 JAM:1 HND:1 BHS:1 MMR:1 PAK:0.8 OMN:0.4 NZL:0.8 FJI:1 VUT:0.8 GBR:1 FRA:0.8 DEU:0.6`),
    magnitude: () => pickWeighted([[1, 0.35], [2, 0.25], [3, 0.18], [4, 0.14], [5, 0.08]]),
  },
  wildfire: {
    count: 3200, fromYear: 2000, oceanShare: 0,
    hotspots: W(`USA:10 AUS:9 CAN:7 BRA:7 RUS:8 PRT:3 GRC:3 ESP:3 TUR:2 IDN:4 ZAF:2 CHL:2 BOL:2 ARG:2
      KAZ:2 MNG:1.5 AGO:2 COD:2 ZMB:1.5 MEX:2 ITA:1.5 FRA:1.2 ISR:0.4 DZA:0.6 CHN:1.5 IND:1.5`),
    // Burned area, km² (log-uniform 5–20,000)
    magnitude: () => Math.round(Math.exp(between(Math.log(5), Math.log(20000)))),
  },
  landslide: {
    count: 2400, fromYear: 2007, oceanShare: 0,
    hotspots: W(`IND:8 NPL:5 CHN:7 IDN:6 PHL:5 COL:3 BRA:3 PER:2 USA:3 ITA:2 JPN:3 PAK:2 BGD:1 VNM:2 GTM:1.5
      ECU:1.5 MEX:1.5 BTN:0.8 LKA:1 MMR:1 TWN:1.5 UGA:0.8 KEN:0.6 RWA:0.5 HTI:0.6 CHE:0.6 AUT:0.6 NOR:0.4
      TJK:0.8 KGZ:0.6`),
    // Fatalities (many zero)
    magnitude: () => (rand() < 0.45 ? 0 : Math.floor(Math.exp(between(0, Math.log(400))))),
  },
}

function randomDate(fromYear: number): string {
  // Recorded events grow over time (better sensors, better reporting).
  const years: [number, number][] = []
  for (let y = fromYear; y <= LAST_YEAR; y++) years.push([y, 0.4 + (y - 1950) / 30])
  const y = pickWeighted(years)
  const maxDay = y === LAST_YEAR ? 317 : 365
  const d = new Date(Date.UTC(y, 0, 1 + Math.floor(rand() * maxDay)))
  const iso = d.toISOString().slice(0, 10)
  return iso > LAST_DATE ? LAST_DATE : iso
}

// --- generate ---------------------------------------------------------------
const rows: MockRow[] = []
let skipped = 0
TYPES.forEach((type, ti) => {
  const p = PROFILES[type]
  const hot = Object.entries(p.hotspots).filter(([id]) => polysById.has(id)) as [string, number][]
  for (let i = 0; i < p.count; i++) {
    let lon: number, lat: number, cc: string | null
    if (p.oceanShare > 0 && rand() < p.oceanShare) {
      ;[lon, lat] = randomOceanPoint(OCEAN_ZONES[type as 'earthquake' | 'storm'])
      cc = null
    } else {
      cc = pickWeighted(hot)
      const pt = randomPointIn(cc)
      if (!pt) { skipped++; continue }
      ;[lon, lat] = pt
    }
    const id = `${type.slice(0, 2)}-${String(i).padStart(5, '0')}`
    rows.push([id, ti, randomDate(p.fromYear), Math.round(lon * 1000) / 1000, Math.round(lat * 1000) / 1000, cc, p.magnitude()])
  }
})

rows.sort((a, b) => a[2].localeCompare(b[2]))
const outDir = path.join(root, 'src/mocks/data')
fs.mkdirSync(outDir, { recursive: true })
fs.writeFileSync(path.join(outDir, 'incidents.json'), JSON.stringify({ types: TYPES, rows }))
console.log(`wrote ${rows.length} incidents (${skipped} skipped) to src/mocks/data/incidents.json`)
