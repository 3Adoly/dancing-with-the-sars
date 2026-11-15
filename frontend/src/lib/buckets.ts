import { bucketLabel } from './format'

/** Colors for each bucket (b[i], b[i+1]], spread across the ramp when there are fewer buckets than colors. */
export function bucketColors(breaks: number[], ramp: readonly string[]): string[] {
  const n = Math.max(1, breaks.length - 1)
  if (n === 1) return [ramp[ramp.length - 1]]
  return Array.from({ length: n }, (_, i) => ramp[Math.round((i * (ramp.length - 1)) / (n - 1))])
}

export function bucketLegend(breaks: number[], ramp: readonly string[]) {
  const colors = bucketColors(breaks, ramp)
  return colors.map((color, i) => ({ color, label: bucketLabel(breaks[i], breaks[i + 1]) }))
}

/** MapLibre expression: feature-state count → bucket color, 0/missing → noData. */
export function countFillExpression(breaks: number[], ramp: readonly string[], noData: string) {
  const colors = bucketColors(breaks, ramp)
  const count = ['coalesce', ['feature-state', 'count'], 0]
  const step: unknown[] = ['step', count, colors[0]]
  for (let i = 1; i < colors.length; i++) step.push(breaks[i] + 1, colors[i])
  return ['case', ['>', count, 0], step, noData]
}
