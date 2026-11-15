import { Search, X } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useActiveTypes, useSearch } from '@/api/hooks'
import type { IncidentSummary } from '@/api/types'
import { typeColor } from '@/lib/theme'
import { useAppStore } from '@/store/app-store'
import { TypeIcon } from './TypeIcon'

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

export function SearchBox() {
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const q = useDebounced(text, 250)
  const { data, isFetching } = useSearch(q)
  const activeTypes = useActiveTypes()
  const listId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const results = q.trim().length >= 2 ? (data?.items ?? []) : []

  const choose = (r: IncidentSummary) => {
    const s = useAppStore.getState()
    const year = Number(r.startTime.slice(0, 4))
    // Make sure the chosen event is inside the year window and type filter.
    s.setRange(Math.min(s.from, year), Math.max(s.to, year))
    if (!activeTypes.includes(r.type)) s.setTypes([...activeTypes.filter((t) => t !== 'fire_activity'), r.type])
    s.openStory(null)
    s.select({ kind: 'incident', id: String(r.id) })
    setText(r.title)
    setOpen(false)
    inputRef.current?.blur()
  }

  useEffect(() => setActive(0), [q])

  // "/" focuses search, like many map tools.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === '/' && document.activeElement?.tagName !== 'INPUT') {
        e.preventDefault()
        inputRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const showList = open && text.trim().length >= 2

  return (
    <div className="relative w-full max-w-md">
      <label htmlFor={`${listId}-input`} className="sr-only">Search events</label>
      <div className="glass flex h-10 items-center gap-2 rounded-lg px-3">
        <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden />
        <input
          id={`${listId}-input`}
          ref={inputRef}
          value={text}
          onChange={(e) => { setText(e.target.value); setOpen(true) }}
          onFocus={() => setOpen(true)}
          onBlur={() => setTimeout(() => setOpen(false), 120)}
          onKeyDown={(e) => {
            if (!showList || results.length === 0) return
            if (e.key === 'ArrowDown') { e.preventDefault(); setActive((a) => (a + 1) % results.length) }
            if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => (a - 1 + results.length) % results.length) }
            if (e.key === 'Enter') { e.preventDefault(); choose(results[active]) }
            if (e.key === 'Escape') setOpen(false)
          }}
          placeholder="Search an event, like “Tohoku” or “Pakistan flood”"
          className="h-full w-full bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
          role="combobox"
          aria-expanded={showList}
          aria-controls={listId}
          aria-activedescendant={showList && results[active] ? `${listId}-${active}` : undefined}
          autoComplete="off"
        />
        {text && (
          <button
            type="button"
            onClick={() => { setText(''); inputRef.current?.focus() }}
            className="rounded p-1 text-muted-foreground hover:text-foreground"
            aria-label="Clear search"
          >
            <X className="size-3.5" />
          </button>
        )}
      </div>
      {showList && (
        <ul id={listId} role="listbox" className="glass absolute top-12 right-0 left-0 z-30 max-h-80 overflow-auto rounded-lg py-1 shadow-2xl">
          {results.length === 0 && (
            <li className="px-3 py-2.5 text-sm text-muted-foreground">
              {isFetching ? 'Searching…' : 'No events match. Try a place, a year or a hazard type.'}
            </li>
          )}
          {results.map((r, i) => {
            return (
              <li
                key={r.id}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                onMouseDown={(e) => { e.preventDefault(); choose(r) }}
                onMouseEnter={() => setActive(i)}
                className={`flex cursor-pointer items-center gap-3 px-3 py-2 text-sm ${i === active ? 'bg-accent' : ''}`}
              >
                <TypeIcon type={r.type} className="size-4 shrink-0" style={{ color: typeColor(r.type) }} />
                <span className="min-w-0 flex-1 truncate">{r.title}</span>
                <span className="num shrink-0 text-xs text-muted-foreground">{r.startTime.slice(0, 4)}</span>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
