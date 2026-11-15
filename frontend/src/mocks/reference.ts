// Reference data served by the mock API. Copied from the live team server
// (2026-11-15) so offline mode matches the real backend.
import type { Coverage, IncidentSource, IncidentTypeId, SARMission, TypeInfo } from "@/api/types"

export const INCIDENT_TYPES: TypeInfo[] = [
  {
    id: "earthquake",
    label: "Earthquakes",
    unit: "incidents",
    color: "#d7301f",
    icon: "earthquake",
    fromYear: 1900,
    toYear: 2026,
    clickable: true
  },
  {
    id: "volcano",
    label: "Volcanoes",
    unit: "incidents",
    color: "#7a0177",
    icon: "volcano",
    fromYear: 1900,
    toYear: 2026,
    clickable: true
  },
  {
    id: "tsunami",
    label: "Tsunamis",
    unit: "incidents",
    color: "#0570b0",
    icon: "tsunami",
    fromYear: 1900,
    toYear: 2026,
    clickable: true
  },
  {
    id: "wildfire",
    label: "Wildfires (events)",
    unit: "incidents",
    color: "#e31a1c",
    icon: "wildfire",
    fromYear: 2000,
    toYear: 2026,
    clickable: true
  },
  {
    id: "fire_activity",
    label: "Fire activity (satellite)",
    unit: "detections",
    color: "#fd8d3c",
    icon: "fire",
    fromYear: 2000,
    toYear: 2026,
    clickable: false
  },
  {
    id: "flood",
    label: "Floods",
    unit: "incidents",
    color: "#2171b5",
    icon: "flood",
    fromYear: 1985,
    toYear: 2026,
    clickable: true
  },
  {
    id: "landslide",
    label: "Landslides",
    unit: "incidents",
    color: "#8c510a",
    icon: "landslide",
    fromYear: 1988,
    toYear: 2026,
    clickable: true
  },
  {
    id: "storm",
    label: "Storms / cyclones",
    unit: "incidents",
    color: "#6a51a3",
    icon: "storm",
    fromYear: 1900,
    toYear: 2026,
    clickable: true
  },
  {
    id: "drought",
    label: "Droughts",
    unit: "incidents",
    color: "#bf812d",
    icon: "drought",
    fromYear: 2000,
    toYear: 2026,
    clickable: true
  },
  {
    id: "other",
    label: "Other",
    unit: "incidents",
    color: "#737373",
    icon: "other",
    fromYear: 2000,
    toYear: 2026,
    clickable: true
  }
]

export const COVERAGE: Coverage[] = [
  {
    type: "earthquake",
    fromYear: 1900,
    toYear: 2026,
    sources: [
      "usgs",
      "noaa_eq"
    ],
    note: "USGS M≥4.5; catalog incomplete before ~1973"
  },
  {
    type: "volcano",
    fromYear: 1900,
    toYear: 2026,
    sources: [
      "noaa_vo"
    ],
    note: "NOAA significant eruptions"
  },
  {
    type: "tsunami",
    fromYear: 1900,
    toYear: 2026,
    sources: [
      "noaa_ts"
    ],
    note: "NOAA tsunami events"
  },
  {
    type: "wildfire",
    fromYear: 2000,
    toYear: 2026,
    sources: [
      "eonet"
    ],
    note: "EONET events; dense from 2015"
  },
  {
    type: "fire_activity",
    fromYear: 2000,
    toYear: 2026,
    sources: [
      "firms"
    ],
    note: "MODIS active-fire detections (not events)"
  },
  {
    type: "flood",
    fromYear: 1985,
    toYear: 2026,
    sources: [
      "dfo",
      "gdacs"
    ],
    note: "DFO 1985–2021, GDACS 2022→"
  },
  {
    type: "landslide",
    fromYear: 1988,
    toYear: 2026,
    sources: [
      "glc",
      "eonet"
    ],
    note: "NASA GLC 1988–2017, EONET 2018→ (sparse)"
  },
  {
    type: "storm",
    fromYear: 1900,
    toYear: 2026,
    sources: [
      "ibtracs"
    ],
    note: "IBTrACS tropical cyclones"
  }
]

export const MISSIONS: SARMission[] = [
  {
    id: "seasat",
    name: "Seasat",
    agency: "NASA",
    band: "L",
    from: "1978-06-28",
    to: "1978-10-10",
    searchable: true,
    note: "Coverage limited to ground-station areas (North America, Europe)"
  },
  {
    id: "ers",
    name: "ERS-1/2",
    agency: "ESA",
    band: "C",
    from: "1991-07-17",
    to: "2011-07-04",
    searchable: false,
    note: "Not returned by our ASF query"
  },
  {
    id: "jers1",
    name: "JERS-1",
    agency: "JAXA",
    band: "L",
    from: "1992-02-11",
    to: "1998-10-12",
    searchable: false,
    note: "Not returned by our ASF query"
  },
  {
    id: "radarsat1",
    name: "RADARSAT-1",
    agency: "CSA",
    band: "C",
    from: "1995-11-04",
    to: "2013-03-29",
    searchable: false,
    note: "CSA open data portal (link only)"
  },
  {
    id: "envisat",
    name: "Envisat ASAR",
    agency: "ESA",
    band: "C",
    from: "2002-03-01",
    to: "2012-04-08",
    searchable: false,
    note: "Not in our ASF query"
  },
  {
    id: "alos",
    name: "ALOS PALSAR",
    agency: "JAXA",
    band: "L",
    from: "2006-05-16",
    to: "2011-04-21",
    searchable: true
  },
  {
    id: "sentinel1",
    name: "Sentinel-1",
    agency: "ESA/Copernicus",
    band: "C",
    from: "2014-04-03",
    searchable: true
  },
  {
    id: "saocom",
    name: "SAOCOM-1A/1B",
    agency: "CONAE",
    band: "L",
    from: "2018-10-07",
    searchable: false,
    note: "Partner agency: no public search API; used only in a precomputed story (Argentina)"
  },
  {
    id: "rcm",
    name: "RADARSAT Constellation",
    agency: "CSA",
    band: "C",
    from: "2019-06-12",
    searchable: false,
    note: "Partner agency (link only)"
  },
  {
    id: "nisar",
    name: "NISAR",
    agency: "NASA/ISRO",
    band: "L+S",
    from: "2025-10-28",
    searchable: true
  }
]

/** Which catalog each mock incident type pretends to come from. */
export const SOURCE_BY_TYPE: Partial<Record<IncidentTypeId, IncidentSource>> = {
  earthquake: "usgs",
  volcano: "gvp",
  flood: "dfo",
  storm: "ibtracs",
  wildfire: "eonet",
  landslide: "glc",
}

export const SOURCE_URL: Record<IncidentSource, string> = {
  usgs: "https://earthquake.usgs.gov/earthquakes/search/",
  noaa_eq: "https://www.ngdc.noaa.gov/hazel/view/hazards/earthquake/search",
  noaa_ts: "https://www.ngdc.noaa.gov/hazel/view/hazards/tsunami/event-search",
  noaa_vo: "https://www.ngdc.noaa.gov/hazel/view/hazards/volcano/event-search",
  gvp: "https://volcano.si.edu/",
  eonet: "https://eonet.gsfc.nasa.gov/",
  gdacs: "https://www.gdacs.org/",
  ibtracs: "https://www.ncei.noaa.gov/products/international-best-track-archive",
  glc: "https://gpm.nasa.gov/landslides/",
  dfo: "https://floodobservatory.colorado.edu/",
}
