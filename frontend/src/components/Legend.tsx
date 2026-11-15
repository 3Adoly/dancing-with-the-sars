import { useMemo } from 'react'
import { useActiveTypes, useChoropleth, useMeta, useTypeMap } from '@/api/hooks'
import { bucketLegend } from '@/lib/buckets'
import { fmtInt } from '@/lib/format'
import { COUNT_RAMP, typeColor, typeRamp } from '@/lib/theme'
import { useAppStore } from '@/store/app-store'

const short = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 })
const fmtShort = (n: number) => (n < 1000 ? String(n) : short.format(n))

export function Legend() {
  const types = useActiveTypes()
  const from = useAppStore((s) => s.from)
  const to = useAppStore((s) => s.to)
  const typeMap = useTypeMap()
  const { data: meta } = useMeta()
  const { data, isFetching } = useChoropleth(types, from, to)
  const zoom = useAppStore((s) => s.mapZoom)

  const single = types.length === 1 ? typeMap.get(types[0]) : undefined
  const ramp = useMemo(() => (single ? typeRamp(typeColor(single)) : [...COUNT_RAMP]), [single])
  if (!data) return null
  const unit = data.unit === 'detections' ? 'detections' : 'incidents'
  if (zoom >= 5.5) {
    return (
      <div className="glass rounded-lg px-3 py-2 text-xs text-muted-foreground">
        Each dot is one incident, colored by type. Numbered circles group nearby incidents.
      </div>
    )
  }
  if (zoom >= 3 && meta?.features.grid !== false) {
    return (
      <div className="glass max-w-72 rounded-lg px-3 py-2 text-xs leading-relaxed text-muted-foreground">
        Taller, brighter columns mean more {unit} in that {zoom < 4 ? '2°' : '1°'} square. Tilt the map with right-drag or the compass
        button to see them in 3D.
      </div>
    )
  }

  const items = bucketLegend(data.breaks, ramp)
  const what = single ? single.label.replace(/ \(.*\)$/, '').toLowerCase() : unit

  return (
    <div className="glass w-72 rounded-lg p-3 text-xs" aria-live="polite">
      <p className="mb-2 text-[13px] leading-snug text-foreground">
        Recorded {what} per country, <span className="num whitespace-nowrap">{from}–{to}</span>
      </p>
      <div className={`transition-opacity ${isFetching ? 'opacity-60' : ''}`}>
        <div className="flex h-2.5 overflow-hidden rounded-sm">
          {items.map((it) => (
            <div key={it.label} className="flex-1" style={{ backgroundColor: it.color }} title={it.label} />
          ))}
        </div>
        {/* Edge ticks: 1, then each bucket's upper bound */}
        <div className="num relative mt-1 h-3.5 text-[10.5px] text-muted-foreground" aria-label={items.map((i) => i.label).join(', ')}>
          {data.breaks.map((b, i) => (
            <span
              key={b}
              className={`absolute ${i === 0 ? 'left-0' : i === data.breaks.length - 1 ? 'right-0' : '-translate-x-1/2'}`}
              style={i === 0 || i === data.breaks.length - 1 ? undefined : { left: `${(i / (data.breaks.length - 1)) * 100}%` }}
            >
              {i === 0 ? 1 : fmtShort(b)}
            </span>
          ))}
        </div>
      </div>
      <div className="mt-2 flex items-center justify-between text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <span className="inline-block size-2.5 rounded-sm bg-[#22344D] ring-1 ring-line" /> None recorded
        </span>
        {data.ocean > 0 && <span className="num">{fmtInt(data.ocean)} at sea</span>}
      </div>
      {data.coverage.length > 0 && (
        <ul className="mt-2 space-y-0.5 border-t border-line pt-2 text-muted-foreground">
          {data.coverage.map((c) => (
            <li key={c.type}>
              {typeMap.get(c.type)?.label.replace(/ \(.*\)$/, '') ?? c.type}: {c.note}.
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
