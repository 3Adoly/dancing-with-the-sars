const nf = new Intl.NumberFormat('en-US')
const compact = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 })

export const fmtInt = (n: number) => nf.format(n)
export const fmtCompact = (n: number) => (n < 10_000 ? nf.format(n) : compact.format(n))

export function fmtDate(iso: string) {
  const d = new Date(iso.length <= 10 ? `${iso}T00:00:00Z` : iso)
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' })
}

export function fmtDays(days: number) {
  if (days === 0) return 'same day'
  const abs = Math.abs(days)
  const unit = abs >= 60 ? `${Math.round(abs / 30)} months` : `${abs} day${abs === 1 ? '' : 's'}`
  return days < 0 ? `${unit} before` : `${unit} after`
}

/** Legend label for bucket (prev, edge]. */
export function bucketLabel(prev: number, edge: number) {
  const lo = prev + 1
  return lo >= edge ? fmtCompact(edge) : `${fmtCompact(lo)}–${fmtCompact(edge)}`
}
