// Basemap built on OpenFreeMap vector tiles (OpenMapTiles schema, free, no key),
// restyled in the app's palette. Our data layers are inserted before the first
// label layer (ids starting with "label-").
import type { StyleSpecification } from 'maplibre-gl'
import { theme } from '@/lib/theme'

const FONT = ['Noto Sans Regular']

export function baseStyle(projection: 'globe' | 'mercator' = 'globe'): StyleSpecification {
  return {
    version: 8,
    projection: { type: projection },
    glyphs: 'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf',
    sky: {
      'sky-color': theme.space,
      'horizon-color': '#284A7A',
      'fog-color': theme.space,
      'sky-horizon-blend': 0.6,
      'horizon-fog-blend': 0.5,
      'fog-ground-blend': 0.6,
      'atmosphere-blend': ['interpolate', ['linear'], ['zoom'], 0, 1, 5, 1, 7, 0],
    },
    sources: {
      omt: {
        type: 'vector',
        url: 'https://tiles.openfreemap.org/planet',
        attribution:
          '<a href="https://openfreemap.org" target="_blank">OpenFreeMap</a> © <a href="https://www.openmaptiles.org/" target="_blank">OpenMapTiles</a> Data from <a href="https://www.openstreetmap.org/copyright" target="_blank">OpenStreetMap</a>',
      },
    },
    layers: [
      { id: 'land', type: 'background', paint: { 'background-color': theme.land } },
      { id: 'water', type: 'fill', source: 'omt', 'source-layer': 'water', paint: { 'fill-color': theme.ocean } },
      {
        id: 'ice', type: 'fill', source: 'omt', 'source-layer': 'landcover',
        filter: ['in', ['get', 'subclass'], ['literal', ['glacier', 'ice_shelf']]],
        paint: { 'fill-color': '#2A4060', 'fill-opacity': 0.7 },
      },
      {
        id: 'residential', type: 'fill', source: 'omt', 'source-layer': 'landuse', minzoom: 8,
        filter: ['in', ['get', 'class'], ['literal', ['residential', 'suburb', 'neighbourhood', 'industrial']]],
        paint: { 'fill-color': '#22385A', 'fill-opacity': 0.8 },
      },
      {
        id: 'waterway', type: 'line', source: 'omt', 'source-layer': 'waterway', minzoom: 7,
        paint: { 'line-color': theme.ocean, 'line-width': ['interpolate', ['linear'], ['zoom'], 7, 0.5, 12, 2] },
      },
      {
        id: 'roads', type: 'line', source: 'omt', 'source-layer': 'transportation', minzoom: 6,
        filter: ['in', ['get', 'class'], ['literal', ['motorway', 'trunk', 'primary', 'secondary']]],
        paint: {
          'line-color': '#2F4A6E',
          'line-width': ['interpolate', ['linear'], ['zoom'], 6, 0.4, 10, 1.2, 14, 3],
        },
      },
      {
        id: 'boundary-state', type: 'line', source: 'omt', 'source-layer': 'boundary', minzoom: 5,
        filter: ['==', ['get', 'admin_level'], 4],
        paint: { 'line-color': '#34507A', 'line-width': 0.6, 'line-dasharray': [3, 2] },
      },
      {
        id: 'label-city', type: 'symbol', source: 'omt', 'source-layer': 'place', minzoom: 4,
        filter: ['in', ['get', 'class'], ['literal', ['city', 'town']]],
        layout: {
          'text-field': ['coalesce', ['get', 'name:en'], ['get', 'name']],
          'text-font': FONT,
          'text-size': ['interpolate', ['linear'], ['zoom'], 4, 10, 10, 14],
          'symbol-sort-key': ['coalesce', ['get', 'rank'], 99],
        },
        paint: { 'text-color': '#B8C6DA', 'text-halo-color': theme.space, 'text-halo-width': 1.4 },
      },
      {
        id: 'label-country', type: 'symbol', source: 'omt', 'source-layer': 'place', minzoom: 2, maxzoom: 7,
        filter: ['==', ['get', 'class'], 'country'],
        layout: {
          'text-field': ['coalesce', ['get', 'name:en'], ['get', 'name']],
          'text-font': FONT,
          'text-size': ['interpolate', ['linear'], ['zoom'], 2, 10, 6, 14],
          'text-max-width': 7,
        },
        paint: { 'text-color': '#D5DEEA', 'text-halo-color': theme.space, 'text-halo-width': 1.4, 'text-opacity': 0.85 },
      },
    ],
  }
}

export const FIRST_LABEL_LAYER = 'label-city'
