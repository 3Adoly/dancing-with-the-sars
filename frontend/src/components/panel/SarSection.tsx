// "Radar views" for one incident (E10): scenes from ASF before/after the event,
// a before/after slider on the backend's bestPair, and plain explanations when
// no radar could have seen it.
import { Download, ExternalLink, Map as MapIcon } from 'lucide-react'
import { useMemo } from 'react'
import { useCoverageTimeline, useIncidentSar, useYearBounds } from '@/api/hooks'
import { isGeocodedBrowse, MISSION_BAND, type Incident, type SARResult, type SARScene } from '@/api/types'
import { fmtDate, fmtDays } from '@/lib/format'
import { useAppStore } from '@/store/app-store'
import { BeforeAfter } from './BeforeAfter'

const MISSION_NAME: Record<string, string> = {
  nisar: 'NISAR', sentinel1: 'Sentinel-1', alos: 'ALOS PALSAR', seasat: 'Seasat', saocom: 'SAOCOM', rcm: 'RCM',
}
const missionName = (m: string) => MISSION_NAME[m] ?? m
const DAY = 86_400_000

function pickPair(sar: SARResult) {
  const find = (id?: string) => [...sar.before, ...sar.after].find((s) => s.granuleId === id)
  const b = find(sar.bestPair?.before)
  const a = find(sar.bestPair?.after)
  if (b?.browseUrl && a?.browseUrl) return { before: b, after: a, aligned: true }
  const before = sar.before.find((s) => s.browseUrl)
  const after = sar.after.find((s) => s.browseUrl)
  return before && after ? { before, after, aligned: false } : null
}

function SceneRow({ s, eventTime }: { s: SARScene; eventTime: number }) {
  const setHovered = useAppStore((st) => st.setHoveredScene)
  const overlay = useAppStore((st) => st.overlaySceneId)
  const setOverlay = useAppStore((st) => st.setOverlayScene)
  const onMap = overlay === s.granuleId
  const days = Math.round((Date.parse(s.startTime) - eventTime) / DAY)
  const geocoded = isGeocodedBrowse(s)
  return (
    <li
      className="rounded-md p-1.5 text-xs hover:bg-accent/60"
      onMouseEnter={() => setHovered(s.granuleId)}
      onMouseLeave={() => setHovered(null)}
      onFocus={() => setHovered(s.granuleId)}
      onBlur={() => setHovered(null)}
    >
      <p className="text-[13px] text-foreground">
        {missionName(s.mission)} <span className="text-muted-foreground">{MISSION_BAND[s.mission] ?? ''}-band</span>
      </p>
      <p className="num text-muted-foreground">
        {fmtDate(s.startTime)}, {fmtDays(days)}
      </p>
      <p className="text-muted-foreground">
        {[s.polarization, s.flightDirection?.toLowerCase(), s.path !== undefined ? `track ${s.path}` : null, s.processingLevel]
          .filter(Boolean)
          .join(', ')}
      </p>
      <div className="mt-1 flex flex-wrap gap-x-3">
        {geocoded && (
          <button type="button" onClick={() => setOverlay(onMap ? null : s.granuleId)} className="flex items-center gap-1 text-signal hover:underline" aria-pressed={onMap}>
            <MapIcon className="size-3" /> {onMap ? 'Remove from map' : 'Show on map'}
          </button>
        )}
        {s.browseUrl && (
          <a href={s.browseUrl} target="_blank" rel="noreferrer" className="flex items-center gap-1 text-muted-foreground hover:text-foreground">
            <ExternalLink className="size-3" /> Preview image
          </a>
        )}
        {s.downloadUrl && (
          <a href={s.downloadUrl} className="flex items-center gap-1 text-muted-foreground hover:text-foreground" title="Full-resolution file. Needs a free NASA Earthdata login.">
            <Download className="size-3" /> Data file{s.sizeMB ? ` (${s.sizeMB >= 1000 ? `${(s.sizeMB / 1000).toFixed(1)} GB` : `${Math.round(s.sizeMB)} MB`})` : ''}
          </a>
        )}
      </div>
    </li>
  )
}

function CoverageStrip({ year }: { year: number }) {
  const { data } = useCoverageTimeline()
  const { min, max } = useYearBounds()
  const lo = Math.max(min, 1950)
  const pct = (y: number) => ((y - lo) / (max - lo + 1)) * 100
  if (!data) return null
  return (
    <div className="mt-3" aria-label="Imaging radar coverage through time">
      <div className="relative h-5 rounded bg-[#0A1422]">
        {data.missions.map((m) => {
          const from = Number(m.from.slice(0, 4))
          const to = m.to ? Number(m.to.slice(0, 4)) : max
          return (
            <div
              key={m.id}
              className="absolute top-1 h-3 rounded-sm opacity-80"
              style={{
                left: `${pct(from)}%`,
                width: `${Math.max(0.8, pct(to + 1) - pct(from))}%`,
                backgroundColor: m.band.startsWith('L') ? '#A87A45' : '#3E5F88',
              }}
              title={`${m.name}, ${from}–${m.to ? to : 'today'}`}
            />
          )
        })}
        {year >= lo && <div className="absolute -top-1 -bottom-1 w-0.5 bg-select" style={{ left: `${pct(year)}%` }} title={`This event: ${year}`} />}
      </div>
      <div className="num relative mt-1 h-3.5 text-[10.5px] text-muted-foreground">
        <span className="absolute left-0">{lo}</span>
        <span className="absolute -translate-x-1/2" style={{ left: `${pct(1978)}%` }}>1978</span>
        <span className="absolute -translate-x-1/2" style={{ left: `${pct(1991)}%` }}>1991</span>
        <span className="absolute -translate-x-1/2" style={{ left: `${pct(2014)}%` }}>2014</span>
        <span className="absolute right-0">Today</span>
      </div>
    </div>
  )
}

