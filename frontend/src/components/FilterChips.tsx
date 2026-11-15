import { defaultTypes, useActiveTypes, useIncidentTypes } from '@/api/hooks'
import type { TypeInfo } from '@/api/types'
import { typeColor } from '@/lib/theme'
import { useAppStore } from '@/store/app-store'
import { TypeIcon } from './TypeIcon'

export function FilterChips() {
  const { data: types } = useIncidentTypes()
  const active = useActiveTypes()
  const toggle = useAppStore((s) => s.toggleType)
  const setTypes = useAppStore((s) => s.setTypes)
  const chosen = useAppStore((s) => s.types)
  if (!types) return <div className="h-8" />

  const incidents = types.filter((t) => t.unit === 'incidents')
  const detections = types.filter((t) => t.unit === 'detections')
  const isDefault = chosen === null || active.length === defaultTypes(types).length

  const chip = (t: TypeInfo) => {
    const on = active.includes(t.id)
    const label = t.label.replace(/ \(events\)$/, '')
    return (
      <button
        key={t.id}
        type="button"
        aria-pressed={on}
        onClick={() => toggle(t.id, types, active)}
        onDoubleClick={() => t.unit === 'incidents' && setTypes([t.id])}
        title={
          t.unit === 'detections'
            ? 'Satellite fire detections (MODIS). Counted in detections, so shown on their own.'
            : `${on ? 'Hide' : 'Show'} ${label.toLowerCase()}. Double-click to show only ${label.toLowerCase()}.`
        }
        className={`type-label flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-3 text-[13px] transition-colors ${
          on ? 'border-transparent text-[#0E1B2E]' : 'glass text-muted-foreground hover:text-foreground'
        }`}
        style={on ? { backgroundColor: typeColor(t) } : undefined}
      >
        <TypeIcon type={t.id} className="size-3.5" />
        {label}
      </button>
    )
  }

  return (
    <div
      className="-mx-3 flex max-w-[100vw] items-center gap-1.5 overflow-x-auto px-3 pb-1 [scrollbar-width:none] md:mx-0 md:flex-wrap md:overflow-visible md:px-0 md:pb-0"
      role="group"
      aria-label="Hazard types"
    >
      {incidents.map(chip)}
      {!isDefault && (
        <button
          type="button"
          onClick={() => setTypes(null)}
          className="type-label h-8 shrink-0 rounded-full px-2 text-[13px] text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
        >
          Show all
        </button>
      )}
      {detections.length > 0 && <span className="mx-1 h-5 w-px shrink-0 bg-line" aria-hidden />}
      {detections.map(chip)}
    </div>
  )
}
