import { BookOpen, ExternalLink } from 'lucide-react'
import { useEffect, useState } from 'react'
import { isMockIncidentId, useDataSource } from '@/api/client'
import { useIncident, useTypeMap } from '@/api/hooks'
import type { Incident } from '@/api/types'
import { loadCountries, type CountryMeta } from '@/lib/countries'
import { fmtDate, fmtInt } from '@/lib/format'
import { typeColor } from '@/lib/theme'
import { useAppStore } from '@/store/app-store'
import { TypeIcon } from '../TypeIcon'
import { SarSection } from './SarSection'

const SOURCE_NAME: Record<string, string> = {
  usgs: 'USGS Earthquake Catalog',
  noaa_eq: 'NOAA significant earthquakes',
  noaa_ts: 'NOAA tsunami database',
  noaa_vo: 'NOAA significant volcanic eruptions',
  gvp: 'Smithsonian Global Volcanism Program',
  eonet: 'NASA EONET',
  gdacs: 'GDACS',
  ibtracs: 'NOAA IBTrACS',
  glc: 'NASA Global Landslide Catalog',
  dfo: 'Dartmouth Flood Observatory',
}

function when(i: Incident) {
  const fmt = (t: string) =>
    i.timePrecision === 'year'
      ? t.slice(0, 4)
      : i.timePrecision === 'month'
        ? new Date(t).toLocaleDateString('en-GB', { month: 'long', year: 'numeric', timeZone: 'UTC' })
        : fmtDate(t)
  return i.endTime && i.endTime.slice(0, 10) !== i.startTime.slice(0, 10) ? `${fmt(i.startTime)} to ${fmt(i.endTime)}` : fmt(i.startTime)
}

export function IncidentPanel({ id }: { id: string }) {
  const { data: incident, isLoading, isError, error } = useIncident(id)
  const typeMap = useTypeMap()
  const openStory = useAppStore((s) => s.openStory)
  const detailMocked = useDataSource((s) => s.mocked.includes('/incidents/:id'))
  const [countries, setCountries] = useState<Record<string, CountryMeta>>({})
  useEffect(() => {
    loadCountries().then(setCountries)
  }, [])

  if (isLoading) {
    return (
      <div className="space-y-3" aria-busy>
        <div className="h-4 w-24 animate-pulse rounded bg-accent/60" />
        <div className="h-7 w-3/4 animate-pulse rounded bg-accent/60" />
        <div className="h-24 animate-pulse rounded bg-accent/40" />
      </div>
    )
  }
  if (isError || !incident) {
    const status = (error as { status?: number } | null)?.status
    return (
      <p className="text-sm text-muted-foreground">
        {status === 404 && detailMocked && !isMockIncidentId(id)
          ? "Incident details aren't available yet: the backend's incident endpoint is still being built."
          : status === 404
            ? "This incident isn't in the catalog. The link may be wrong or the record was removed."
            : `Couldn't load this incident: ${(error as Error)?.message}`}
      </p>
    )
  }

  const t = typeMap.get(incident.type)
  const names = incident.countries.map((c) => countries[c]?.name ?? c)
  const hasMagnitude = incident.magnitude !== undefined && incident.magnitude !== null
  return (
    <div className="space-y-5">
      <header>
        <p className="type-label flex items-center gap-1.5 text-xs" style={{ color: typeColor(t ?? incident.type) }}>
          <TypeIcon type={incident.type} className="size-3.5" /> {t?.label.replace(/ \(.*\)$/, '').replace(/s$/, '') ?? incident.type}
        </p>
        <h2 className="type-display mt-1 text-[22px] leading-tight">{incident.title}</h2>
        <p className="num mt-1 text-sm text-muted-foreground">{when(incident)}</p>
      </header>

      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
        {hasMagnitude && (
          <div>
            <dt className="text-xs text-muted-foreground">{incident.magnitudeUnit ? `Magnitude (${incident.magnitudeUnit})` : 'Magnitude'}</dt>
            <dd className="type-display num text-lg">{fmtInt(incident.magnitude!)}</dd>
          </div>
        )}
        {incident.deaths !== undefined && (
          <div>
            <dt className="text-xs text-muted-foreground">Deaths</dt>
            <dd className="type-display num text-lg">{fmtInt(incident.deaths)}</dd>
          </div>
        )}
        <div>
          <dt className="text-xs text-muted-foreground">Where</dt>
          <dd className="text-foreground">{names.length ? names.join(', ') : 'At sea'}</dd>
          <dd className="num text-xs text-muted-foreground">
            {Math.abs(incident.lat).toFixed(2)}°{incident.lat >= 0 ? 'N' : 'S'}, {Math.abs(incident.lon).toFixed(2)}°{incident.lon >= 0 ? 'E' : 'W'}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Severity</dt>
          <dd className="text-foreground">{['None recorded', 'Minor', 'Moderate', 'Major'][incident.severity] ?? incident.severity}</dd>
        </div>
      </dl>

      {incident.description && <p className="text-sm leading-relaxed text-foreground/90">{incident.description}</p>}
      <a
        href={incident.url ?? '#'}
        target="_blank"
        rel="noreferrer"
        className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
      >
        Source: {SOURCE_NAME[incident.source] ?? incident.source} ({incident.sourceId}) <ExternalLink className="size-3" />
      </a>

      {incident.storyId && (
        <button
          type="button"
          onClick={() => openStory(incident.storyId!)}
          className="flex w-full items-center justify-center gap-2 rounded-md bg-signal py-2 text-sm font-medium text-[#1A1206] hover:brightness-110"
        >
          <BookOpen className="size-4" /> Open the radar story for this event
        </button>
      )}

      <SarSection incident={incident} />
    </div>
  )
}
