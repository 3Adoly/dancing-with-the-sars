# Dancing with the SARs: frontend

NASA Space Apps 2026 entry for the "Dancing with the SARs" challenge. A 3D globe of
natural-hazard incidents that drills down into real radar (SAR) scenes, plus a
Stories mode for NISAR change detection.

## Run it

```bash
npm install
npm run dev          # http://localhost:5173
```

By default the dev server talks to the **team backend** (`VITE_PROXY_TARGET` in
`.env.development`, contract in `../api/openapi.yaml`). Vite proxies `/api`, `/media` and
`/healthz`, so there are no CORS or mixed-content problems in dev.

Endpoints the backend still answers with `501` (grid, incidents, incident
detail, search) **fall back to the in-browser mock automatically**, and the UI
shows a "sample data" notice. When the backend implements them, the fallback
stops on its own; no code change needed.

| Mode | How |
| --- | --- |
| Team backend + mock fallback (default) | nothing to set |
| Offline, mock only | `VITE_USE_MOCKS=true` in `.env.local` |
| Backend only, show its errors | `VITE_MOCK_FALLBACK=false` |
| Production build against a backend | `VITE_API_BASE=https://…` |

All requests go through `src/api/client.ts`; wire types in `src/api/types.ts`
mirror the OpenAPI spec exactly.

### Notes for the backend team

- `/media/stories/*` has no `Access-Control-Allow-Origin` header. MapLibre loads
  story images through WebGL, which needs CORS, so a frontend on another origin
  can't show them. Fine in dev (proxy); needed for the deployed site.
- The server is plain HTTP. A frontend served over HTTPS (Vercel, Netlify,
  GitHub Pages) can't call it (mixed content). We need HTTPS, or a rewrite
  proxy on the frontend host.
- `/stats/timeseries` has no per-type split, so the country panel makes one
  `/stats/choropleth` call per type (cached) to show the breakdown. An optional
  `byType` field would save those calls.
- `/stats/grid` cells: the frontend treats `lat`/`lon` as the cell center.
- Type colors from `/incident-types` are tuned for light maps; the dark globe
  uses lighter versions of the same hues (`src/lib/theme.ts`).

## What's where

| Path | What it does |
| --- | --- |
| `src/map/MapView.tsx` | Globe. z<3 countries colored by count, z3–5.5 3D density columns, z≥5.5 incident points; SAR footprints and NISAR overlay |
| `src/map/style.ts` | Basemap on OpenFreeMap vector tiles, restyled in the app palette |
| `src/components/TimelineBar.tsx` | Year range, play, incidents per year, radar-era strip |
| `src/components/panel/*` | Country summary and incident detail with "Radar views" (before/after slider) |
| `src/components/stories/*` | Stories mode: swipe compare, change layer, interferogram, time series |
| `src/api/` | Contract types, fetch client (mock/real switch), TanStack Query hooks, ASF search |
| `src/mocks/` | Mock API in the backend's exact shapes: curated real events, sample stories, reference data copied from the live server |
| `src/store/app-store.ts` | Zustand store, mirrored to the URL so views are shareable |
| `public/data/` | Natural Earth 1:50m countries (key `ADM0_A3`) |
| `public/media/stories/` | **Placeholder** story imagery (replace with real NISAR layers) |

## Data scripts

```bash
npm run data:countries -- <ne_50m_admin_0_countries.geojson> public/data
npm run data:mock        # regenerate src/mocks/data/incidents.json
npm run data:stories     # regenerate placeholder story images (offline mode)
```

## Before the demo

- Real stories come from the backend (`/api/v1/stories`); the sample stories in `src/mocks/stories.ts` only appear offline.
- Mock incidents are synthetic, except the curated real events in `src/mocks/curated.ts`; mock ids start at 9,000,000 so they never collide with real ones.

## Credits

Country outlines: Natural Earth. Basemap: OpenFreeMap / OpenMapTiles / OpenStreetMap.
Radar scenes: NASA ASF DAAC (NISAR: NASA-ISRO; Sentinel-1: Copernicus/ESA; ALOS: JAXA; Seasat: NASA).