export function SarSection({ incident }: { incident: Incident }) {
  const { data, isLoading, isError, error, refetch } = useIncidentSar(String(incident.id))
  const openStory = useAppStore((s) => s.openStory)
  const pair = useMemo(() => (data ? pickPair(data) : null), [data])
  const eventTime = Date.parse(incident.startTime)
  const year = incident.year
  const empty = data && data.before.length === 0 && data.after.length === 0

  return (
    <section aria-labelledby="sar-heading" className="border-t border-line pt-4">
      <h3 id="sar-heading" className="type-display mb-1 text-base">Radar views</h3>
      <p className="mb-3 text-xs leading-relaxed text-muted-foreground">
        Imaging radar sends its own microwave pulse, so it sees through cloud, smoke and darkness. Dark areas are smooth (water);
        bright areas are rough or upright (buildings, trees).
      </p>

      {isLoading && (
        <div className="space-y-2" aria-busy>
          <div className="aspect-[4/3] animate-pulse rounded-md bg-accent/50" />
          <p className="text-xs text-muted-foreground">Searching the NASA ASF archive for scenes around {fmtDate(incident.startTime)}…</p>
        </div>
      )}

      {isError && (
        <div className="rounded-md border border-line p-3 text-xs">
          <p className="text-foreground">The radar archive didn't answer: {(error as Error).message}</p>
          <button type="button" onClick={() => refetch()} className="mt-1 text-signal hover:underline">Try again</button>
        </div>
      )}

      {data?.stale && (
        <p className="mb-2 rounded-md bg-accent px-3 py-2 text-xs text-muted-foreground">
          The archive is not responding, so these are saved results from {fmtDate(data.fetchedAt)}.
        </p>
      )}

      {data?.coverage === 'pre_sar_era' && (
        <div className="rounded-md border border-line p-3">
          <p className="text-sm text-foreground">No imaging radar was in orbit in {year}.</p>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            The first civilian imaging radar, Seasat, flew for 106 days in 1978. Continuous coverage began with ERS-1 in 1991.
            NISAR now maps nearly all land every 12 days.
          </p>
          <CoverageStrip year={year} />
        </div>
      )}

      {data && data.coverage !== 'pre_sar_era' && empty && (
        <div className="rounded-md border border-line p-3 text-xs leading-relaxed text-muted-foreground">
          <p className="text-sm text-foreground">No radar scenes within {data.windowDays} days of this event.</p>
          <p className="mt-1">
            {data.reason === 'no_searchable_mission_in_window'
              ? 'Radar satellites were flying (ERS, RADARSAT-1, Envisat), but their archives are not searchable here.'
              : data.reason === 'upstream_error'
                ? 'The ASF archive did not answer. Try again in a minute.'
                : incident.countries.length === 0
                  ? 'This event was at sea. Imaging radars mostly record over land and coasts.'
                  : 'The satellites may not have passed over this spot in that window.'}
          </p>
          <CoverageStrip year={year} />
        </div>
      )}

      {data && !empty && (
        <div className="space-y-4">
          {(data.coverage === 'before_only' || data.coverage === 'after_only') && (
            <p className="text-xs text-muted-foreground">
              Only scenes {data.coverage === 'before_only' ? 'before' : 'after'} the event were found within {data.windowDays} days.
            </p>
          )}
          {pair && (
            <BeforeAfter
              before={{ src: pair.before.browseUrl!, label: `${missionName(pair.before.mission)}, ${fmtDate(pair.before.startTime)}` }}
              after={{ src: pair.after.browseUrl!, label: `${missionName(pair.after.mission)}, ${fmtDate(pair.after.startTime)}` }}
              note={
                pair.aligned
                  ? `Same satellite track (${pair.before.flightDirection?.toLowerCase() ?? ''}${pair.before.path !== undefined ? `, track ${pair.before.path}` : ''}). Quick-look previews, not calibrated.`
                  : 'Different satellite passes, so the two previews will not line up exactly.'
              }
            />
          )}
          {data.storyId && (
            <button type="button" onClick={() => openStory(data.storyId!)} className="text-xs text-signal hover:underline">
              See the full change analysis in the radar story
            </button>
          )}
          {(['before', 'after'] as const).map((rel) =>
            data[rel].length === 0 ? null : (
              <div key={rel}>
                <h4 className="type-label mb-1 text-xs text-muted-foreground">
                  {rel === 'before' ? 'Before the event' : 'After the event'} ({data[rel].length})
                </h4>
                <ul className="space-y-0.5">
                  {data[rel].map((s) => <SceneRow key={s.granuleId} s={s} eventTime={eventTime} />)}
                </ul>
              </div>
            ),
          )}
        </div>
      )}

      {data && (
        <a href={data.asfSearchUrl} target="_blank" rel="noreferrer" className="mt-3 inline-flex items-center gap-1 text-xs text-signal hover:underline">
          Open this search in ASF Vertex <ExternalLink className="size-3" />
        </a>
      )}
    </section>
  )
}
