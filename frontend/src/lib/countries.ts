import type { BBox, Iso3 as CountryCode } from '@/api/types'

export interface CountryMeta {
  name: string
  continent: string
  pop: number
  bbox: BBox
  /** Bounds of the largest landmass — use for fly-to. */
  mainBbox: BBox
  center: [number, number]
}

let cache: Promise<Record<CountryCode, CountryMeta>> | null = null

/** Natural Earth country names, population and bounds, keyed by ADM0_A3. Loaded once. */
export function loadCountries(): Promise<Record<CountryCode, CountryMeta>> {
  const base = import.meta.env?.BASE_URL ?? '/'
  cache ??= fetch(`${base}data/countries-meta.json`).then((r) => {
    if (!r.ok) throw new Error(`Could not load country list (${r.status})`)
    return r.json()
  })
  return cache
}
