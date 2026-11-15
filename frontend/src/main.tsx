import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { setWorkerUrl } from 'maplibre-gl'
import mapWorkerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?url'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { toast } from 'sonner'
import App from './App.tsx'
import './index.css'

// MapLibre builds its worker file name at runtime, so Vite can't see it and
// leaves it out of the production build. Import it explicitly so it ships.
setWorkerUrl(mapWorkerUrl)

const onError = (error: Error) => {
  if (error.name === 'AbortError') return
  // Keep showing the last good data; just tell the user what failed.
  toast.error(error.message || 'Something went wrong loading data.', { id: error.message })
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: { refetchOnWindowFocus: false, retry: 1, staleTime: 60_000 },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)
