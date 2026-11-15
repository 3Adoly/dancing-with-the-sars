// Bottom timeline: incidents per year (bars), the year-range slider, play, and
// the radar-era strip showing which imaging radars were in orbit each year.
// The strip makes the core constraint visible: NISAR only covers late 2025 on.
import { Pause, Play } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useActiveTypes, useCoverageTimeline, useTimeseries, useYearBounds } from '@/api/hooks'
import type { SARMission } from '@/api/types'
import { Slider } from '@/components/ui/slider'
import { fmtInt } from '@/lib/format'
import { useAppStore } from '@/store/app-store'

const yearOf = (d: string) => Number(d.slice(0, 4))

function assignRows(missions: SARMission[], maxYear: number) {
  const rowsEnd: number[] = []
  return [...missions]
    .sort((a, b) => a.from.localeCompare(b.from))
    .map((m) => {
      const fromYear = yearOf(m.from)
      const end = (m.to ? yearOf(m.to) : maxYear) + 1
      let row = rowsEnd.findIndex((e) => e <= fromYear)
      if (row === -1) row = rowsEnd.length
      rowsEnd[row] = end
      return { m, row, fromYear, end }
    })
}

export function TimelineBar() {
  const types = useActiveTypes()
  const from = useAppStore((s) => s.from)
  const to = useAppStore((s) => s.to)
  const setRange = useAppStore((s) => s.setRange)
  const playing = useAppStore((s) => s.playing)
  const setPlaying = useAppStore((s) => s.setPlaying)
  const bounds = useYearBounds()
  const span = bounds.max - bounds.min + 1
  const pct = (year: number) => ((year - bounds.min) / span) * 100

  const { data: ts } = useTimeseries(types, bounds.min, bounds.max, null)
  const { data: coverage } = useCoverageTimeline()

  // Local value so the thumbs move instantly; the store (and network) follow after 150 ms.
  const [local, setLocal] = useState<[number, number]>([from, to])
  useEffect(() => setLocal([from, to]), [from, to])
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const onSlide = (v: number[]) => {
    setLocal([v[0], v[1]])
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setRange(v[0], v[1]), 150)
  }

  const maxCount = useMemo(() => Math.max(1, ...(ts?.points.map((p) => p.n) ?? [1])), [ts])
  const inRangeTotal = useMemo(
    () => ts?.points.filter((p) => p.year >= from && p.year <= to).reduce((a, p) => a + p.n, 0) ?? 0,
    [ts, from, to],
  )
  const rows = useMemo(() => (coverage ? assignRows(coverage.missions, bounds.max) : []), [coverage, bounds.max])
  const rowCount = Math.max(1, ...rows.map((r) => r.row + 1))
  const unit = ts?.unit === 'detections' ? 'fire detections' : 'incidents'
  const ticks = useMemo(() => {
    const step = span > 90 ? 20 : 10
    const out: number[] = []
    for (let y = Math.ceil(bounds.min / step) * step; y <= bounds.max - 4; y += step) out.push(y)
    return out
  }, [bounds.min, bounds.max, span])
  const presets = [
    { label: '10 years', years: 10 },
    { label: '25 years', years: 25 },
    { label: '50 years', years: 50 },
    { label: 'All', years: span },
  ]

  return (
    <section className="glass rounded-xl px-4 pt-3 pb-3" aria-label="Time range">
      <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1">
        <button
          type="button"
          onClick={() => setPlaying(!playing)}
          className="flex size-8 items-center justify-center rounded-full bg-signal text-[#1A1206] hover:brightness-110"
          aria-label={playing ? 'Pause year animation' : 'Play years forward'}
        >
          {playing ? <Pause className="size-4" /> : <Play className="ml-0.5 size-4" />}
        </button>
        <p className="type-display num text-xl leading-none text-foreground">
          {local[0]}–{local[1]}
        </p>
        <p className="num text-sm text-muted-foreground">
          {fmtInt(inRangeTotal)} {unit}
        </p>
        <div className="flex items-center gap-1 sm:ml-auto" role="group" aria-label="Year presets">
          <span className="mr-1 text-xs text-muted-foreground">Last</span>
          {presets.map((p) => {
            const active = p.years === span ? from === bounds.min && to === bounds.max : to === bounds.max && to - from === p.years
            return (
              <button
                key={p.label}
                type="button"
                onClick={() => setRange(p.years === span ? bounds.min : bounds.max - p.years, bounds.max)}
                className={`type-label rounded-md px-2 py-1 text-xs ${active ? 'bg-accent text-foreground' : 'text-muted-foreground hover:text-foreground'}`}
                aria-pressed={active}
              >
                {p.label}
              </button>
            )
          })}
        </div>
      </div>

      {/* Bars + slider share one horizontal scale */}
      <div className="relative">
        <svg viewBox={`0 0 ${span} 40`} preserveAspectRatio="none" className="block h-10 w-full" aria-hidden>
          {ts?.points.map((p) => {
            const h = Math.max(p.n > 0 ? 1.2 : 0, (p.n / maxCount) * 38)
            const inside = p.year >= local[0] && p.year <= local[1]
            return (
              <rect
                key={p.year}
                x={p.year - bounds.min + 0.12}
                y={40 - h}
                width={0.76}
                height={h}
                fill={inside ? '#F5B66B' : '#3A5578'}
                opacity={inside ? 0.95 : 0.7}
              />
            )
          })}
        </svg>
        <Slider
          min={bounds.min}
          max={bounds.max}
          step={1}
          minStepsBetweenThumbs={0}
          value={local}
          onValueChange={onSlide}
          className="-mt-1"
          aria-label="Year range"
        />
        <div className="num relative mt-1.5 h-3.5 text-[10.5px] text-muted-foreground" aria-hidden>
          {ticks.map((y) => (
            <span key={y} className="absolute -translate-x-1/2" style={{ left: `${pct(y + 0.5)}%` }}>
              {y}
            </span>
          ))}
        </div>
      </div>

      {/* Radar eras (hidden on phones: too narrow to label) */}
      <div className="mt-4 hidden sm:block">
        <p className="mb-1 text-[11px] text-muted-foreground">
          Imaging radars in orbit. <span className="text-[#F5B66B]">L-band</span> sees through vegetation,{' '}
          <span className="text-[#8FB0D6]">C-band</span> is shorter. Outlined bars are radars whose archives aren't searchable here (hover for names).
        </p>
        <div className="relative" style={{ height: rowCount * 15 }}>
          {rows.map(({ m, row, fromYear, end }) => {
            const left = pct(Math.max(bounds.min, fromYear))
            const width = Math.max(0.6, pct(end) - left)
            const narrow = width < m.name.length * 0.62
            const isL = m.band.startsWith('L')
            const isNisar = m.id === 'nisar'
            const fill = isL ? (isNisar ? '#F5B66B' : '#A87A45') : '#3E5F88'
            return (
              <div
                key={m.id}
                className="absolute flex items-center"
                style={{ left: `${left}%`, width: `${width}%`, top: row * 15, height: 12 }}
                title={`${m.name} (${m.agency}), ${m.band}-band, ${m.from.slice(0, 4)}–${m.to ? m.to.slice(0, 4) : 'today'}${m.searchable ? '' : ', not searchable here'}${m.note ? `. ${m.note}` : ''}`}
              >
                <div
                  className={`h-full w-full rounded-[3px] ${isNisar ? 'ring-1 ring-select' : ''}`}
                  style={m.searchable ? { backgroundColor: fill } : { border: `1px solid ${fill}`, backgroundColor: 'transparent' }}
                />
                {m.searchable && (
                <span
                  className={`type-label absolute text-[10.5px] leading-none whitespace-nowrap ${
                    narrow ? (left > 80 ? 'right-full mr-1.5' : 'left-full ml-1.5') : 'left-1.5'
                  } ${isNisar ? 'text-select' : narrow || !m.searchable ? 'text-muted-foreground' : isL ? 'text-[#1A1206]' : 'text-[#DCE6F2]'}`}
                >
                  {m.name}
                </span>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </section>
  )
}

/** Moves the year window forward while playing. Mount once. */
export function usePlayback() {
  const playing = useAppStore((s) => s.playing)
  const bounds = useYearBounds()
  useEffect(() => {
    if (!playing) return
    const s = useAppStore.getState()
    const start = Math.max(bounds.min, 1950)
    if (s.to - s.from >= 30 || s.to >= bounds.max) s.setRange(start, start + 9)
    const id = setInterval(() => {
      const st = useAppStore.getState()
      if (st.to >= bounds.max) {
        st.setPlaying(false)
        return
      }
      st.setRange(st.from + 1, st.to + 1)
    }, 650)
    return () => clearInterval(id)
  }, [playing, bounds.min, bounds.max])
}
