// The globe. Three zoom bands, as in the guide:
//   z < 3    countries colored by incident count (feature-state, no polygon reloads)
//   3 – 5.5  3D density columns from /stats/grid
//   ≥ 5.5    individual incidents from /incidents (clustered)
// Plus: selected-country outline, selected incident ring, SAR scene footprints,
// and an optional NISAR browse image overlay.
import * as maplibregl from 'maplibre-gl'
import type { GeoJSONSource, ImageSource, MapGeoJSONFeature } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useActiveTypes, useChoropleth, useGrid, useIncident, useIncidentSar, useIncidents, useIncidentTypes, useMeta } from '@/api/hooks'
import { isGeocodedBrowse, type BBox } from '@/api/types'
import { countFillExpression } from '@/lib/buckets'
import { loadCountries } from '@/lib/countries'
import { COUNT_RAMP, theme, typeColor, typeRamp } from '@/lib/theme'
import { useAppStore } from '@/store/app-store'
import { baseStyle, FIRST_LABEL_LAYER } from './style'

const GRID_MIN_ZOOM = 3
const POINTS_MIN_ZOOM = 5.5
/** Keep the globe centered in the space between the header and the timeline. */
export function basePadding(panelOpen = false) {
  const mobile = window.innerWidth < 768
  return {
    top: mobile ? 130 : 120,
    bottom: mobile ? 120 : 250,
    left: mobile ? 8 : 24,
    right: panelOpen && !mobile ? 440 : mobile ? 8 : 24,
  }
}

interface View {
  zoom: number
  bbox: BBox
}

function normalizeBounds(map: maplibregl.Map): BBox {
  const b = map.getBounds()
  let w = b.getWest()
  let e = b.getEast()
  const s = Math.max(-90, b.getSouth())
  const n = Math.min(90, b.getNorth())
  if (e - w >= 360) return [-180, s, 180, n]
  const wrap = (x: number) => ((((x + 180) % 360) + 360) % 360) - 180
  w = wrap(w)
  e = wrap(e)
  return [w, s, e, n]
}

function emptyFC(): GeoJSON.FeatureCollection {
  return { type: 'FeatureCollection', features: [] }
}

