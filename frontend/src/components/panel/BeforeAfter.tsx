// Two images stacked with a draggable divider. Works with mouse, touch and keyboard
// (the divider is a real range input, so arrow keys move it).
import { useState } from 'react'

interface Side {
  src: string
  label: string
}

export function BeforeAfter({ before, after, aspect = 4 / 3, note }: { before: Side; after: Side; aspect?: number; note?: string }) {
  const [pos, setPos] = useState(50)
  return (
    <figure className="m-0">
      <div className="relative w-full overflow-hidden rounded-md bg-[#0A1422]" style={{ aspectRatio: aspect }}>
        <img src={after.src} alt={after.label} className="absolute inset-0 h-full w-full object-cover" loading="lazy" draggable={false} />
        <img
          src={before.src}
          alt={before.label}
          className="absolute inset-0 h-full w-full object-cover"
          style={{ clipPath: `inset(0 ${100 - pos}% 0 0)` }}
          loading="lazy"
          draggable={false}
        />
        <div className="pointer-events-none absolute inset-y-0 w-0.5 bg-select shadow-[0_0_0_1px_rgba(0,0,0,0.35)]" style={{ left: `${pos}%` }}>
          <div className="absolute top-1/2 left-1/2 flex size-7 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-select text-[11px] font-semibold text-[#1A1206]">
            ⇆
          </div>
        </div>
        <span className="type-label absolute top-2 left-2 rounded bg-black/55 px-1.5 py-0.5 text-[11px] text-white">{before.label}</span>
        <span className="type-label absolute top-2 right-2 rounded bg-black/55 px-1.5 py-0.5 text-[11px] text-white">{after.label}</span>
        <input
          type="range"
          min={0}
          max={100}
          value={pos}
          onChange={(e) => setPos(Number(e.target.value))}
          aria-label="Drag to compare before and after"
          className="absolute inset-0 h-full w-full cursor-ew-resize opacity-0"
        />
      </div>
      {note && <figcaption className="mt-1.5 text-[11px] leading-snug text-muted-foreground">{note}</figcaption>}
    </figure>
  )
}
