import { keepPreviousData, useQueries, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useAppStore } from '@/store/app-store'
import { apiGet } from './client'
import type {
  BBox,
  Choropleth,
  Grid,
  Incident,
  IncidentPage,
  IncidentTypeId,
  IncidentTypesResponse,
  Meta,
  SARCoverageTimeline,
  SARResult,
  SearchResponse,
  StoriesResponse,
  Story,
  Timeseries,
  TypeInfo,
} from './types'

const STATIC = { staleTime: Infinity, gcTime: Infinity } as const
const csv = (types: IncidentTypeId[]) => [...types].sort().join(',')
const bboxParam = (b: BBox) => b.map((v) => v.toFixed(2)).join(',')

// --- reference data ---------------------------------------------------------------

export const useMeta = () =>
  useQuery({ queryKey: ['meta'], queryFn: ({ signal }) => apiGet<Meta>('/meta', {}, signal), ...STATIC })

export const useIncidentTypes = () =>
  useQuery({
    queryKey: ['incident-types'],
    queryFn: ({ signal }) => apiGet<IncidentTypesResponse>('/incident-types', {}, signal),
    select: (d) => d.types,
    ...STATIC,
  })

export const useCoverageTimeline = () =>
  useQuery({
    queryKey: ['coverage-timeline'],
    queryFn: ({ signal }) => apiGet<SARCoverageTimeline>('/sar/coverage-timeline', {}, signal),
    ...STATIC,
  })

/** Year bounds from /meta, with a sensible fallback while it loads. */
export function useYearBounds() {
  const { data } = useMeta()
  const now = new Date().getUTCFullYear()
  return { min: data?.minYear ?? 1900, max: data?.maxYear ?? now }
}

/** Types shown by default: every incident type (fire detections are a different unit and opt-in). */
export function defaultTypes(all: TypeInfo[]): IncidentTypeId[] {
  return all.filter((t) => t.unit === 'incidents').map((t) => t.id)
}

/** The type filter in effect: the user's choice, or the default set once types are known. */
export function useActiveTypes(): IncidentTypeId[] {
  const chosen = useAppStore((s) => s.types)
  const { data: all } = useIncidentTypes()
  return useMemo(() => {
    if (!all) return chosen ?? []
    const valid = (chosen ?? []).filter((t) => all.some((x) => x.id === t))
    return valid.length ? valid : defaultTypes(all)
  }, [chosen, all])
}

export function useTypeMap() {
  const { data } = useIncidentTypes()
  return useMemo(() => new Map((data ?? []).map((t) => [t.id, t])), [data])
}

// --- stats --------------------------------------------------------------------------

export function useChoropleth(types: IncidentTypeId[], from: number, to: number) {
  return useQuery({
    queryKey: ['choropleth', csv(types), from, to],
    queryFn: ({ signal }) => apiGet<Choropleth>('/stats/choropleth', { types: csv(types), from, to }, signal),
    placeholderData: keepPreviousData,
    enabled: types.length > 0,
  })
}

/** One choropleth per type, cached, so a country panel can show the per-type split. */
export function usePerTypeCounts(types: IncidentTypeId[], from: number, to: number, iso3: string | null) {
  const results = useQueries({
    queries: types.map((t) => ({
      queryKey: ['choropleth', t, from, to],
      queryFn: ({ signal }: { signal: AbortSignal }) => apiGet<Choropleth>('/stats/choropleth', { types: t, from, to }, signal),
      staleTime: 5 * 60_000,
    })),
  })
  return useMemo(
    () => types.map((t, i) => ({ type: t, n: iso3 ? (results[i].data?.counts[iso3] ?? 0) : (results[i].data?.total ?? 0), loading: results[i].isLoading })),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [types, iso3, ...results.map((r) => r.data)],
  )
}

export function useTimeseries(types: IncidentTypeId[], from: number, to: number, iso3: string | null) {
  return useQuery({
    queryKey: ['timeseries', csv(types), from, to, iso3 ?? ''],
    queryFn: ({ signal }) => apiGet<Timeseries>('/stats/timeseries', { types: csv(types), from, to, iso3 }, signal),
    placeholderData: keepPreviousData,
    enabled: types.length > 0,
  })
}

export function useGrid(types: IncidentTypeId[], from: number, to: number, bbox: BBox | null, cell: number, enabled: boolean) {
  return useQuery({
    queryKey: ['grid', csv(types), from, to, bbox && bboxParam(bbox), cell],
    queryFn: ({ signal }) => apiGet<Grid>('/stats/grid', { types: csv(types), from, to, bbox: bboxParam(bbox!), cell }, signal),
    placeholderData: keepPreviousData,
    enabled: enabled && bbox !== null && types.length > 0,
  })
}

// --- incidents -----------------------------------------------------------------------

export function useIncidents(types: IncidentTypeId[], from: number, to: number, bbox: BBox | null, enabled: boolean) {
  return useQuery({
    queryKey: ['incidents', csv(types), from, to, bbox && bboxParam(bbox)],
    queryFn: ({ signal }) =>
      apiGet<IncidentPage>('/incidents', { types: csv(types), from, to, bbox: bboxParam(bbox!), sort: 'severity', limit: 2000 }, signal),
    placeholderData: keepPreviousData,
    enabled: enabled && bbox !== null && types.length > 0,
  })
}

export const useIncident = (id: string | null) =>
  useQuery({
    queryKey: ['incident', id],
    queryFn: ({ signal }) => apiGet<Incident>(`/incidents/${encodeURIComponent(id!)}`, {}, signal),
    enabled: id !== null,
    staleTime: 5 * 60_000,
    retry: (count, err) => (err as { status?: number }).status !== 404 && count < 1,
  })

export const useIncidentSar = (id: string | null) =>
  useQuery({
    queryKey: ['incident-sar', id],
    queryFn: ({ signal }) => apiGet<SARResult>(`/incidents/${encodeURIComponent(id!)}/sar`, {}, signal),
    enabled: id !== null,
    staleTime: 30 * 60_000,
    retry: 1,
  })

export const useSearch = (q: string) =>
  useQuery({
    queryKey: ['search', q],
    queryFn: ({ signal }) => apiGet<SearchResponse>('/search', { q, limit: 8 }, signal),
    enabled: q.trim().length >= 2,
    placeholderData: keepPreviousData,
  })

// --- stories -------------------------------------------------------------------------

export const useStories = () =>
  useQuery({
    queryKey: ['stories'],
    queryFn: ({ signal }) => apiGet<StoriesResponse>('/stories', {}, signal),
    select: (d) => [...d.stories].sort((a, b) => a.order - b.order),
    staleTime: 10 * 60_000,
  })

export const useStory = (id: string | null) =>
  useQuery({
    queryKey: ['story', id],
    queryFn: ({ signal }) => apiGet<Story>(`/stories/${encodeURIComponent(id!)}`, {}, signal),
    enabled: id !== null,
    staleTime: 10 * 60_000,
  })
