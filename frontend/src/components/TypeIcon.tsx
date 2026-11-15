import {
  Activity,
  CircleDot,
  Droplets,
  Flame,
  Mountain,
  MountainSnow,
  Satellite,
  Sun,
  Tornado,
  Waves,
  type LucideProps,
} from 'lucide-react'

// Keyed by incident type id; the backend's `icon` names match these ids.
const ICONS: Record<string, React.ComponentType<LucideProps>> = {
  earthquake: Activity,
  volcano: Mountain,
  tsunami: Waves,
  wildfire: Flame,
  fire_activity: Satellite,
  fire: Satellite,
  flood: Droplets,
  landslide: MountainSnow,
  storm: Tornado,
  drought: Sun,
  other: CircleDot,
}

export function TypeIcon({ type, ...props }: { type: string } & LucideProps) {
  const Icon = ICONS[type] ?? CircleDot
  return <Icon aria-hidden {...props} />
}
