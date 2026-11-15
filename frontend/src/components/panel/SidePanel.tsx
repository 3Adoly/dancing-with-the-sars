import { ArrowLeft, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useIncident } from '@/api/hooks'
import { loadCountries } from '@/lib/countries'
import { useAppStore } from '@/store/app-store'
import { CountryPanel } from './CountryPanel'
import { IncidentPanel } from './IncidentPanel'

/** Right-hand panel on desktop, bottom sheet on phones. */
export function SidePanel() {
  const selection = useAppStore((s) => s.selection)
  const select = useAppStore((s) => s.select)
  const incidentId = selection?.kind === 'incident' ? selection.id : null
  const { data: incident } = useIncident(incidentId)
  const scrollRef = useRef<HTMLDivElement>(null)
  const [countryName, setCountryName] = useState<string | null>(null)
  const panelIso3 = incident?.primaryIso3 ?? incident?.countries[0]
  useEffect(() => {
    if (!panelIso3) return
    loadCountries().then((c) => setCountryName(c[panelIso3]?.name ?? null))
  }, [panelIso3])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: 0 })
  }, [selection])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && document.activeElement?.tagName !== 'INPUT') select(null)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [select])

  if (!selection) return null
  const iso3 = incident?.primaryIso3 ?? incident?.countries[0]
  const backTo = iso3 ? { kind: 'country' as const, id: iso3 } : null

  return (
    <aside
      className="glass pointer-events-auto fixed inset-x-2 bottom-2 z-20 flex max-h-[62vh] flex-col rounded-xl shadow-2xl md:inset-x-auto md:top-4 md:right-4 md:bottom-4 md:max-h-none md:w-[400px]"
      aria-label={selection.kind === 'country' ? 'Country details' : 'Incident details'}
    >
      <div className="flex items-center justify-between border-b border-line px-4 py-2">
        {selection.kind === 'incident' && backTo ? (
          <button
            type="button"
            onClick={() => select(backTo)}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" /> {countryName ?? iso3}
          </button>
        ) : (
          <span />
        )}
        <button
          type="button"
          onClick={() => select(null)}
          className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          aria-label="Close panel"
        >
          <X className="size-4" />
        </button>
      </div>
      <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
        {selection.kind === 'country' ? <CountryPanel code={selection.id} /> : <IncidentPanel id={selection.id} />}
      </div>
    </aside>
  )
}