function prefersReducedMotion() {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

export function MapView({ onMapReady }: { onMapReady?: (map: maplibregl.Map) => void }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<maplibregl.Map | null>(null)
  const [ready, setReady] = useState(false)
  const [view, setView] = useState<View | null>(null)
  /** Countries are clickable until the grid (or, without a grid, the points) take over. */
  const countryMaxZoom = useRef(GRID_MIN_ZOOM)

  const types = useActiveTypes()
  const { data: meta } = useMeta()
  const gridEnabled = meta?.features.grid !== false
  const from = useAppStore((s) => s.from)
  const to = useAppStore((s) => s.to)
  const selection = useAppStore((s) => s.selection)
  const hoveredSceneId = useAppStore((s) => s.hoveredSceneId)
  const overlaySceneId = useAppStore((s) => s.overlaySceneId)

  const { data: incidentTypes } = useIncidentTypes()
  const { data: choropleth } = useChoropleth(types, from, to)

  const zoom = view?.zoom ?? 0
  const showGrid = gridEnabled && zoom >= GRID_MIN_ZOOM && zoom < POINTS_MIN_ZOOM
  const showPoints = zoom >= POINTS_MIN_ZOOM
  const cell = zoom < 4 ? 2 : 1
  const { data: grid } = useGrid(types, from, to, view?.bbox ?? null, cell, showGrid)
  const { data: incidents } = useIncidents(types, from, to, view?.bbox ?? null, showPoints)

  const incidentId = selection?.kind === 'incident' ? selection.id : null
  const { data: incident } = useIncident(incidentId)
  const { data: sar } = useIncidentSar(incidentId)

  const ramp = useMemo(() => {
    if (types.length === 1 && incidentTypes) {
      const t = incidentTypes.find((x) => x.id === types[0])
      if (t) return typeRamp(typeColor(t))
    }
    return [...COUNT_RAMP]
  }, [types, incidentTypes])

  const typeColorExpr = useMemo(() => {
    const expr: unknown[] = ['match', ['get', 'type']]
    for (const t of incidentTypes ?? []) expr.push(t.id, typeColor(t))
    expr.push('#CCCCCC')
    return expr.length > 3 ? expr : '#CCCCCC'
  }, [incidentTypes])

  // --- create the map once ---------------------------------------------------
  useEffect(() => {
    if (!containerRef.current) return
    const map = new maplibregl.Map({
      container: containerRef.current,
      style: baseStyle(),
      center: [30, 18],
      zoom: window.innerWidth < 768 ? 1.3 : 1.85,
      minZoom: 1,
      maxZoom: 14,
      attributionControl: { compact: true },
      canvasContextAttributes: { antialias: true },
    })
    mapRef.current = map
    map.addControl(new maplibregl.NavigationControl({ visualizePitch: true }), 'top-right')
    map.setPadding(basePadding())
    map.on('error', (e) => console.error('[map]', e.error?.message ?? e))
    if (import.meta.env.DEV) (window as unknown as { __map: maplibregl.Map }).__map = map

    map.on('load', async () => {
      const countriesUrl = `${import.meta.env.BASE_URL}data/countries.geojson`
      map.addSource('countries', { type: 'geojson', data: countriesUrl, promoteId: 'ADM0_A3' })
      map.addSource('grid', { type: 'geojson', data: emptyFC() })
      map.addSource('incidents', {
        type: 'geojson', data: emptyFC(), cluster: true, clusterRadius: 42, clusterMaxZoom: 9,
      })
      map.addSource('selected-incident', { type: 'geojson', data: emptyFC() })
      map.addSource('incident-track', { type: 'geojson', data: emptyFC() })
      map.addSource('sar-footprints', { type: 'geojson', data: emptyFC(), promoteId: 'id' })

      const before = FIRST_LABEL_LAYER
      map.addLayer({
        id: 'country-fill', type: 'fill', source: 'countries',
        paint: {
          'fill-color': theme.land,
          'fill-opacity': ['interpolate', ['linear'], ['zoom'], 2.5, 0.92, 4, 0.45, 6, 0.08],
        },
      }, before)
      map.addLayer({
        id: 'country-line', type: 'line', source: 'countries',
        paint: { 'line-color': theme.line, 'line-width': ['interpolate', ['linear'], ['zoom'], 1, 0.3, 6, 1] },
      }, before)
      map.addLayer({
        id: 'country-hover', type: 'line', source: 'countries',
        paint: {
          'line-color': theme.select,
          'line-width': ['case', ['boolean', ['feature-state', 'selected'], false], 2, 1.2],
          'line-opacity': [
            'case',
            ['boolean', ['feature-state', 'selected'], false], 1,
            ['boolean', ['feature-state', 'hover'], false], 0.7,
            0,
          ],
        },
      }, before)
      map.addLayer({
        id: 'grid-columns', type: 'fill-extrusion', source: 'grid',
        minzoom: GRID_MIN_ZOOM - 0.3, maxzoom: POINTS_MIN_ZOOM,
        paint: {
          'fill-extrusion-color': ['get', 'color'],
          'fill-extrusion-height': ['get', 'height'],
          'fill-extrusion-opacity': 0.9,
        },
      }, before)
      map.addLayer({
        id: 'sar-footprint-fill', type: 'fill', source: 'sar-footprints',
        paint: {
          'fill-color': theme.accent,
          'fill-opacity': ['case', ['boolean', ['feature-state', 'hover'], false], 0.18, 0],
        },
      }, before)
      map.addLayer({
        id: 'sar-footprint-line', type: 'line', source: 'sar-footprints',
        paint: {
          'line-color': theme.accent,
          'line-width': ['case', ['boolean', ['feature-state', 'hover'], false], 2, 0.8],
          'line-opacity': ['case', ['boolean', ['feature-state', 'hover'], false], 1, 0.35],
          'line-dasharray': [2, 1.5],
        },
      }, before)
      map.addLayer({
        id: 'incident-clusters', type: 'circle', source: 'incidents', filter: ['has', 'point_count'],
        minzoom: POINTS_MIN_ZOOM - 0.3,
        paint: {
          'circle-color': '#2B4466',
          'circle-stroke-color': theme.accent,
          'circle-stroke-width': 1,
          'circle-radius': ['interpolate', ['linear'], ['get', 'point_count'], 2, 12, 50, 20, 500, 30],
          'circle-opacity': 0.92,
        },
      })
      map.addLayer({
        id: 'incident-cluster-count', type: 'symbol', source: 'incidents', filter: ['has', 'point_count'],
        minzoom: POINTS_MIN_ZOOM - 0.3,
        layout: { 'text-field': ['get', 'point_count_abbreviated'], 'text-font': ['Noto Sans Regular'], 'text-size': 12 },
        paint: { 'text-color': theme.text },
      })
      map.addLayer({
        id: 'incident-points', type: 'circle', source: 'incidents', filter: ['!', ['has', 'point_count']],
        minzoom: POINTS_MIN_ZOOM - 0.3,
        paint: {
          'circle-color': '#CCCCCC',
          'circle-radius': ['interpolate', ['linear'], ['zoom'], 5, 4, 10, 7],
          'circle-stroke-color': theme.space,
          'circle-stroke-width': 1.2,
        },
      })
      map.addLayer({
        id: 'incident-track', type: 'line', source: 'incident-track',
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': theme.select, 'line-width': 2, 'line-opacity': 0.8, 'line-dasharray': [1, 1.5] },
      })
      map.addLayer({
        id: 'selected-incident-ring', type: 'circle', source: 'selected-incident',
        paint: {
          'circle-radius': 14,
          'circle-color': 'transparent',
          'circle-stroke-color': theme.select,
          'circle-stroke-width': 2,
        },
      })

      // --- interaction ---
      let hoverId: string | null = null
      const setHover = (id: string | null) => {
        if (hoverId === id) return
        if (hoverId) map.setFeatureState({ source: 'countries', id: hoverId }, { hover: false })
        hoverId = id
        if (id) map.setFeatureState({ source: 'countries', id }, { hover: true })
      }
      map.on('mousemove', 'country-fill', (e: maplibregl.MapLayerMouseEvent) => {
        if (map.getZoom() >= countryMaxZoom.current) return setHover(null)
        setHover((e.features?.[0]?.id as string) ?? null)
        map.getCanvas().style.cursor = 'pointer'
      })
      map.on('mouseleave', 'country-fill', () => {
        setHover(null)
        map.getCanvas().style.cursor = ''
      })
      for (const layer of ['incident-points', 'incident-clusters', 'grid-columns']) {
        map.on('mouseenter', layer, () => (map.getCanvas().style.cursor = 'pointer'))
        map.on('mouseleave', layer, () => (map.getCanvas().style.cursor = ''))
      }

      map.on('click', (e: maplibregl.MapMouseEvent) => {
        const hits = map.queryRenderedFeatures(e.point, {
          layers: ['incident-points', 'incident-clusters', 'grid-columns', 'country-fill'],
        }) as MapGeoJSONFeature[]
        const top = hits[0]
        if (!top) return
        if (top.layer.id === 'incident-points') {
          useAppStore.getState().select({ kind: 'incident', id: String(top.properties.id) })
        } else if (top.layer.id === 'incident-clusters') {
          const src = map.getSource('incidents') as GeoJSONSource
          src.getClusterExpansionZoom(top.properties.cluster_id).then((z) =>
            map.easeTo({ center: (top.geometry as GeoJSON.Point).coordinates as [number, number], zoom: z + 0.2 }),
          )
        } else if (top.layer.id === 'grid-columns') {
          map.flyTo({ center: [top.properties.lon, top.properties.lat], zoom: POINTS_MIN_ZOOM + 0.8, pitch: map.getPitch() })
        } else if (top.layer.id === 'country-fill' && map.getZoom() < countryMaxZoom.current) {
          useAppStore.getState().select({ kind: 'country', id: String(top.id) })
        }
      })

      const updateView = () => {
        setView({ zoom: map.getZoom(), bbox: normalizeBounds(map) })
        useAppStore.getState().setMapZoom(map.getZoom())
      }
      map.on('moveend', updateView)
      updateView()

      // One orchestrated moment: the globe turns slowly until someone touches it.
      if (!prefersReducedMotion() && !new URLSearchParams(window.location.search).has('incident')) {
        let spinning = true
        const stop = () => { spinning = false }
        map.once('mousedown', stop)
        map.once('touchstart', stop)
        map.once('wheel', stop)
        const spin = () => {
          if (!spinning || map.getZoom() > 2.5) return
          const c = map.getCenter()
          map.easeTo({ center: [c.lng - 6, c.lat], duration: 1000, easing: (t: number) => t })
        }
        map.on('moveend', () => spinning && spin())
        useAppStore.subscribe((s, prev) => {
          if (s.types !== prev.types || s.from !== prev.from || s.selection !== prev.selection || s.storyId !== prev.storyId) stop()
        })
        spin()
      }

      setReady(true)
      onMapReady?.(map)
    })

    return () => {
      map.remove()
      mapRef.current = null
      setReady(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // --- choropleth: counts → feature-state, breaks → fill expression ---------
  const prevKeys = useRef<Set<string>>(new Set())
  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map || !choropleth) return
    const apply = () => {
      const next = new Set(Object.keys(choropleth.counts))
      for (const k of prevKeys.current) if (!next.has(k)) map.setFeatureState({ source: 'countries', id: k }, { count: 0 })
      for (const [k, v] of Object.entries(choropleth.counts)) map.setFeatureState({ source: 'countries', id: k }, { count: v })
      prevKeys.current = next
      map.setPaintProperty('country-fill', 'fill-color', countFillExpression(choropleth.breaks, ramp, '#22344D') as never)
    }
    // Feature-state can be set before the polygons finish loading; it applies when they arrive.
    apply()
  }, [ready, choropleth, ramp])

  // Without a grid endpoint, keep the country colors visible until points take over.
  useEffect(() => {
    countryMaxZoom.current = gridEnabled ? GRID_MIN_ZOOM : POINTS_MIN_ZOOM
    const map = mapRef.current
    if (!ready || !map) return
    map.setPaintProperty(
      'country-fill',
      'fill-opacity',
      (gridEnabled
        ? ['interpolate', ['linear'], ['zoom'], 2.5, 0.92, 4, 0.45, 6, 0.08]
        : ['interpolate', ['linear'], ['zoom'], 4.5, 0.92, 6, 0.15]) as never,
    )
  }, [ready, gridEnabled])

  // --- density columns ---------------------------------------------------------
  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    const src = map.getSource('grid') as GeoJSONSource | undefined
    if (!src) return
    if (!grid || !showGrid) {
      src.setData(emptyFC())
      return
    }
    const half = (grid.cellDeg / 2) * 0.86
    const max = Math.max(1, grid.max)
    src.setData({
      type: 'FeatureCollection',
      features: grid.cells.map((c) => {
        const t = Math.sqrt(c.n / max)
        const color = ramp[Math.min(ramp.length - 1, Math.floor(t * ramp.length))]
        return {
          type: 'Feature',
          properties: { count: c.n, lon: c.lon, lat: c.lat, color, height: 4000 + t * 450_000 },
          geometry: {
            type: 'Polygon',
            coordinates: [[
              [c.lon - half, c.lat - half], [c.lon + half, c.lat - half],
              [c.lon + half, c.lat + half], [c.lon - half, c.lat + half], [c.lon - half, c.lat - half],
            ]],
          },
        }
      }),
    })
  }, [ready, grid, showGrid, ramp])

  // --- incident points ------------------------------------------------------------
  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    const src = map.getSource('incidents') as GeoJSONSource | undefined
    if (!src) return
    if (!incidents || !showPoints) {
      src.setData(emptyFC())
      return
    }
    src.setData({
      type: 'FeatureCollection',
      features: incidents.items.map((i) => ({
        type: 'Feature',
        properties: { id: i.id, type: i.type, title: i.title, severity: i.severity },
        geometry: { type: 'Point', coordinates: [i.lon, i.lat] },
      })),
    })
  }, [ready, incidents, showPoints])

  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    map.setPaintProperty('incident-points', 'circle-color', typeColorExpr as never)
  }, [ready, typeColorExpr])

  // --- selection: outline + camera -------------------------------------------------
  const prevSelected = useRef<string | null>(null)
  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    const countryId = selection?.kind === 'country' ? selection.id : null
    if (prevSelected.current && prevSelected.current !== countryId)
      map.setFeatureState({ source: 'countries', id: prevSelected.current }, { selected: false })
    if (countryId) {
      map.setFeatureState({ source: 'countries', id: countryId }, { selected: true })
      loadCountries().then((c) => {
        const m = c[countryId]
        if (m) map.fitBounds(m.mainBbox, { padding: basePadding(true), maxZoom: 4.6, duration: prefersReducedMotion() ? 0 : 1600 })
      })
    }
    prevSelected.current = countryId
    if (!selection) map.easeTo({ padding: basePadding(false), duration: prefersReducedMotion() ? 0 : 600 })
  }, [ready, selection])

  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    const src = map.getSource('selected-incident') as GeoJSONSource
    const track = map.getSource('incident-track') as GeoJSONSource
    if (!incident || String(incident.id) !== incidentId) {
      src.setData(emptyFC())
      track.setData(emptyFC())
      return
    }
    src.setData({
      type: 'FeatureCollection',
      features: [{ type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [incident.lon, incident.lat] } }],
    })
    track.setData(
      incident.track
        ? { type: 'FeatureCollection', features: [{ type: 'Feature', properties: {}, geometry: incident.track }] }
        : emptyFC(),
    )
    map.flyTo({
      center: [incident.lon, incident.lat],
      zoom: Math.max(map.getZoom(), 7),
      padding: basePadding(true),
      duration: prefersReducedMotion() ? 0 : 1800,
    })
  }, [ready, incident, incidentId])

  // --- SAR footprints + hover ------------------------------------------------------
  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    const src = map.getSource('sar-footprints') as GeoJSONSource
    const scenes = sar && String(sar.incidentId) === incidentId ? [...sar.before, ...sar.after].filter((s) => s.footprint) : []
    src.setData({
      type: 'FeatureCollection',
      features: scenes.map((s) => ({ type: 'Feature', properties: { id: s.granuleId }, geometry: s.footprint! })),
    })
  }, [ready, sar, incidentId])

  const prevHover = useRef<string | null>(null)
  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    if (prevHover.current) map.setFeatureState({ source: 'sar-footprints', id: prevHover.current }, { hover: false })
    if (hoveredSceneId) map.setFeatureState({ source: 'sar-footprints', id: hoveredSceneId }, { hover: true })
    prevHover.current = hoveredSceneId
  }, [ready, hoveredSceneId, sar])

  // --- NISAR browse overlay (geocoded PNG, CORS-enabled) ----------------------------
  useEffect(() => {
    const map = mapRef.current
    if (!ready || !map) return
    const scene = sar && [...sar.before, ...sar.after].find((s) => s.granuleId === overlaySceneId && isGeocodedBrowse(s) && s.footprint)
    if (map.getLayer('sar-overlay')) map.removeLayer('sar-overlay')
    if (map.getSource('sar-overlay')) map.removeSource('sar-overlay')
    if (!scene) return
    const ring = scene.footprint!.coordinates[0]
    const lons = ring.map((p) => p[0])
    const lats = ring.map((p) => p[1])
    const [w, e, s, n] = [Math.min(...lons), Math.max(...lons), Math.min(...lats), Math.max(...lats)]
    map.addSource('sar-overlay', {
      type: 'image',
      url: scene.browseUrl!,
      coordinates: [[w, n], [e, n], [e, s], [w, s]],
    })
    map.addLayer({ id: 'sar-overlay', type: 'raster', source: 'sar-overlay', paint: { 'raster-opacity': 0.9 } }, 'sar-footprint-fill')
    map.fitBounds([w, s, e, n], { padding: basePadding(true), duration: prefersReducedMotion() ? 0 : 1200 })
    return () => {
      const src = map.getSource('sar-overlay') as ImageSource | undefined
      if (src && map.getLayer('sar-overlay')) map.removeLayer('sar-overlay')
      if (src) map.removeSource('sar-overlay')
    }
  }, [ready, overlaySceneId, sar])

  // Wrapper keeps the absolute positioning: maplibre-gl.css (unlayered) forces the
  // container itself to position: relative, which beats Tailwind's layered utilities.
  return (
    <div className="absolute inset-0">
      <div ref={containerRef} className="h-full w-full" aria-label="Interactive globe of natural hazard incidents" role="region" />
    </div>
  )
}
