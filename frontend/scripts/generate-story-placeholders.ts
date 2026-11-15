// Generates radar-looking PLACEHOLDER images for the sample stories, so Stories
// mode can be built and demoed before the data team's real NISAR/SAOCOM layers
// exist. Not real data. Each story's bbox comes from src/mocks/stories.ts.
//
// Usage: npx tsx scripts/generate-story-placeholders.ts
// Writes public/media/stories/<story-id>/{before,after,change,interferogram,frame-N,thumb}.png
import fs from 'node:fs'
import path from 'node:path'
import zlib from 'node:zlib'
import { STORIES } from '../src/mocks/stories'

const root = path.resolve(import.meta.dirname, '..')
const OUT = path.join(root, 'public/media/stories')

// --- tiny PNG encoder (RGBA8) -------------------------------------------------
function png(width: number, height: number, rgba: Uint8ClampedArray): Buffer {
  const chunk = (type: string, data: Buffer) => {
    const len = Buffer.alloc(4)
    len.writeUInt32BE(data.length)
    const td = Buffer.concat([Buffer.from(type, 'ascii'), data])
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(zlib.crc32(td) >>> 0)
    return Buffer.concat([len, td, crc])
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(width, 0)
  ihdr.writeUInt32BE(height, 4)
  ihdr[8] = 8 // bit depth
  ihdr[9] = 6 // RGBA
  const raw = Buffer.alloc((width * 4 + 1) * height)
  for (let y = 0; y < height; y++) {
    raw[y * (width * 4 + 1)] = 0
    Buffer.from(rgba.buffer, y * width * 4, width * 4).copy(raw, y * (width * 4 + 1) + 1)
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', zlib.deflateSync(raw, { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

// --- noise ----------------------------------------------------------------------
let seed = 7
function rand() {
  seed |= 0
  seed = (seed + 0x6d2b79f5) | 0
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296
}
const PERM = Array.from({ length: 512 }, () => rand())
function hash(x: number, y: number) {
  return PERM[((x * 73856093) ^ (y * 19349663)) & 511]
}
function valueNoise(x: number, y: number) {
  const xi = Math.floor(x), yi = Math.floor(y)
  const xf = x - xi, yf = y - yi
  const s = (t: number) => t * t * (3 - 2 * t)
  const a = hash(xi, yi), b = hash(xi + 1, yi), c = hash(xi, yi + 1), d = hash(xi + 1, yi + 1)
  return a + (b - a) * s(xf) + (c - a) * s(yf) + (a - b - c + d) * s(xf) * s(yf)
}
function fbm(x: number, y: number, oct = 5) {
  let v = 0, amp = 0.5, f = 1
  for (let i = 0; i < oct; i++) { v += amp * valueNoise(x * f, y * f); amp *= 0.5; f *= 2 }
  return v / (1 - 0.5 ** oct)
}
/** 4-look speckle: mean of four exponential draws (mean 1). */
function speckle() {
  let s = 0
  for (let i = 0; i < 4; i++) s -= Math.log(1 - rand())
  return s / 4
}

// --- image helpers -----------------------------------------------------------
type Field = (u: number, v: number) => number // u,v in [0,1] → linear backscatter (≈0–1)
type Img = { w: number; h: number; data: Uint8ClampedArray }

function sizeFor(bbox: number[]) {
  const [w, s, e, n] = bbox
  const lat = ((s + n) / 2) * (Math.PI / 180)
  const aspect = ((e - w) * Math.cos(lat)) / (n - s)
  const W = 900
  return { w: W, h: Math.round(W / aspect) }
}

function renderGray(w: number, h: number, f: Field, withSpeckle = true): { img: Img; sigma: Float32Array } {
  const data = new Uint8ClampedArray(w * h * 4)
  const sigma = new Float32Array(w * h)
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
    const s = Math.max(1e-4, f(x / w, y / h)) * (withSpeckle ? speckle() : 1)
    sigma[y * w + x] = s
    const db = 10 * Math.log10(s)
    const g = Math.round(Math.max(0, Math.min(1, (db + 22) / 22)) * 255) // −22..0 dB → 0..255
    const i = (y * w + x) * 4
    data[i] = data[i + 1] = data[i + 2] = g
    data[i + 3] = 255
  }
  return { img: { w, h, data }, sigma }
}

/** Log-ratio change: blue = darker after, red = brighter after, transparent = little change. */
function changeImage(w: number, h: number, before: Float32Array, after: Float32Array): Img {
  const data = new Uint8ClampedArray(w * h * 4)
  const r = 2 // 5×5 box filter to suppress speckle
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
    let sb = 0, sa = 0, n = 0
    for (let dy = -r; dy <= r; dy++) for (let dx = -r; dx <= r; dx++) {
      const xx = Math.min(w - 1, Math.max(0, x + dx)), yy = Math.min(h - 1, Math.max(0, y + dy))
      sb += before[yy * w + xx]; sa += after[yy * w + xx]; n++
    }
    const lr = 10 * Math.log10(sa / n / (sb / n))
    const i = (y * w + x) * 4
    const mag = Math.min(1, Math.max(0, (Math.abs(lr) - 2) / 5))
    if (lr < 0) { data[i] = 62; data[i + 1] = 143; data[i + 2] = 214 } else { data[i] = 229; data[i + 1] = 72; data[i + 2] = 77 }
    data[i + 3] = Math.round(mag * 220)
  }
  return { w, h, data }
}

function hsl(hh: number, s: number, l: number): [number, number, number] {
  const k = (n: number) => (n + hh * 12) % 12
  const a = s * Math.min(l, 1 - l)
  const f = (n: number) => l - a * Math.max(-1, Math.min(k(n) - 3, 9 - k(n), 1))
  return [f(0) * 255, f(8) * 255, f(4) * 255]
}

function thumb(src: Img, tw = 360, th = 220): Img {
  const data = new Uint8ClampedArray(tw * th * 4)
  const scale = Math.max(tw / src.w, th / src.h)
  const ox = (src.w * scale - tw) / 2, oy = (src.h * scale - th) / 2
  for (let y = 0; y < th; y++) for (let x = 0; x < tw; x++) {
    const sx = Math.min(src.w - 1, Math.floor((x + ox) / scale)), sy = Math.min(src.h - 1, Math.floor((y + oy) / scale))
    const si = (sy * src.w + sx) * 4, di = (y * tw + x) * 4
    const a = src.data[si + 3] / 255
    for (let c = 0; c < 3; c++) data[di + c] = src.data[si + c] * a + 28 * (1 - a)
    data[di + 3] = 255
  }
  return { w: tw, h: th, data }
}

function save(storyId: string, name: string, img: Img) {
  const dir = path.join(OUT, storyId)
  fs.mkdirSync(dir, { recursive: true })
  fs.writeFileSync(path.join(dir, name), png(img.w, img.h, img.data))
}

// --- scenes ---------------------------------------------------------------------
const dist = (u: number, v: number, cu: number, cv: number) => Math.hypot(u - cu, v - cv)
const riverU = (v: number) => 0.45 + 0.12 * Math.sin(v * 7) + 0.05 * Math.sin(v * 19)

const scenes: Record<string, (w: number, h: number) => void> = {
  'melissa-flood': (w, h) => {
    const sea = (u: number, v: number) => v > 0.86 + 0.04 * Math.sin(u * 9) + 0.03 * fbm(u * 6, 1)
    const land: Field = (u, v) => {
      if (sea(u, v)) return 0.008
      const d = Math.abs(u - riverU(v))
      if (d < 0.008) return 0.01
      const town = dist(u, v, 0.55, 0.78) < 0.05 ? 0.6 : 0
      const fields = 0.06 + 0.08 * fbm(u * 18, v * 18) + 0.05 * (Math.floor(fbm(u * 8, v * 8, 2) * 6) % 2)
      return Math.max(town, fields)
    }
    const flooded = (u: number, v: number) => {
      const d = Math.abs(u - riverU(v))
      return !sea(u, v) && d < 0.11 + 0.08 * fbm(u * 5, v * 5) && fbm(u * 3 + 9, v * 3) < 0.62 && v > 0.25
    }
    const b = renderGray(w, h, land)
    const a = renderGray(w, h, (u, v) => (flooded(u, v) ? (dist(u, v, 0.55, 0.78) < 0.05 ? 0.35 : 0.006) : land(u, v)))
    save('melissa-flood', 'before.png', b.img)
    save('melissa-flood', 'after.png', a.img)
    save('melissa-flood', 'change.png', changeImage(w, h, b.sigma, a.sigma))
    save('melissa-flood', 'thumb.png', thumb(a.img))
  },

  'palisades-burn-scar': (w, h) => {
    const ocean = (u: number, v: number) => v > 0.8 - 0.25 * u + 0.02 * Math.sin(u * 30)
    const urban = (u: number, v: number) => !ocean(u, v) && v > 0.52 - 0.2 * u && fbm(u * 4, v * 4) > 0.42
    const burn = (u: number, v: number) => !ocean(u, v) && fbm(u * 3 + 4, v * 3 + 2) + (0.6 - v) * 0.5 > 0.62
    const base: Field = (u, v) => {
      if (ocean(u, v)) return 0.006
      if (urban(u, v)) {
        const street = (Math.floor(u * 120) % 4 === 0 || Math.floor(v * 90) % 4 === 0)
        return street ? 0.04 : 0.45
      }
      return 0.09 + 0.07 * fbm(u * 25, v * 25) // chaparral / forest canopy (VH volume scattering)
    }
    const b = renderGray(w, h, base)
    const a = renderGray(w, h, (u, v) => {
      if (!burn(u, v)) return base(u, v)
      if (urban(u, v)) return rand() < 0.5 ? 0.06 : 0.25 // destroyed blocks lose their regular pattern
      return 0.022 + 0.015 * fbm(u * 25, v * 25)
    })
    save('palisades-burn-scar', 'before.png', b.img)
    save('palisades-burn-scar', 'after.png', a.img)
    save('palisades-burn-scar', 'change.png', changeImage(w, h, b.sigma, a.sigma))
    save('palisades-burn-scar', 'thumb.png', thumb(a.img))
  },

  'noto-uplift': (w, h) => {
    const land = (u: number, v: number) => fbm(u * 2.2 + 1, v * 2.2 + 3, 4) + (1 - Math.abs(u - 0.5) * 1.6) * 0.35 - v * 0.08 > 0.62
    const field: Field = (u, v) => (land(u, v) ? 0.08 + 0.12 * fbm(u * 20, v * 20) : 0.007)
    const b = renderGray(w, h, field)
    const a = renderGray(w, h, field)
    save('noto-uplift', 'before.png', b.img)
    save('noto-uplift', 'after.png', a.img)
    // Interferogram: uplift dome near the north coast; ~14 fringes at the peak.
    const data = new Uint8ClampedArray(w * h * 4)
    for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
      const u = x / w, v = y / h
      const i = (y * w + x) * 4
      if (!land(u, v)) { data[i + 3] = 0; continue }
      const du = (u - 0.48) / 0.32, dv = (v - 0.28) / 0.2
      const disp = 14 * Math.exp(-(du * du + dv * dv)) + 0.15 * fbm(u * 30, v * 30) // in fringes
      const phase = disp - Math.floor(disp)
      const [r, g, bb] = hsl(phase, 0.8, 0.55)
      data[i] = r; data[i + 1] = g; data[i + 2] = bb; data[i + 3] = 205
    }
    const ifg = { w, h, data }
    save('noto-uplift', 'interferogram.png', ifg)
    save('noto-uplift', 'thumb.png', thumb(ifg))
  },

  'mexico-city-subsidence': (w, h) => {
    const ramp = (t: number): [number, number, number] => {
      // 0 → blue, 0.5 → amber, 1 → red
      const stops: [number, number, number][] = [[46, 94, 158], [242, 184, 75], [229, 72, 77]]
      const k = Math.min(1.999, t * 2), i = Math.floor(k), f = k - i
      return [0, 1, 2].map((c) => stops[i][c] + (stops[i + 1][c] - stops[i][c]) * f) as [number, number, number]
    }
    let last: Img | null = null
    for (let n = 1; n <= 6; n++) {
      const data = new Uint8ClampedArray(w * h * 4)
      for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
        const u = x / w, v = y / h
        const du = (u - 0.6) / 0.28, dv = (v - 0.5) / 0.22
        const bowl = Math.exp(-(du * du + dv * dv)) * (0.85 + 0.3 * fbm(u * 6, v * 6))
        const depth = Math.min(1, bowl * (n / 6)) // 1 = −30 cm
        const i = (y * w + x) * 4
        const [r, g, b] = ramp(depth)
        data[i] = r; data[i + 1] = g; data[i + 2] = b
        data[i + 3] = Math.round(Math.min(1, depth * 3) * 200)
      }
      last = { w, h, data }
      save('mexico-city-subsidence', `frame-${n}.png`, last)
    }
    save('mexico-city-subsidence', 'thumb.png', thumb(last!))
  },

  'parana-two-radars': (w, h) => {
    const channel = (u: number, v: number) => Math.abs(fbm(u * 3, v * 3, 3) - 0.5) < 0.018 || Math.abs(fbm(u * 5 + 7, v * 5, 3) - 0.5) < 0.01
    const marsh = (u: number, v: number) => fbm(u * 9 + 2, v * 9) // reed density
    const wet = (u: number, v: number, extra: number) => fbm(u * 2.5 + 11, v * 2.5 + 5) < 0.48 + extra
    const field = (extra: number, gain: number): Field => (u, v) => {
      if (channel(u, v)) return 0.006
      const m = marsh(u, v)
      return gain * (wet(u, v, extra) ? 0.25 + 0.3 * m : 0.05 + 0.08 * m) // flooded reeds: double bounce
    }
    const saocom = renderGray(w, h, field(0, 1))
    const nisar = renderGray(w, h, field(0.02, 0.85))
    const saocomLater = renderGray(w, h, field(0.09, 1))
    save('parana-two-radars', 'before.png', saocom.img)
    save('parana-two-radars', 'after.png', nisar.img)
    save('parana-two-radars', 'change.png', changeImage(w, h, saocom.sigma, saocomLater.sigma))
    save('parana-two-radars', 'thumb.png', thumb(nisar.img))
  },
}

for (const story of STORIES) {
  const make = scenes[story.id]
  if (!make) { console.warn(`no placeholder recipe for ${story.id}`); continue }
  const { w, h } = sizeFor(story.bbox)
  make(w, h)
  console.log(`${story.id}: ${w}×${h}`)
}
