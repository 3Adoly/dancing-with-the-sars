// Color tokens shared by the map (WebGL can't read CSS variables) and the UI.
// Keep in sync with :root in index.css.
export const theme = {
  space: '#0E1B2E',
  ocean: '#11233A',
  land: '#1C2E45',
  landHi: '#24395499',
  line: '#2C4566',
  text: '#E8EDF4',
  muted: '#93A4BC',
  accent: '#F5B66B',
  select: '#FFF4D6',
  increase: '#E5484D',
  decrease: '#3E8FD6',
} as const

/** Magma-like sequential ramp, dark → bright = few → many (reads on a dark globe). */
export const COUNT_RAMP = ['#5A2E99', '#8A3A9C', '#BE4581', '#E35E63', '#F9915A', '#FEC98B'] as const

/** Single-type ramp: from the land color toward the type's own hue. */
export function typeRamp(hex: string, steps = COUNT_RAMP.length): string[] {
  const [r, g, b] = hexToRgb(hex)
  const [r0, g0, b0] = hexToRgb('#2A2A55')
  return Array.from({ length: steps }, (_, i) => {
    const t = 0.25 + (0.75 * i) / (steps - 1)
    const mix = (a: number, c: number) => Math.round(a + (c - a) * t)
    // Lift the last steps toward white so the top bucket glows.
    const lift = i >= steps - 2 ? (i - (steps - 3)) * 0.18 : 0
    const up = (v: number) => Math.round(v + (255 - v) * lift)
    return rgbToHex(up(mix(r0, r)), up(mix(g0, g)), up(mix(b0, b)))
  })
}

function hexToRgb(hex: string): [number, number, number] {
  const n = parseInt(hex.replace('#', '').slice(0, 6), 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}
function rgbToHex(r: number, g: number, b: number) {
  return '#' + [r, g, b].map((v) => Math.max(0, Math.min(255, v)).toString(16).padStart(2, '0')).join('')
}

/**
 * Display colors per incident type for the dark globe. The backend's colors
 * (/incident-types) are ColorBrewer tones meant for light maps; several
 * (volcano #7a0177, storm #6a51a3, landslide #8c510a) vanish on navy. Unknown
 * ids fall back to the backend color.
 */
export const TYPE_DISPLAY_COLOR: Record<string, string> = {
  earthquake: '#F2B84B',
  volcano: '#F2674B',
  tsunami: '#3FC1C9',
  wildfire: '#FF8A3D',
  fire_activity: '#FFB07A',
  flood: '#4BA3F2',
  landslide: '#C9A66B',
  storm: '#9B8CF2',
  drought: '#D9C76A',
  other: '#9AA7B8',
}

export function typeColor(t: { id: string; color?: string } | string | undefined): string {
  if (!t) return '#9AA7B8'
  if (typeof t === 'string') return TYPE_DISPLAY_COLOR[t] ?? '#9AA7B8'
  return TYPE_DISPLAY_COLOR[t.id] ?? t.color ?? '#9AA7B8'
}
