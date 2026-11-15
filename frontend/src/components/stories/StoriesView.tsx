// Stories mode: the heart of the challenge. Curated radar stories (E12/E13),
// each opening a full-screen comparison with a plain-language explanation.
import { ArrowLeft, Info, MapPin, Pause, Play, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { mediaUrl } from '@/api/client'
import { useStories, useStory, useTypeMap } from '@/api/hooks'
import type { Legend, Story, StoryLayerKind } from '@/api/types'
import { fmtDate } from '@/lib/format'
import { useAppStore } from '@/store/app-store'
import { indexLayers, StoryMap, type StoryMode } from './StoryMap'

const isPlaceholder = (s: { title: string; summary: string }) => /placeholder|sample imagery/i.test(`${s.title} ${s.summary}`)
const cleanTitle = (t: string) => t.replace(/^PLACEHOLDER\s*[–-]\s*[^:]*:\s*/i, '')

export function StoriesView() {
  const storyId = useAppStore((s) => s.storyId)
  const openStory = useAppStore((s) => s.openStory)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && document.activeElement?.tagName !== 'INPUT') openStory(storyId === 'index' ? null : 'index')
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [storyId, openStory])

  return (
    <div className="fixed inset-0 z-40 bg-space" role="dialog" aria-modal aria-label="Radar stories">
      {storyId === 'index' || !storyId ? <StoryIndex /> : <StoryPlayer id={storyId} />}
    </div>
  )
}

function StoryIndex() {
  const { data: stories, isLoading, isError, error } = useStories()
  const openStory = useAppStore((s) => s.openStory)
  const typeMap = useTypeMap()
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-4 py-8 md:py-14">
        <div className="mb-8 flex items-start justify-between gap-4">
          <div>
            <h1 className="type-display text-3xl md:text-4xl">Radar stories</h1>
            <p className="mt-3 max-w-[60ch] text-[15px] leading-relaxed text-muted-foreground">
              Places where imaging radar shows a change on the ground that a normal photo can't: floodwater under cloud,
              forest lost under smoke, ground that rose or sank by centimeters.
            </p>
          </div>
          <button
            type="button"
            onClick={() => openStory(null)}
            className="flex shrink-0 items-center gap-1.5 rounded-lg border border-line px-3 py-2 text-sm text-muted-foreground hover:text-foreground"
          >
            <X className="size-4" /> Back to the globe
          </button>
        </div>

        {isLoading && <p className="text-sm text-muted-foreground">Loading stories…</p>}
        {isError && <p className="text-sm text-muted-foreground">Stories couldn't load: {(error as Error).message}</p>}
        {stories && stories.length === 0 && <p className="text-sm text-muted-foreground">No stories have been published yet.</p>}
        <ol className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {stories?.map((s) => (
            <li key={s.id}>
              <button
                type="button"
                onClick={() => openStory(s.id)}
                className="group block w-full overflow-hidden rounded-xl border border-line bg-card text-left transition-colors hover:border-signal"
              >
                <div className="relative aspect-[36/22] overflow-hidden bg-[#0A1422]">
                  <img src={mediaUrl(s.thumbnail)} alt="" className="h-full w-full object-cover" loading="lazy" />
                  {isPlaceholder(s) && (
                    <span className="type-label absolute top-2 left-2 rounded bg-black/65 px-1.5 py-0.5 text-[11px] text-white">
                      Placeholder imagery
                    </span>
                  )}
                </div>
                <div className="p-4">
                  <p className="type-label text-xs text-signal">
                    {typeMap.get(s.type)?.label.replace(/ \(.*\)$/, '') ?? s.type}, {s.platform}
                  </p>
                  <h2 className="type-display mt-1 text-lg leading-snug group-hover:text-select">{cleanTitle(s.title)}</h2>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {s.place}, {fmtDate(s.eventDate)}
                  </p>
                  <p className="mt-3 line-clamp-3 text-sm leading-relaxed text-foreground/85">{s.summary}</p>
                </div>
              </button>
            </li>
          ))}
        </ol>
      </div>
    </div>
  )
}

const MODE_LABEL: Record<StoryMode, string> = {
  compare: 'Before and after',
  change: 'What changed',
  interferogram: 'Ground motion',
  timeseries: 'Over time',
}

function availableModes(story: Story): StoryMode[] {
  const has = (k: StoryLayerKind) => story.layers.some((l) => l.kind === k)
  const modes: StoryMode[] = []
  if (has('before') && has('after')) modes.push('compare')
  if (has('change')) modes.push('change')
  if (has('interferogram')) modes.push('interferogram')
  if (has('frame')) modes.push('timeseries')
  return modes
}

