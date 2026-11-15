// Tells viewers which parts of the screen are sample data while backend
// endpoints are still being built (they answer 501 and the mock fills in).
import { Info } from 'lucide-react'
import { USE_MOCKS, useDataSource } from '@/api/client'

const LABEL: Record<string, string> = {
  '/stats/grid': 'density columns',
  '/incidents': 'incident points',
  '/incidents/:id': 'incident details',
  '/incidents/:id/sar': 'radar scenes for sample incidents',
  '/search': 'search',
}

export function DataSourceNotice() {
  const mocked = useDataSource((s) => s.mocked)
  if (USE_MOCKS) {
    return <Notice text="Offline mode: everything on screen is sample data." />
  }
  const parts = [...new Set(mocked.map((k) => LABEL[k]).filter(Boolean))]
  if (parts.length === 0) return null
  return <Notice text={`Sample data for ${parts.join(', ')} until the backend's incident database is ready.`} />
}

function Notice({ text }: { text: string }) {
  return (
    <p className="glass pointer-events-auto flex max-w-md items-start gap-2 rounded-lg px-3 py-2 text-xs leading-snug text-muted-foreground" role="status">
      <Info className="mt-0.5 size-3.5 shrink-0 text-signal" aria-hidden />
      {text}
    </p>
  )
}
