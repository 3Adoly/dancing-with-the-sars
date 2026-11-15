// In-browser stand-in for the team backend, returning exactly the shapes in
// api/openapi.yaml. Used offline (VITE_USE_MOCKS=true) and as a fallback for
// endpoints the live server still answers with 501 (grid, incidents, search).
import { MOCK_ID_BASE } from '@/api/client'
import { sarAvailability, searchSar } from '@/api/asf'
import type {
  BBox,
  Choropleth,
  Grid,
  Incident,
  IncidentPage,
  IncidentSummary,
  IncidentTypeId,
  IncidentTypesResponse,
  Meta,
  SARCoverageTimeline,
  SearchResponse,
  StoriesResponse,
  Timeseries,
} from '@/api/types'
import { loadCountries } from '@/lib/countries'
import { CURATED, type CuratedEvent } from './curated'
import raw from './data/incidents.json'
import { COVERAGE, INCIDENT_TYPES, MISSIONS, SOURCE_BY_TYPE, SOURCE_URL } from './reference'
import { STORIES } from './stories'

const DATASET_VERSION = 1
const MIN_YEAR = 1900
const MAX_YEAR = new Date().getUTCFullYear()

interface Row {
  id: number
  type: IncidentTypeId
  date: string
  year: number
  lon: number
  lat: number
  iso3: string | null
  magnitude: number | null
  curated?: CuratedEvent
}

type RawRow = [string, number, string, number, number, string | null, number | null]
const rawTypes = raw.types as IncidentTypeId[]

const ALL: Row[] = [
  ...CURATED.map((c, i) => ({
    id: MOCK_ID_BASE + 1 + i, type: c.type, date: c.date, year: Number(c.date.slice(0, 4)),
    lon: c.lon, lat: c.lat, iso3: c.country, magnitude: c.magnitude, curated: c,
  })),
  ...(raw.rows as RawRow[]).map(([, t, date, lon, lat, iso3, magnitude], i) => ({
    id: MOCK_ID_BASE + 100_000 + i, type: rawTypes[t], date, year: Number(date.slice(0, 4)), lon, lat, iso3, magnitude,
  })),
]
const BY_ID = new Map(ALL.map((r) => [r.id, r]))

// --- helpers ----------------------------------------------------------------------
const fail = (status: number, code: string, message: string) => Object.assign(new Error(message), { status, code })

function parseTypes(v: string | undefined, required: boolean): IncidentTypeId[] {
  const all = INCIDENT_TYPES.map((t) => t.id)
  if (!v) {
    if (required) throw fail(400, 'invalid_param', 'types is required')
    return all.filter((t) => t !== 'fire_activity')
  }
  const list = v.split(',')
  const bad = list.find((t) => !all.includes(t as IncidentTypeId))
  if (bad) throw fail(400, 'invalid_param', `unknown incident type "${bad}"`)
  if (list.includes('fire_activity') && list.length > 1) throw fail(400, 'invalid_param', 'fire_activity must be requested alone')
  return list as IncidentTypeId[]
}
const year = (v: string | undefined, d: number) => {
  const n = Number(v)
  return v !== undefined && Number.isFinite(n) ? Math.max(MIN_YEAR, Math.min(MAX_YEAR, Math.round(n))) : d
}
function parseBbox(v?: string): BBox | null {
  if (!v) return null
  const p = v.split(',').map(Number)
  if (p.length !== 4 || !p.every(Number.isFinite)) throw fail(400, 'invalid_param', 'bbox must be minLon,minLat,maxLon,maxLat')
  return p as BBox
}
function inBbox(r: Row, [w, s, e, n]: BBox) {
  if (r.lat < s || r.lat > n) return false
  return w <= e ? r.lon >= w && r.lon <= e : r.lon >= w || r.lon <= e // antimeridian
}

function filter(params: Record<string, string>, typesRequired: boolean) {
  const types = parseTypes(params.types, typesRequired)
  const set = new Set(types)
  const from = year(params.from, MIN_YEAR)
  const to = year(params.to, MAX_YEAR)
  const bbox = parseBbox(params.bbox)
  const iso3 = params.iso3 || null
  const rows = ALL.filter(
    (r) => set.has(r.type) && r.year >= from && r.year <= to && (!iso3 || r.iso3 === iso3) && (!bbox || inBbox(r, bbox)),
  )
  return { rows, types, from, to }
}

const unitFor = (types: IncidentTypeId[]) => (types.includes('fire_activity') ? 'detections' : 'incidents') as 'incidents' | 'detections'

function niceNumber(n: number) {
  if (n <= 10) return Math.max(1, Math.round(n))
  const mag = 10 ** Math.floor(Math.log10(n))
  const f = n / mag
  return (f < 1.5 ? 1 : f < 2.25 ? 2 : f < 3.5 ? 2.5 : f < 7.5 ? 5 : 10) * mag
}

