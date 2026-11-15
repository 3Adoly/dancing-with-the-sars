import { ZoomIn } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useActiveTypes, usePerTypeCounts, useTimeseries, useTypeMap } from '@/api/hooks'
import { type CountryMeta, loadCountries } from '@/lib/countries'
import { fmtCompact, fmtInt } from '@/lib/format'
import { typeColor } from '@/lib/theme'
import { getMap } from '@/map/bridge'
import { basePadding } from '@/map/MapView'
import { useAppStore } from '@/store/app-store'
import { TypeIcon } from '../TypeIcon'

export function CountryPanel({ code }: { code: string }) {
  const types = useActiveTypes()
  const from = useAppStore((s) => s.from)
  const to = useAppStore((s) => s.to)
  const typeMap = useTypeMap()
  const { data: ts, isFetching } = useTimeseries(types, from, to, code)
  const perType = usePerTypeCounts(types, from, to, code)
  const [meta, setMeta] = useState<CountryMeta | null>(null)

  useEffect(() => {
    loadCountries().then((c) => setMeta(c[code] ?? null))
  }, [code])

  const total = ts?.points.reduce((a, p) => a + p.n, 0) ?? 0
  const unit = ts?.unit === 'detections' ? 'fire detections' : 'recorded incidents'
  const breakdown = useMemo(() => [...perType].sort((a, b) => b.n - a.n), [perType])
  const maxN = Math.max(1, ...breakdown.map((b) => b.n))
  const barColor = types.length === 1 ? typeColor(typeMap.get(types[0]) ?? types[0]) : '#F5B66B'

  const zoomIn = () => {
    const map = getMap()
    if (meta && map) map.flyTo({ center: meta.center, zoom: 5.8, padding: basePadding(true) })
  }

  return (
    <div className="space-y-5">
      <header>
        <p className="text-xs text-muted-foreground">{meta?.continent ?? 'Country'}</p>
        <h2 className="type-display text-2xl leading-tight">{meta?.name ?? code}</h2>
        {meta?.pop ? (
          <p className="num mt-1 text-xs text-muted-foreground">Population about {fmtCompact(meta.pop)} (Natural Earth estimate)</p>
        ) : null}
      </header>

      <div className={isFetching ? 'opacity-70 transition-opacity' : ''}>
        <p className="type-display num text-4xl leading-none">{fmtInt(total)}</p>
        <p className="mt-1 text-sm text-muted-foreground">
          {unit}, <span className="num">{from}–{to}</span>
        </p>
      </div>

      {ts && total === 0 ? (
        <p className="rounded-md border border-line p-3 text-sm leading-relaxed text-muted-foreground">
          Nothing of the selected types was recorded here in <span className="num">{from}–{to}</span>. Try a wider year range or turn on
          more hazard types.
        </p>
      ) : (
        <>
          {breakdown.length > 1 && (
            <ul className="space-y-1.5" aria-label="Incidents by type">
              {breakdown.map(({ type, n, loading }) => {
                const t = typeMap.get(type)
                return (
                  <li key={type} className="grid grid-cols-[1.25rem_7.5rem_1fr_3.5rem] items-center gap-2 text-sm">
                    <TypeIcon type={type} className="size-4" style={{ color: typeColor(t ?? type) }} />
                    <span className="truncate text-foreground">{t?.label.replace(/ \(.*\)$/, '') ?? type}</span>
                    <span className="h-1.5 rounded-full bg-accent/60">
                      <span className="block h-full rounded-full" style={{ width: `${(n / maxN) * 100}%`, backgroundColor: typeColor(t ?? type) }} />
                    </span>
                    <span className="num text-right text-muted-foreground">{loading ? '…' : fmtInt(n)}</span>
                  </li>
                )
              })}
            </ul>
          )}

          <figure className="m-0">
            <figcaption className="mb-1 text-xs text-muted-foreground">Per year</figcaption>
            <div className="h-36">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={ts?.points ?? []} margin={{ top: 4, right: 0, bottom: 0, left: -28 }} barCategoryGap={1}>
                  <XAxis dataKey="year" tick={{ fill: '#93A4BC', fontSize: 10 }} tickLine={false} axisLine={{ stroke: '#2C4566' }} minTickGap={24} />
                  <YAxis tick={{ fill: '#93A4BC', fontSize: 10 }} tickLine={false} axisLine={false} allowDecimals={false} width={48} />
                  <Tooltip
                    cursor={{ fill: 'rgba(245,182,107,0.08)' }}
                    contentStyle={{ background: '#13243B', border: '1px solid #2C4566', borderRadius: 6, fontSize: 12 }}
                    labelStyle={{ color: '#E8EDF4' }}
                    formatter={(v) => [fmtInt(Number(v)), unit]}
                  />
                  <Bar dataKey="n" fill={barColor} isAnimationActive={false} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </figure>
        </>
      )}

      <button
        type="button"
        onClick={zoomIn}
        className="flex w-full items-center justify-center gap-2 rounded-md border border-line py-2 text-sm text-foreground hover:bg-accent"
      >
        <ZoomIn className="size-4" /> Zoom in to see individual incidents
      </button>
    </div>
  )
}
