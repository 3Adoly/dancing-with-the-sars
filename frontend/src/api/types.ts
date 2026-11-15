// Wire types for the team backend, copied from its OpenAPI contract
// (api/openapi.yaml, "Dancing with the SARs API" 1.0.0, task A0). Field names and
// shapes must match the spec exactly; frontend-only helpers live at the bottom.

export type IncidentTypeId =
  | 'earthquake'
  | 'volcano'
  | 'tsunami'
  | 'wildfire'
  | 'fire_activity'
  | 'flood'
  | 'landslide'
  | 'storm'
  | 'drought'
  | 'other'

/** Natural Earth ADM0_A3 code. */
export type Iso3 = string

/** [west, south, east, north] in EPSG:4326 */
export type BBox = [number, number, number, number]

export interface ApiErrorBody {
  error: { code: string; message: string; details?: Record<string, unknown> }
  requestId: string
}

// E1 GET /healthz
export interface Health {
  status: 'ok' | 'degraded'
  db: 'ok' | 'unavailable' | 'not_configured'
  cubeVersion: number
  stories: number
  uptimeSec: number
  version?: string
}

export interface Coverage {
  type: IncidentTypeId
  fromYear: number
  toYear: number
  sources: string[]
  note: string
}

// E2 GET /api/v1/meta
export interface Meta {
  datasetVersion: number
  updatedAt: string
  minYear: number
  maxYear: number
  features: { chat?: boolean; stories?: boolean; grid?: boolean; sar?: boolean }
  coverage: Coverage[]
}

// E3 GET /api/v1/incident-types → { types: TypeInfo[] }
export interface TypeInfo {
  id: IncidentTypeId
  label: string
  unit: 'incidents' | 'detections'
  color: string
  icon: string
  fromYear: number
  toYear: number
  clickable: boolean
}
export interface IncidentTypesResponse {
  types: TypeInfo[]
}

// E4 GET /api/v1/stats/choropleth?types=&from=&to=
export interface Choropleth {
  types: IncidentTypeId[]
  from: number
  to: number
  level: 'country'
  unit: 'incidents' | 'detections'
  /** ADM0_A3 → count; missing key = 0 */
  counts: Record<Iso3, number>
  ocean: number
  /** Distinct incidents */
  total: number
  max: number
  /** Ascending bucket edges, at most 6: [0, b1, …, max] */
  breaks: number[]
  coverage: Coverage[]
  datasetVersion: number
}

// E5 GET /api/v1/stats/timeseries?types=&from=&to=&iso3=
export interface Timeseries {
  types: IncidentTypeId[]
  /** '' = world */
  iso3: string
  unit: string
  points: { year: number; n: number }[]
  datasetVersion: number
}

// E6 GET /api/v1/stats/grid?types=&from=&to=&bbox=&cell=
export interface Grid {
  cellDeg: number
  unit: string
  max: number
  cells: { lat: number; lon: number; n: number }[]
}

export interface IncidentSummary {
  id: number
  type: IncidentTypeId
  title: string
  startTime: string
  lon: number
  lat: number
  magnitude?: number
  severity: number
  primaryIso3?: string
  /** Search only */
  score?: number
}

// E7 GET /api/v1/incidents
export interface IncidentPage {
  items: IncidentSummary[]
  /** '' when this is the last page */
  nextCursor: string
  limit: number
}

export type IncidentSource = 'usgs' | 'noaa_eq' | 'noaa_ts' | 'noaa_vo' | 'gvp' | 'eonet' | 'gdacs' | 'ibtracs' | 'glc' | 'dfo'

// E8 GET /api/v1/incidents/{id}
export interface Incident {
  id: number
  source: IncidentSource
  sourceId: string
  type: IncidentTypeId
  title: string
  name?: string
  description?: string
  startTime: string
  endTime?: string
  timePrecision: 'second' | 'day' | 'month' | 'year'
  year: number
  lon: number
  lat: number
  magnitude?: number
  magnitudeUnit?: string
  severity: number
  deaths?: number
  primaryIso3?: string
  countries: string[]
  url?: string
  props?: Record<string, unknown>
  /** GeoJSON LineString (storms) */
  track?: { type: 'LineString'; coordinates: [number, number][] }
  sarAvailability?: 'searchable' | 'pre_sar_era' | 'gap'
  storyId?: string
}

