// Two synced MapLibre maps for a story. In "compare" mode the top map (after)
// is clipped at the divider; other modes use only the bottom map.
import * as maplibregl from 'maplibre-gl'
import { useEffect, useMemo, useRef, useState } from 'react'
import { mediaUrl } from '@/api/client'
import type { BBox, Story, StoryLayer } from '@/api/types'
import { fmtDate } from '@/lib/format'
import { baseStyle, FIRST_LABEL_LAYER } from '@/map/style'

export type StoryMode = 'compare' | 'change' | 'interferogram' | 'timeseries'

/** The API gives [w, s, e, n]; MapLibre image sources want four corners, clockwise from top-left. */
const corners = ([w, s, e, n]: BBox): [[number, number], [number, number], [number, number], [number, number]] => [
  [w, n], [e, n], [e, s], [w, s],
]

export interface IndexedLayer extends StoryLayer {
  key: string
}

export function indexLayers(story: Story): IndexedLayer[] {
  return story.layers.map((l, i) => ({ ...l, key: `story-${l.kind}-${i}` }))
}

function addImageLayers(map: maplibregl.Map, layers: IndexedLayer[]) {
  for (const l of layers) {
    if (map.getSource(l.key)) continue
    map.addSource(l.key, { type: 'image', url: mediaUrl(l.url), coordinates: corners(l.bounds) })
    map.addLayer(
      { id: l.key, type: 'raster', source: l.key, paint: { 'raster-opacity': 0, 'raster-opacity-transition': { duration: 350 }, 'raster-fade-duration': 0 } },
      FIRST_LABEL_LAYER,
    )
  }
}

function setOpacity(map: maplibregl.Map, key: string | undefined, value: number) {
  if (key && map.getLayer(key)) map.setPaintProperty(key, 'raster-opacity', value)
}

export function StoryMap({ story, mode, frameIndex }: { story: Story; mode: StoryMode; frameIndex: number }) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const aRef = useRef<HTMLDivElement>(null)
  const bRef = useRef<HTMLDivElement>(null)
  const maps = useRef<{ a: maplibregl.Map; b: maplibregl.Map } | null>(null)
  const [ready, setReady] = useState(0)
  const [pos, setPos] = useState(50)

  const layers = useMemo(() => indexLayers(story), [story])
  const before = layers.find((l) => l.kind === 'before')
  const after = layers.find((l) => l.kind === 'after')
  const change = layers.find((l) => l.kind === 'change')
  const ifg = layers.find((l) => l.kind === 'interferogram')
  const frames = useMemo(() => layers.filter((l) => l.kind === 'frame'), [layers])

  useEffect(() => {
    if (!aRef.current || !bRef.current) return
    const opts = (container: HTMLElement): maplibregl.MapOptions => ({
      container,
      style: baseStyle('mercator'),
      bounds: story.bbox,
      fitBoundsOptions: { padding: 24 },
      attributionControl: false,
      maxZoom: 15,
    })
    const a = new maplibregl.Map({ ...opts(aRef.current), attributionControl: { compact: true } })
    const b = new maplibregl.Map(opts(bRef.current))
    a.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'bottom-right')
    maps.current = { a, b }

    // Keep the two cameras locked together.
    let syncing = false
    const sync = (src: maplibregl.Map, dst: maplibregl.Map) => () => {
      if (syncing) return
      syncing = true
      dst.jumpTo({ center: src.getCenter(), zoom: src.getZoom(), bearing: src.getBearing(), pitch: src.getPitch() })
      syncing = false
    }
    a.on('move', sync(a, b))
    b.on('move', sync(b, a))

    a.on('load', () => {
      addImageLayers(a, layers.filter((l) => l !== after || !before))
      const [w, s, e, n] = story.bbox
      a.addSource('story-area', {
        type: 'geojson',
        data: { type: 'Feature', properties: {}, geometry: { type: 'Polygon', coordinates: [[[w, n], [e, n], [e, s], [w, s], [w, n]]] } },
      })
      a.addLayer({ id: 'story-area', type: 'line', source: 'story-area', paint: { 'line-color': '#F5B66B', 'line-width': 1, 'line-dasharray': [2, 2] } })
      setReady((r) => r + 1)
    })
    b.on('load', () => {
      if (after) addImageLayers(b, [after])
      setReady((r) => r + 1)
    })
    return () => {
      a.remove()
      b.remove()
      maps.current = null
      setReady(0)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [story.id])

  // Which image is visible in which map for the current mode.
  useEffect(() => {
    const m = maps.current
    if (!m || ready < 2) return
    const { a, b } = m
    for (const l of layers) setOpacity(a, l.key, 0)
    setOpacity(b, after?.key, 0)

    if (mode === 'compare') {
      setOpacity(a, before?.key, 1)
      setOpacity(b, after?.key, 1)
    } else if (mode === 'change' || mode === 'interferogram') {
      setOpacity(a, (before ?? after)?.key, 0.85)
      setOpacity(a, (mode === 'change' ? change : ifg)?.key, 1)
    } else if (mode === 'timeseries') {
      setOpacity(a, frames[Math.min(frameIndex, frames.length - 1)]?.key, 1)
    }
  }, [ready, mode, frameIndex, layers, before, after, change, ifg, frames])

  // Resize maps when the container changes (panel open/close, rotation).
  useEffect(() => {
    if (!wrapRef.current) return
    const ro = new ResizeObserver(() => {
      maps.current?.a.resize()
      maps.current?.b.resize()
    })
    ro.observe(wrapRef.current)
    return () => ro.disconnect()
  }, [])

  const comparing = mode === 'compare' && before && after
  return (
    <div ref={wrapRef} className="absolute inset-0">
      <div className="absolute inset-0">
        <div ref={aRef} className="h-full w-full" />
      </div>
      <div className="pointer-events-none absolute inset-0" style={{ clipPath: comparing ? `inset(0 0 0 ${pos}%)` : 'inset(0 0 0 100%)' }} aria-hidden>
        <div ref={bRef} className="h-full w-full" />
      </div>
      {comparing && (
        <>
          <div className="pointer-events-none absolute inset-y-0 z-10 w-0.5 bg-select" style={{ left: `${pos}%` }}>
            <div className="absolute top-1/2 left-1/2 flex size-9 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-select text-sm font-semibold text-[#1A1206] shadow-lg">
              ⇆
            </div>
          </div>
          <SideLabel side="left" layer={before} story={story} fallbackDate={story.beforeDate} />
          <SideLabel side="right" layer={after} story={story} fallbackDate={story.afterDate} />
          <DividerHandle pos={pos} onChange={setPos} />
        </>
      )}
    </div>
  )
}

