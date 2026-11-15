// Lets UI outside the map (panels, buttons) move the camera without prop drilling.
import type * as maplibregl from 'maplibre-gl'

let current: maplibregl.Map | null = null
export const setMap = (m: maplibregl.Map | null) => { current = m }
export const getMap = () => current