function LegendBar({ legend }: { legend: Legend }) {
  const colors = legend.colors?.length ? legend.colors : legend.type === 'diverging' ? ['#3E8FD6', '#f7f7f7', '#E5484D'] : ['#2E5E9E', '#F2B84B', '#E5484D']
  return (
    <div className="glass pointer-events-auto w-72 max-w-full rounded-lg px-3 py-2 text-xs">
      <div className="h-2.5 rounded-full" style={{ background: `linear-gradient(90deg, ${colors.join(', ')})` }} />
      <div className="mt-1.5 flex justify-between gap-3 text-muted-foreground">
        <span>{legend.lowLabel}</span>
        <span className="text-right">{legend.highLabel}</span>
      </div>
      {legend.type !== 'cyclic' && (
        <p className="num mt-0.5 text-[10.5px] text-muted-foreground/80">
          {legend.min} to {legend.max} {legend.unit}
        </p>
      )}
    </div>
  )
}

function StoryPlayer({ id }: { id: string }) {
  const { data: story, isLoading, isError } = useStory(id)
  const openStory = useAppStore((s) => s.openStory)
  const select = useAppStore((s) => s.select)
  const typeMap = useTypeMap()
  const modes = useMemo(() => (story ? availableModes(story) : []), [story])
  const [mode, setMode] = useState<StoryMode>('compare')
  const [frame, setFrame] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [panelOpen, setPanelOpen] = useState(true)
  const layers = useMemo(() => (story ? indexLayers(story) : []), [story])
  const frames = useMemo(() => layers.filter((l) => l.kind === 'frame'), [layers])

  useEffect(() => {
    if (modes.length && !modes.includes(mode)) setMode(modes[0])
  }, [modes, mode])

  useEffect(() => {
    if (!playing || frames.length < 2) return
    const t = setInterval(() => setFrame((f) => (f + 1) % frames.length), 900)
    return () => clearInterval(t)
  }, [playing, frames.length])

  // Preload frames so playback is smooth.
  useEffect(() => {
    for (const f of frames) new Image().src = mediaUrl(f.url)
  }, [frames])

  if (isLoading) return <div className="flex h-full items-center justify-center text-sm text-muted-foreground">Loading story…</div>
  if (isError || !story) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
        <p>This story isn't available.</p>
        <button type="button" onClick={() => openStory('index')} className="text-signal hover:underline">See all stories</button>
      </div>
    )
  }

  const before = layers.find((l) => l.kind === 'before')
  const after = layers.find((l) => l.kind === 'after')
  const beforePlatform = before?.platform ?? story.platform
  const afterPlatform = after?.platform ?? story.platform
  const crossSensor = before && after && beforePlatform !== afterPlatform
  const legend =
    mode === 'change'
      ? layers.find((l) => l.kind === 'change')?.legend
      : mode === 'interferogram'
        ? layers.find((l) => l.kind === 'interferogram')?.legend
        : mode === 'timeseries'
          ? frames[frame]?.legend
          : undefined
  const typeLabel = typeMap.get(story.type)?.label.replace(/ \(.*\)$/, '') ?? story.type

  return (
    <div className="flex h-full flex-col md:flex-row">
      {/* Map stage */}
      <div className="relative min-h-[50vh] flex-1">
        <StoryMap story={story} mode={mode} frameIndex={frame} />

        <div className="pointer-events-none absolute inset-x-0 top-0 z-20 flex items-start justify-between gap-3 p-3 md:p-4">
          <button
            type="button"
            onClick={() => openStory('index')}
            className="glass pointer-events-auto flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm text-foreground"
          >
            <ArrowLeft className="size-4" /> All stories
          </button>
          {!panelOpen && (
            <button
              type="button"
              onClick={() => setPanelOpen(true)}
              className="glass pointer-events-auto hidden items-center gap-1.5 rounded-lg px-3 py-2 text-sm md:flex"
            >
              <Info className="size-4" /> How to read this image
            </button>
          )}
        </div>

        {/* Mode switcher, time controls, legend */}
        <div className="pointer-events-none absolute inset-x-0 bottom-0 z-20 flex flex-col items-center gap-2 p-3 md:p-5">
          {mode === 'timeseries' && frames.length > 1 && (
            <div className="glass pointer-events-auto flex items-center gap-3 rounded-full px-3 py-1.5">
              <button
                type="button"
                onClick={() => setPlaying(!playing)}
                className="flex size-7 items-center justify-center rounded-full bg-signal text-[#1A1206]"
                aria-label={playing ? 'Pause' : 'Play'}
              >
                {playing ? <Pause className="size-3.5" /> : <Play className="ml-0.5 size-3.5" />}
              </button>
              <input
                type="range"
                min={0}
                max={frames.length - 1}
                value={frame}
                onChange={(e) => {
                  setPlaying(false)
                  setFrame(Number(e.target.value))
                }}
                className="w-40 accent-[#F5B66B]"
                aria-label="Frame"
              />
              <span className="num w-24 text-sm text-foreground">{frames[frame]?.label ?? (frames[frame]?.date && fmtDate(frames[frame].date!))}</span>
            </div>
          )}
          {modes.length > 1 && (
            <div className="glass pointer-events-auto flex rounded-lg p-1" role="tablist" aria-label="View">
              {modes.map((m) => (
                <button
                  key={m}
                  type="button"
                  role="tab"
                  aria-selected={mode === m}
                  onClick={() => setMode(m)}
                  className={`type-label rounded-md px-3 py-1.5 text-[13px] ${mode === m ? 'bg-signal text-[#1A1206]' : 'text-muted-foreground hover:text-foreground'}`}
                >
                  {MODE_LABEL[m]}
                </button>
              ))}
            </div>
          )}
          {legend && <LegendBar legend={legend} />}
        </div>
      </div>

      {/* Explanation panel */}
      {panelOpen && (
        <aside className="relative max-h-[50vh] overflow-y-auto border-t border-line bg-card md:max-h-none md:w-[400px] md:border-t-0 md:border-l">
          <div className="space-y-5 p-5">
            <div className="flex items-start justify-between gap-2">
              <p className="type-label text-xs text-signal">
                {typeLabel}, {fmtDate(story.eventDate)}
              </p>
              <button
                type="button"
                onClick={() => setPanelOpen(false)}
                className="-mt-1 hidden rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground md:block"
                aria-label="Hide explanation"
              >
                <X className="size-4" />
              </button>
            </div>
            <header>
              <h1 className="type-display text-2xl leading-tight">{cleanTitle(story.title)}</h1>
              <p className="mt-1 text-sm text-muted-foreground">{story.place}</p>
            </header>

            {story.explanation.caveats && (
              <p className="rounded-md border border-dashed border-signal/60 px-3 py-2 text-xs leading-relaxed text-signal">
                {story.explanation.caveats}
              </p>
            )}
            {crossSensor && (
              <p className="rounded-md bg-accent px-3 py-2 text-xs leading-relaxed">
                Two different satellites: {beforePlatform} on the left, {afterPlatform} on the right. Compare patterns, not exact
                brightness.
              </p>
            )}

            <p className="text-[15px] leading-relaxed">{story.summary}</p>

            <section>
              <h2 className="type-display mb-2 text-base">How to read this image</h2>
              <p className="text-sm leading-relaxed text-foreground/90">{story.explanation.howToRead}</p>
            </section>

            <dl className="space-y-3 text-sm leading-relaxed">
              <div>
                <dt className="type-label text-xs text-muted-foreground">Why it looks this way</dt>
                <dd>{story.explanation.driver}</dd>
              </div>
              <div>
                <dt className="type-label text-xs text-muted-foreground">Which polarization shows it</dt>
                <dd>{story.explanation.polarizationNote}</dd>
              </div>
              <div>
                <dt className="type-label text-xs text-muted-foreground">Data</dt>
                <dd>
                  {story.product}, {story.band}-band {story.polarization}. Before {fmtDate(story.beforeDate)}, after {fmtDate(story.afterDate)}.
                </dd>
              </div>
            </dl>

            {story.incidentId !== undefined && (
              <button
                type="button"
                onClick={() => {
                  select({ kind: 'incident', id: String(story.incidentId) })
                  openStory(null)
                }}
                className="flex items-center gap-1.5 text-sm text-signal hover:underline"
              >
                <MapPin className="size-4" /> Show this event on the globe
              </button>
            )}

            <footer className="border-t border-line pt-3 text-[11px] leading-relaxed text-muted-foreground">
              {story.credits.map((c) => (
                <p key={c}>{c}</p>
              ))}
            </footer>
          </div>
        </aside>
      )}
    </div>
  )
}