/** Quantile breaks over non-zero counts, at most 6 edges: [0, …, max]. */
function computeBreaks(values: number[]): number[] {
  const v = values.filter((x) => x > 0).sort((a, b) => a - b)
  if (v.length === 0) return [0, 1]
  const max = v[v.length - 1]
  const edges = [0]
  for (let k = 1; k < 5; k++) {
    const q = niceNumber(v[Math.floor((k / 5) * (v.length - 1))])
    if (q > edges[edges.length - 1] && q < max) edges.push(q)
  }
  edges.push(max)
  return edges
}

/** 0–3 like the backend: roughly minor / moderate / major. */
function severity(r: Row): number {
  const m = r.magnitude
  if (m === null) return r.curated ? 3 : 1
  switch (r.type) {
    case 'earthquake': return m >= 7 ? 3 : m >= 6 ? 2 : 1
    case 'volcano': return m >= 4 ? 3 : m >= 2 ? 2 : 1
    case 'flood': return Math.min(3, m)
    case 'storm': return m >= 5 ? 3 : m >= 3 ? 2 : 1
    case 'wildfire': return m >= 1000 ? 3 : m >= 100 ? 2 : 1
    case 'landslide': return m >= 100 ? 3 : m >= 10 ? 2 : m > 0 ? 1 : 0
    default: return 1
  }
}

const MAG_UNIT: Partial<Record<IncidentTypeId, string>> = {
  earthquake: 'Mw', volcano: 'VEI', flood: 'severity class', storm: 'category', wildfire: 'km² burned',
}

function titleFor(r: Row, country: string | null): string {
  if (r.curated) return r.curated.title
  const where = country ?? 'at sea'
  const m = r.magnitude
  switch (r.type) {
    case 'earthquake': return `M${m?.toFixed(1)} earthquake, ${where}`
    case 'volcano': return `Eruption, VEI ${m}, ${where}`
    case 'flood': return `${m === 3 ? 'Major flood' : m === 2 ? 'Flood' : 'Minor flood'}, ${where}`
    case 'storm': return country ? `Category ${m} storm landfall, ${where}` : `Category ${m} storm at sea`
    case 'wildfire': return `Wildfire, ${where}`
    case 'landslide': return `Landslide, ${where}`
    default: return `Incident, ${where}`
  }
}

const startTime = (date: string) => `${date}T00:00:00Z`

async function summary(r: Row): Promise<IncidentSummary> {
  const countries = await loadCountries()
  const name = r.iso3 ? (countries[r.iso3]?.name ?? r.iso3) : null
  return {
    id: r.id, type: r.type, title: titleFor(r, name), startTime: startTime(r.date), lon: r.lon, lat: r.lat,
    magnitude: r.type === 'landslide' ? undefined : (r.magnitude ?? undefined),
    severity: severity(r), primaryIso3: r.iso3 ?? undefined,
  }
}

const fold = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()

// --- routes -------------------------------------------------------------------------
type Handler = (params: Record<string, string>, match: RegExpMatchArray, signal?: AbortSignal) => unknown

