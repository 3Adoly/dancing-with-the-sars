// Single entry point for the team backend (see api/openapi.yaml).
//
// Modes (env, see .env):
//   default              real backend. Endpoints that still answer 501 (not_implemented)
//                        fall back to the in-browser mock, and the UI says so.
//   VITE_USE_MOCKS=true  mock only (offline work)
//   VITE_MOCK_FALLBACK=false  never fall back; show the backend's errors instead
//
// In dev, VITE_API_BASE is empty and Vite proxies /api, /media and /healthz to
// VITE_PROXY_TARGET (the team server), so there are no CORS or mixed-content issues.
import { create } from 'zustand'
import type { ApiErrorBody } from './types'

export const USE_MOCKS = import.meta.env.VITE_USE_MOCKS === 'true'
const MOCK_FALLBACK = import.meta.env.VITE_MOCK_FALLBACK !== 'false'
const API_BASE = (import.meta.env.VITE_API_BASE ?? '').replace(/\/$/, '')

/** Mock incident ids live far above real ones so they can never collide. */
export const MOCK_ID_BASE = 9_000_000
export const isMockIncidentId = (id: string | number) => Number(id) >= MOCK_ID_BASE

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

/** Which endpoints are currently served by the mock (shown as a notice in the UI). */
export const useDataSource = create<{ mocked: string[]; add: (k: string) => void }>((set) => ({
  mocked: [],
  add: (k) => set((s) => (s.mocked.includes(k) ? s : { mocked: [...s.mocked, k] })),
}))

type Params = Record<string, string | number | boolean | null | undefined>

function clean(params: Params): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') out[k] = String(v)
  return out
}

const routeKey = (path: string) => path.replace(/\/\d+/g, '/:id')
const MOCKABLE = new Set(['/stats/grid', '/incidents', '/incidents/:id', '/incidents/:id/sar', '/search'])
const knownMissing = new Set<string>()

async function viaMock<T>(path: string, p: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const { mockGet } = await import('@/mocks/handler')
  try {
    return await mockGet<T>(path, p, signal)
  } catch (e) {
    const err = e as Error & { status?: number; code?: string }
    if (err.name === 'AbortError') throw err
    throw new ApiError(err.status ?? 500, err.code ?? 'internal', err.message)
  }
}

async function viaBackend<T>(path: string, p: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const qs = new URLSearchParams(p).toString()
  const res = await fetch(`${API_BASE}/api/v1${path}${qs ? `?${qs}` : ''}`, { signal })
  if (!res.ok) {
    let code = 'http_error'
    let message = `Request failed (${res.status})`
    try {
      const body = (await res.json()) as ApiErrorBody
      code = body.error?.code ?? code
      message = body.error?.message ?? message
    } catch { /* not JSON */ }
    throw new ApiError(res.status, code, message)
  }
  return res.json() as Promise<T>
}

export async function apiGet<T>(path: string, params: Params = {}, signal?: AbortSignal): Promise<T> {
  const p = clean(params)
  if (USE_MOCKS) return viaMock<T>(path, p, signal)

  const key = routeKey(path)
  const idMatch = path.match(/^\/incidents\/(\d+)/)
  // Incidents that came from the mock must keep using the mock (their ids don't exist upstream).
  if (idMatch && isMockIncidentId(idMatch[1])) {
    useDataSource.getState().add(key)
    return viaMock<T>(path, p, signal)
  }
  if (MOCK_FALLBACK && knownMissing.has(key)) return viaMock<T>(path, p, signal)

  try {
    return await viaBackend<T>(path, p, signal)
  } catch (e) {
    if (MOCK_FALLBACK && e instanceof ApiError && e.status === 501 && MOCKABLE.has(key)) {
      knownMissing.add(key)
      useDataSource.getState().add(key)
      return viaMock<T>(path, p, signal)
    }
    throw e
  }
}

/** Story images and other static media. */
export function mediaUrl(path: string) {
  if (/^https?:\/\//.test(path)) return path
  return USE_MOCKS ? `${import.meta.env.BASE_URL.replace(/\/$/, '')}${path}` : `${API_BASE}${path}`
}
