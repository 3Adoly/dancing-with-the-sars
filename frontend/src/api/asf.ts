// Live radar scene lookup against the public ASF Search API (no login, CORS-enabled).
// The backend does this server-side (E10). The mock uses this module so that
// mock incidents still get real scenes, in exactly the E10 SARResult shape.
import type { SARResult, SARScene } from './types'

const ASF_SEARCH = 'https://api.daac.asf.alaska.edu/services/search/param'
const DAY = 86_400_000

interface MissionQuery {
  id: string
  asfPlatform: string
  /** Empty = any (Seasat has no processing level in ASF). */
  processingLevel: string
  from: string
  to: string | null
}

/** Searchable missions, same ids and dates as /sar/coverage-timeline. */
const MISSIONS: MissionQuery[] = [
  { id: 'nisar', asfPlatform: 'NISAR', processingLevel: 'GCOV', from: '2025-10-28', to: null },
  { id: 'sentinel1', asfPlatform: 'SENTINEL-1', processingLevel: 'GRD_HD', from: '2014-04-03', to: null },
  { id: 'alos', asfPlatform: 'ALOS', processingLevel: 'L1.5', from: '2006-05-16', to: '2011-04-21' },
  { id: 'seasat', asfPlatform: 'SEASAT', processingLevel: '', from: '1978-06-28', to: '1978-10-10' },
]

/** ERS-1 started continuous civilian radar imaging; before it, only Seasat's 1978 window. */
const CONTINUOUS_SAR_START = Date.parse('1991-07-17')

function missionsInWindow(t: number, windowDays: number) {
  return MISSIONS.filter((m) => {
    const from = Date.parse(m.from)
    const to = m.to ? Date.parse(m.to) : Infinity
    return t + windowDays * DAY >= from && t - windowDays * DAY <= to
  })
}

export function sarAvailability(date: string): 'searchable' | 'pre_sar_era' | 'gap' {
  const t = Date.parse(date)
  if (missionsInWindow(t, 45).length) return 'searchable'
  return t < CONTINUOUS_SAR_START ? 'pre_sar_era' : 'gap'
}

const iso = (t: number) => new Date(t).toISOString().slice(0, 19) + 'Z'

interface AsfFeature {
  geometry: { type: 'Polygon'; coordinates: number[][][] }
  properties: {
    fileID: string
    sceneName: string
    startTime: string
    platform: string
    beamModeType?: string
    processingLevel: string | null
    polarization: string | null
    flightDirection: string | null
    pathNumber?: number
    frameNumber?: number
    browse: string[] | string | null
    url: string
    bytes?: number
  }
}

async function queryAsf(params: Record<string, string>, signal?: AbortSignal): Promise<AsfFeature[]> {
  const res = await fetch(`${ASF_SEARCH}?${new URLSearchParams({ ...params, output: 'geojson' })}`, { signal })
  if (!res.ok) throw Object.assign(new Error(`ASF search failed (${res.status})`), { status: 502, code: 'upstream_error' })
  return (await res.json()).features ?? []
}

function toScene(m: MissionQuery, f: AsfFeature): SARScene {
  const p = f.properties
  const browse = (Array.isArray(p.browse) ? p.browse : [p.browse]).filter((b): b is string => Boolean(b))
  const browseUrl = m.id === 'nisar' ? (browse.find((b) => b.endsWith('_LATLON.png')) ?? browse[0]) : browse[0]
  return {
    granuleId: p.fileID ?? p.sceneName,
    mission: m.id,
    platform: p.platform,
    beamMode: p.beamModeType,
    polarization: p.polarization ?? undefined,
    flightDirection: p.flightDirection ?? undefined,
    path: p.pathNumber,
    frame: p.frameNumber,
    startTime: p.startTime,
    processingLevel: p.processingLevel ?? (m.processingLevel || 'L1'),
    browseUrl,
    downloadUrl: p.url,
    sizeMB: p.bytes ? Math.round(p.bytes / 1e5) / 10 : undefined,
    footprint: f.geometry,
  }
}

/** One scene per satellite pass (Sentinel-1 returns several overlapping frames per pass). */
function onePerPass(scenes: SARScene[]) {
  const seen = new Set<string>()
  return scenes.filter((s) => {
    const key = `${s.mission}|${s.startTime.slice(0, 10)}|${s.flightDirection}|${s.path}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

export function vertexUrl(lon: number, lat: number, t: number, windowDays: number) {
  const qs = new URLSearchParams({
    polygon: `POINT(${lon.toFixed(4)} ${lat.toFixed(4)})`,
    start: iso(t - windowDays * DAY),
    end: iso(t + windowDays * DAY),
    center: `${lon.toFixed(4)},${lat.toFixed(4)}`,
    zoom: '7',
  })
  return `https://search.asf.alaska.edu/#/?${qs}`
}

export async function searchSar(
  incidentId: number,
  lon: number,
  lat: number,
  startTime: string,
  windowDays = 45,
  signal?: AbortSignal,
): Promise<SARResult> {
  const t = Date.parse(startTime)
  const missions = missionsInWindow(t, windowDays)
  const base = {
    incidentId,
    windowDays,
    missionsInWindow: missions.map((m) => m.id),
    fetchedAt: new Date().toISOString(),
    stale: false,
    asfSearchUrl: vertexUrl(lon, lat, t, windowDays),
  }
  if (missions.length === 0) {
    return t < CONTINUOUS_SAR_START
      ? { ...base, coverage: 'pre_sar_era', before: [], after: [] }
      : { ...base, coverage: 'none', reason: 'no_searchable_mission_in_window', before: [], after: [] }
  }

  const point = `POINT(${lon} ${lat})`
  const per = await Promise.all(
    missions.map(async (m) => {
      const common: Record<string, string> = { platform: m.asfPlatform, intersectsWith: point }
      if (m.processingLevel) common.processingLevel = m.processingLevel
      const [b, a] = await Promise.all([
        queryAsf({ ...common, start: iso(t - windowDays * DAY), end: iso(t - 1000), maxResults: '20' }, signal),
        queryAsf({ ...common, start: iso(t), end: iso(t + windowDays * DAY), maxResults: '60' }, signal),
      ])
      return {
        before: onePerPass(b.map((f) => toScene(m, f))).slice(0, 5),
        after: onePerPass(a.map((f) => toScene(m, f)).sort((x, y) => x.startTime.localeCompare(y.startTime))).slice(0, 5),
      }
    }),
  )
  const before = per.flatMap((p) => p.before).sort((x, y) => y.startTime.localeCompare(x.startTime))
  const after = per.flatMap((p) => p.after).sort((x, y) => x.startTime.localeCompare(y.startTime))

  // Best pair: same mission, path and direction, closest to the event; NISAR first.
  let bestPair: SARResult['bestPair']
  outer: for (const mission of ['nisar', 'sentinel1', 'alos', 'seasat']) {
    for (const b of before.filter((s) => s.mission === mission && s.browseUrl)) {
      const a = after.find((s) => s.mission === mission && s.browseUrl && s.path === b.path && s.flightDirection === b.flightDirection)
      if (a) {
        bestPair = { before: b.granuleId, after: a.granuleId }
        break outer
      }
    }
  }

  const coverage: SARResult['coverage'] =
    before.length && after.length ? 'ok' : before.length ? 'before_only' : after.length ? 'after_only' : 'none'
  return { ...base, coverage, reason: coverage === 'none' ? 'no_scenes_found' : undefined, before, after, bestPair }
}
