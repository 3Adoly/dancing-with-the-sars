// One small store for what the user is looking at. Mirrored to the URL
// (?types=&from=&to=&country=&incident=&story=) so any view can be shared.
import { create } from 'zustand'
import type { IncidentTypeId, Iso3, TypeInfo } from '@/api/types'

export type Selection = { kind: 'country'; id: Iso3 } | { kind: 'incident'; id: string } | null

const THIS_YEAR = new Date().getUTCFullYear()

interface AppState {
  /** null = default set (all incident types); see useActiveTypes() */
  types: IncidentTypeId[] | null
  from: number
  to: number
  selection: Selection
  storyId: string | null
  playing: boolean
  /** Scene highlighted in the SAR list (footprint drawn on the map). */
  hoveredSceneId: string | null
  /** Scene whose geocoded browse image is drawn on the map. */
  overlaySceneId: string | null
  /** Current map zoom (set by the map on moveend). */
  mapZoom: number

  toggleType: (t: IncidentTypeId, all: TypeInfo[], current: IncidentTypeId[]) => void
  setTypes: (t: IncidentTypeId[] | null) => void
  setRange: (from: number, to: number) => void
  select: (s: Selection) => void
  openStory: (id: string | null) => void
  setPlaying: (p: boolean) => void
  setHoveredScene: (id: string | null) => void
  setOverlayScene: (id: string | null) => void
  setMapZoom: (z: number) => void
}

function readUrl() {
  const q = new URLSearchParams(window.location.search)
  const types = q.get('types')?.split(',').filter(Boolean) as IncidentTypeId[] | undefined
  const year = (v: string | null, d: number) => {
    const n = Number(v)
    return v && Number.isFinite(n) ? Math.round(n) : d
  }
  let from = year(q.get('from'), THIS_YEAR - 50)
  let to = year(q.get('to'), THIS_YEAR)
  if (from > to) [from, to] = [to, from]
  const country = q.get('country')
  const incident = q.get('incident')
  const selection: Selection = incident ? { kind: 'incident', id: incident } : country ? { kind: 'country', id: country } : null
  return { types: types && types.length ? types : null, from, to, selection, storyId: q.get('story') }
}

export const useAppStore = create<AppState>((set) => ({
  ...readUrl(),
  playing: false,
  hoveredSceneId: null,
  overlaySceneId: null,
  mapZoom: 0,

  toggleType: (t, all, current) =>
    set(() => {
      const info = all.find((x) => x.id === t)
      // Detections (fire_activity) are a different unit: the API requires them alone.
      if (info?.unit === 'detections') return { types: current.includes(t) ? null : [t] }
      const base = current.filter((x) => all.find((a) => a.id === x)?.unit === 'incidents')
      const next = base.includes(t) ? base.filter((x) => x !== t) : [...base, t]
      return { types: next.length ? all.filter((a) => next.includes(a.id)).map((a) => a.id) : base }
    }),
  setTypes: (types) => set({ types: types && types.length ? types : null }),
  setRange: (from, to) => set({ from: Math.min(from, to), to: Math.max(from, to) }),
  select: (selection) => set({ selection, hoveredSceneId: null, overlaySceneId: null }),
  openStory: (storyId) => set({ storyId, playing: false }),
  setPlaying: (playing) => set({ playing }),
  setHoveredScene: (hoveredSceneId) => set({ hoveredSceneId }),
  setOverlayScene: (overlaySceneId) => set({ overlaySceneId }),
  setMapZoom: (mapZoom) => set({ mapZoom }),
}))

// --- URL mirror -------------------------------------------------------------
let urlTimer: ReturnType<typeof setTimeout> | undefined
useAppStore.subscribe((s) => {
  clearTimeout(urlTimer)
  urlTimer = setTimeout(() => {
    const q = new URLSearchParams()
    if (s.types) q.set('types', s.types.join(','))
    q.set('from', String(s.from))
    q.set('to', String(s.to))
    if (s.selection?.kind === 'country') q.set('country', s.selection.id)
    if (s.selection?.kind === 'incident') q.set('incident', s.selection.id)
    if (s.storyId) q.set('story', s.storyId)
    const next = `${window.location.pathname}?${q.toString().replace(/%2C/g, ',')}`
    if (next !== window.location.pathname + window.location.search) window.history.replaceState(null, '', next)
  }, 150)
})