function SideLabel({ side, layer, story, fallbackDate }: { side: 'left' | 'right'; layer: StoryLayer; story: Story; fallbackDate: string }) {
  const platform = layer.platform ?? story.platform
  const pol = layer.polarization ?? story.polarization
  return (
    <div className={`pointer-events-none absolute top-16 z-10 max-w-[45%] rounded-md bg-black/60 px-2.5 py-1.5 text-xs text-white md:top-20 ${side === 'left' ? 'left-4' : 'right-4'}`}>
      <span className="type-label block text-[13px]">{platform}</span>
      <span className="num text-white/75">
        {fmtDate(layer.date ?? fallbackDate)}
        {pol ? `, ${pol}` : ''}
      </span>
    </div>
  )
}

/** Drag anywhere on the handle strip; arrow keys work through the hidden range input. */
function DividerHandle({ pos, onChange }: { pos: number; onChange: (p: number) => void }) {
  const dragging = useRef(false)
  const move = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragging.current) return
    const rect = e.currentTarget.parentElement!.getBoundingClientRect()
    onChange(Math.min(98, Math.max(2, ((e.clientX - rect.left) / rect.width) * 100)))
  }
  return (
    <>
      <div
        className="absolute inset-y-0 z-20 w-10 -translate-x-1/2 cursor-ew-resize touch-none"
        style={{ left: `${pos}%` }}
        onPointerDown={(e) => {
          dragging.current = true
          e.currentTarget.setPointerCapture(e.pointerId)
        }}
        onPointerMove={move}
        onPointerUp={() => (dragging.current = false)}
      />
      <input
        type="range"
        min={2}
        max={98}
        value={Math.round(pos)}
        onChange={(e) => onChange(Number(e.target.value))}
        aria-label="Compare before and after: move the divider"
        className="sr-only"
      />
    </>
  )
}