// E9 GET /api/v1/search?q=
export interface SearchResponse {
  q: string
  items: IncidentSummary[]
}

export interface SARScene {
  granuleId: string
  mission: string
  platform: string
  beamMode?: string
  polarization?: string
  flightDirection?: string
  path?: number
  frame?: number
  startTime: string
  processingLevel: string
  /** Public image, no login */
  browseUrl?: string
  /** Needs Earthdata Login */
  downloadUrl?: string
  sizeMB?: number
  footprint?: { type: 'Polygon'; coordinates: number[][][] }
}

// E10 GET /api/v1/incidents/{id}/sar
export interface SARResult {
  incidentId: number
  coverage: 'ok' | 'before_only' | 'after_only' | 'none' | 'pre_sar_era'
  reason?: 'no_scenes_found' | 'no_searchable_mission_in_window' | 'upstream_error'
  windowDays: number
  missionsInWindow: string[]
  before: SARScene[]
  after: SARScene[]
  bestPair?: { before?: string; after?: string }
  storyId?: string
  fetchedAt: string
  stale: boolean
  asfSearchUrl: string
}

export interface SARMission {
  id: string
  name: string
  agency: string
  band: string
  from: string
  /** Absent = still active */
  to?: string
  searchable: boolean
  note?: string
}

// E11 GET /api/v1/sar/coverage-timeline
export interface SARCoverageTimeline {
  missions: SARMission[]
  byYear: { year: number; missions: string[] }[]
}

export interface Legend {
  type: 'diverging' | 'sequential' | 'cyclic'
  min: number
  max: number
  unit: string
  lowLabel: string
  highLabel: string
  colors?: string[]
}

export type StoryLayerKind = 'before' | 'after' | 'change' | 'interferogram' | 'frame'

export interface StoryLayer {
  kind: StoryLayerKind
  url: string
  /** [west, south, east, north] */
  bounds: BBox
  date?: string
  label?: string
  legend?: Legend
  /** Overrides Story.platform (cross-sensor stories) */
  platform?: string
  /** Overrides Story.polarization */
  polarization?: string
}

// E12 GET /api/v1/stories → { stories: StorySummary[] }
export interface StorySummary {
  id: string
  title: string
  summary: string
  type: IncidentTypeId
  place: string
  eventDate: string
  platform: string
  thumbnail: string
  incidentId?: number
  order: number
}
export interface StoriesResponse {
  stories: StorySummary[]
}

// E13 GET /api/v1/stories/{id}
export interface Story {
  id: string
  title: string
  summary: string
  incidentId?: number
  incidentSource?: string
  incidentSourceId?: string
  type: IncidentTypeId
  place: string
  iso3?: string
  bbox: BBox
  center: [number, number]
  zoom: number
  eventDate: string
  beforeDate: string
  afterDate: string
  platform: string
  band: string
  polarization: string
  product: string
  thumbnail: string
  layers: StoryLayer[]
  explanation: { driver: string; polarizationNote: string; howToRead: string; caveats?: string }
  credits: string[]
  order: number
}

// --- frontend helpers (not part of the wire contract) ---------------------------

export const MISSION_BAND: Record<string, string> = {
  nisar: 'L+S',
  sentinel1: 'C',
  alos: 'L',
  seasat: 'L',
  saocom: 'L',
  rcm: 'C',
}

/** NISAR GCOV *_LATLON.png browse images are map-projected and CORS-enabled, so they can go on the map. */
export function isGeocodedBrowse(s: SARScene) {
  return Boolean(s.browseUrl && s.browseUrl.endsWith('_LATLON.png'))
}
