// Trims Natural Earth 1:50m countries to what the map needs.
// Usage: node scripts/prepare-countries.mjs <ne_50m_admin_0_countries.geojson> <out-dir>
// Writes:
//   <out-dir>/countries.geojson      polygons with ADM0_A3 + NAME (feature-state key = ADM0_A3)
//   <out-dir>/countries-meta.json    { [ADM0_A3]: { name, continent, pop, bbox, center } }
import fs from 'node:fs'
import path from 'node:path'

const [input, outDir] = process.argv.slice(2)
if (!input || !outDir) {
  console.error('usage: node scripts/prepare-countries.mjs <input.geojson> <out-dir>')
  process.exit(1)
}

const src = JSON.parse(fs.readFileSync(input, 'utf8'))
const round = (n) => Math.round(n * 1000) / 1000

function roundCoords(c) {
  return typeof c[0] === 'number' ? [round(c[0]), round(c[1])] : c.map(roundCoords)
}

function dedupeRing(ring) {
  const out = []
  for (const p of ring) {
    const last = out[out.length - 1]
    if (!last || last[0] !== p[0] || last[1] !== p[1]) out.push(p)
  }
  return out
}

function cleanGeometry(g) {
  const polys = g.type === 'Polygon' ? [g.coordinates] : g.coordinates
  const cleaned = polys
    .map((poly) => poly.map((ring) => dedupeRing(roundCoords(ring))).filter((r) => r.length >= 4))
    .filter((poly) => poly.length > 0)
  return cleaned.length === 1
    ? { type: 'Polygon', coordinates: cleaned[0] }
    : { type: 'MultiPolygon', coordinates: cleaned }
}

function ringArea(ring) {
  let a = 0
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    a += (ring[j][0] + ring[i][0]) * (ring[j][1] - ring[i][1])
  }
  return Math.abs(a / 2)
}

const features = []
const meta = {}
for (const f of src.features) {
  const p = f.properties
  const id = p.ADM0_A3
  if (!id || id === '-99') continue
  const geometry = cleanGeometry(f.geometry)
  const polys = geometry.type === 'Polygon' ? [geometry.coordinates] : geometry.coordinates
  let minX = 180, minY = 90, maxX = -180, maxY = -90
  for (const poly of polys) for (const [x, y] of poly[0]) {
    minX = Math.min(minX, x); minY = Math.min(minY, y); maxX = Math.max(maxX, x); maxY = Math.max(maxY, y)
  }
  // Fly-to target: bbox of the largest polygon (avoids e.g. France centering on the Atlantic).
  const main = polys.reduce((a, b) => (ringArea(b[0]) > ringArea(a[0]) ? b : a))
  let mx0 = 180, my0 = 90, mx1 = -180, my1 = -90
  for (const [x, y] of main[0]) {
    mx0 = Math.min(mx0, x); my0 = Math.min(my0, y); mx1 = Math.max(mx1, x); my1 = Math.max(my1, y)
  }
  features.push({ type: 'Feature', properties: { ADM0_A3: id, NAME: p.NAME }, geometry })
  meta[id] = {
    name: p.NAME,
    continent: p.CONTINENT,
    pop: p.POP_EST,
    bbox: [minX, minY, maxX, maxY],
    mainBbox: [mx0, my0, mx1, my1],
    center: [round(p.LABEL_X ?? (mx0 + mx1) / 2), round(p.LABEL_Y ?? (my0 + my1) / 2)],
  }
}

fs.mkdirSync(outDir, { recursive: true })
fs.writeFileSync(path.join(outDir, 'countries.geojson'), JSON.stringify({ type: 'FeatureCollection', features }))
fs.writeFileSync(path.join(outDir, 'countries-meta.json'), JSON.stringify(meta))
console.log(`wrote ${features.length} countries to ${outDir}`)