const routes: [RegExp, Handler][] = [
  [/^\/meta$/, (): Meta => ({
    datasetVersion: DATASET_VERSION, updatedAt: '2026-11-15T12:00:00Z', minYear: MIN_YEAR, maxYear: MAX_YEAR,
    features: { chat: false, grid: true, sar: true, stories: true }, coverage: COVERAGE,
  })],

  [/^\/incident-types$/, (): IncidentTypesResponse => ({ types: INCIDENT_TYPES })],

  [/^\/stats\/choropleth$/, (params): Choropleth => {
    const { rows, types, from, to } = filter(params, true)
    const counts: Record<string, number> = {}
    let ocean = 0
    for (const r of rows) {
      if (r.iso3) counts[r.iso3] = (counts[r.iso3] ?? 0) + 1
      else ocean++
    }
    const values = Object.values(counts)
    return {
      types, from, to, level: 'country', unit: unitFor(types), counts, ocean, total: rows.length,
      max: Math.max(0, ...values), breaks: computeBreaks(values),
      coverage: COVERAGE.filter((c) => types.includes(c.type) && c.fromYear > from).map((c) => ({ ...c, note: `${from}–${c.fromYear - 1} not covered` })),
      datasetVersion: DATASET_VERSION,
    }
  }],

  [/^\/stats\/timeseries$/, (params): Timeseries => {
    const { rows, types, from, to } = filter(params, true)
    const points = Array.from({ length: to - from + 1 }, (_, k) => ({ year: from + k, n: 0 }))
    for (const r of rows) points[r.year - from].n++
    return { types, iso3: params.iso3 ?? '', unit: unitFor(types), points, datasetVersion: DATASET_VERSION }
  }],

  [/^\/stats\/grid$/, (params): Grid => {
    if (!params.bbox) throw fail(400, 'invalid_param', 'bbox is required')
    const cellDeg = [0.25, 0.5, 1, 2].includes(Number(params.cell)) ? Number(params.cell) : 1
    const { rows, types } = filter(params, true)
    const bins = new Map<string, { lat: number; lon: number; n: number }>()
    for (const r of rows) {
      const gx = Math.floor(r.lon / cellDeg)
      const gy = Math.floor(r.lat / cellDeg)
      const key = `${gx}:${gy}`
      const b = bins.get(key) ?? { lat: (gy + 0.5) * cellDeg, lon: (gx + 0.5) * cellDeg, n: 0 }
      b.n++
      bins.set(key, b)
    }
    const cells = [...bins.values()]
    return { cellDeg, unit: unitFor(types), max: Math.max(0, ...cells.map((c) => c.n)), cells }
  }],

  [/^\/incidents$/, async (params): Promise<IncidentPage> => {
    const limit = Math.min(5000, Math.max(1, Number(params.limit) || 500))
    const { rows } = filter(params, false)
    const bySeverity = params.sort !== 'time'
    const sorted = [...rows].sort((a, b) =>
      bySeverity ? severity(b) - severity(a) || b.date.localeCompare(a.date) : b.date.localeCompare(a.date),
    )
    return { items: await Promise.all(sorted.slice(0, limit).map(summary)), nextCursor: '', limit }
  }],

  [/^\/incidents\/(\d+)$/, async (_p, m): Promise<Incident> => {
    const r = BY_ID.get(Number(m[1]))
    if (!r) throw fail(404, 'not_found', 'resource not found')
    const s = await summary(r)
    const source = SOURCE_BY_TYPE[r.type] ?? 'eonet'
    return {
      ...s,
      source, sourceId: `mock-${r.id}`,
      name: r.curated?.title,
      description: r.curated?.description ?? 'Sample record generated for development. The backend will return the real catalog entry here.',
      endTime: r.curated?.endDate ? startTime(r.curated.endDate) : undefined,
      timePrecision: 'day', year: r.year,
      magnitudeUnit: MAG_UNIT[r.type],
      deaths: r.type === 'landslide' ? (r.magnitude ?? undefined) : undefined,
      countries: r.iso3 ? [r.iso3] : [],
      url: SOURCE_URL[source],
      sarAvailability: sarAvailability(r.date),
    }
  }],

  [/^\/incidents\/(\d+)\/sar$/, async (params, m, signal) => {
    const r = BY_ID.get(Number(m[1]))
    if (!r) throw fail(404, 'not_found', 'resource not found')
    const [lon, lat] = r.curated?.sarAt ?? [r.lon, r.lat]
    const windowDays = Math.min(180, Math.max(1, Number(params.windowDays) || 45))
    return searchSar(r.id, lon, lat, startTime(r.date), windowDays, signal)
  }],

  [/^\/search$/, async (params): Promise<SearchResponse> => {
    const q = (params.q ?? '').trim()
    if (q.length < 2) throw fail(400, 'invalid_param', 'q must be at least 2 characters')
    const limit = Math.min(50, Number(params.limit) || 20)
    const tokens = fold(q).split(/\s+/)
    const countries = await loadCountries()
    const scored: { r: Row; score: number }[] = []
    for (const r of ALL) {
      const cname = r.iso3 ? (countries[r.iso3]?.name ?? r.iso3) : ''
      const title = titleFor(r, cname || null)
      const hay = fold(`${title} ${cname} ${r.type} ${r.year}`)
      if (!tokens.every((t) => hay.includes(t))) continue
      const score = (r.curated ? 0.6 : 0.2) + (fold(title).startsWith(fold(q)) ? 0.3 : 0) + severity(r) / 30
      scored.push({ r, score: Math.round(score * 100) / 100 })
    }
    scored.sort((a, b) => b.score - a.score)
    const items = await Promise.all(scored.slice(0, limit).map(async ({ r, score }) => ({ ...(await summary(r)), score })))
    return { q, items }
  }],

  [/^\/sar\/coverage-timeline$/, (): SARCoverageTimeline => {
    const byYear = []
    for (let y = 1978; y <= MAX_YEAR; y++) {
      byYear.push({
        year: y,
        missions: MISSIONS.filter((m) => Number(m.from.slice(0, 4)) <= y && (!m.to || Number(m.to.slice(0, 4)) >= y)).map((m) => m.id),
      })
    }
    return { missions: MISSIONS, byYear }
  }],

  [/^\/stories$/, (): StoriesResponse => ({
    stories: STORIES.map(({ id, title, summary, type, place, eventDate, platform, thumbnail, incidentId, order }) => ({
      id, title, summary, type, place, eventDate, platform, thumbnail, incidentId, order,
    })),
  })],

  [/^\/stories\/([a-z0-9-]+)$/, (_p, m) => {
    const s = STORIES.find((x) => x.id === m[1])
    if (!s) throw fail(404, 'not_found', 'resource not found')
    return s
  }],
]

function delay(ms: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const t = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => {
      clearTimeout(t)
      reject(new DOMException('Aborted', 'AbortError'))
    }, { once: true })
  })
}

export async function mockGet<T>(path: string, params: Record<string, string>, signal?: AbortSignal): Promise<T> {
  for (const [re, handler] of routes) {
    const m = path.match(re)
    if (!m) continue
    await delay(60 + Math.random() * 120, signal)
    return (await handler(params, m, signal)) as T
  }
  throw fail(404, 'not_found', `No mock for ${path}`)
}
