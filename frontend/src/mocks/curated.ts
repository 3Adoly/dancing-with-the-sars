// Well-known real events so search and demos have recognizable entries.
// Dates/locations are approximate epicenters or impact centers. Magnitude
// meaning depends on type (see INCIDENT_TYPES.magnitudeLabel). null = not set.
import type { IncidentTypeId, Iso3 as CountryCode } from '@/api/types'

export interface CuratedEvent {
  id: string
  type: IncidentTypeId
  title: string
  date: string
  endDate?: string
  lon: number
  lat: number
  country: CountryCode | null
  magnitude: number | null
  description: string
  /** Onshore point to search radar scenes when the epicenter is offshore. */
  sarAt?: [number, number]
}

export const CURATED: CuratedEvent[] = [
  { id: 'ev-tohoku-2011', type: 'earthquake', title: '2011 Tōhoku earthquake and tsunami', date: '2011-03-11', lon: 142.373, lat: 38.297, country: 'JPN', magnitude: 9.1, sarAt: [141.0, 38.17], description: "Megathrust earthquake off Japan's Pacific coast. The tsunami flooded large stretches of the Tōhoku coastline." },
  { id: 'ev-campfire-2018', type: 'wildfire', title: 'Camp Fire, California', date: '2018-11-08', endDate: '2018-11-25', lon: -121.437, lat: 39.81, country: 'USA', magnitude: 620, description: "California's deadliest wildfire. It destroyed most of the town of Paradise." },
  { id: 'ev-sumatra-2004', type: 'earthquake', title: '2004 Indian Ocean earthquake and tsunami', date: '2004-12-26', lon: 95.854, lat: 3.316, country: 'IDN', magnitude: 9.1, sarAt: [95.32, 5.55], description: 'Undersea megathrust earthquake off Sumatra. The tsunami struck coasts across the Indian Ocean.' },
  { id: 'ev-haiti-2010', type: 'earthquake', title: '2010 Haiti earthquake', date: '2010-01-12', lon: -72.571, lat: 18.443, country: 'HTI', magnitude: 7.0, description: 'Shallow earthquake close to Port-au-Prince.' },
  { id: 'ev-turkey-2023', type: 'earthquake', title: '2023 Turkey–Syria earthquake', date: '2023-02-06', lon: 37.014, lat: 37.226, country: 'TUR', magnitude: 7.8, description: 'Ruptured the East Anatolian Fault. Sentinel-1 and ALOS-2 radar mapped meters of ground offset.' },
  { id: 'ev-gorkha-2015', type: 'earthquake', title: '2015 Gorkha earthquake, Nepal', date: '2015-04-25', lon: 84.731, lat: 28.231, country: 'NPL', magnitude: 7.8, description: 'Lifted the Kathmandu basin; radar interferograms showed the uplift pattern.' },
  { id: 'ev-valdivia-1960', type: 'earthquake', title: '1960 Valdivia earthquake', date: '1960-05-22', lon: -73.05, lat: -38.24, country: 'CHL', magnitude: 9.5, description: 'The strongest earthquake ever recorded.' },
  { id: 'ev-alaska-1964', type: 'earthquake', title: '1964 Great Alaska earthquake', date: '1964-03-27', lon: -147.339, lat: 60.908, country: 'USA', magnitude: 9.2, description: 'The strongest earthquake recorded in North America.' },
  { id: 'ev-pakistan-flood-2022', type: 'flood', title: '2022 Pakistan floods', date: '2022-06-14', endDate: '2022-10-31', lon: 68.3, lat: 26.9, country: 'PAK', magnitude: 3, description: 'Record monsoon rain flooded much of Sindh and Balochistan for months.' },
  { id: 'ev-ahr-2021', type: 'flood', title: '2021 European floods, Ahr valley', date: '2021-07-14', endDate: '2021-07-16', lon: 7.06, lat: 50.53, country: 'DEU', magnitude: 3, description: 'Extreme rainfall turned small rivers in western Germany and Belgium into torrents.' },
  { id: 'ev-katrina-2005', type: 'storm', title: 'Hurricane Katrina', date: '2005-08-29', lon: -89.6, lat: 29.3, country: 'USA', magnitude: 3, sarAt: [-90.07, 29.95], description: 'Storm surge broke the New Orleans levees and flooded most of the city.' },
  { id: 'ev-haiyan-2013', type: 'storm', title: 'Typhoon Haiyan (Yolanda)', date: '2013-11-08', lon: 125.0, lat: 11.2, country: 'PHL', magnitude: 5, sarAt: [125.0, 11.24], description: 'One of the strongest tropical cyclones at landfall. Storm surge devastated Tacloban.' },
  { id: 'ev-nargis-2008', type: 'storm', title: 'Cyclone Nargis', date: '2008-05-02', lon: 95.0, lat: 16.0, country: 'MMR', magnitude: 4, description: 'Storm surge swept across the Irrawaddy Delta.' },
  { id: 'ev-maria-2017', type: 'storm', title: 'Hurricane Maria', date: '2017-09-20', lon: -65.9, lat: 18.1, country: 'PRI', magnitude: 4, description: "Crossed the whole of Puerto Rico and knocked out the island's power grid." },
  { id: 'ev-sthelens-1980', type: 'volcano', title: 'Mount St. Helens eruption', date: '1980-05-18', lon: -122.18, lat: 46.2, country: 'USA', magnitude: 5, description: 'A lateral blast and landslide removed the top of the mountain.' },
  { id: 'ev-pinatubo-1991', type: 'volcano', title: 'Mount Pinatubo eruption', date: '1991-06-15', lon: 120.35, lat: 15.13, country: 'PHL', magnitude: 6, description: 'The second-largest eruption of the 20th century. Its ash cloud cooled the planet for a year.' },
  { id: 'ev-eyjafjalla-2010', type: 'volcano', title: 'Eyjafjallajökull eruption', date: '2010-04-14', lon: -19.62, lat: 63.63, country: 'ISL', magnitude: 4, description: 'Its ash cloud closed European airspace for days.' },
  { id: 'ev-hungatonga-2022', type: 'volcano', title: 'Hunga Tonga–Hunga Haʻapai eruption', date: '2022-01-15', lon: -175.39, lat: -20.55, country: 'TON', magnitude: 5, sarAt: [-175.2, -21.13], description: 'Underwater explosion whose pressure wave circled the globe several times.' },
  { id: 'ev-kilauea-2018', type: 'volcano', title: 'Kīlauea lower East Rift eruption', date: '2018-05-03', endDate: '2018-09-04', lon: -154.9, lat: 19.46, country: 'USA', magnitude: null, description: 'Lava flows covered neighborhoods in lower Puna while the summit collapsed.' },
  { id: 'ev-blacksummer-2019', type: 'wildfire', title: '2019–20 Australian bushfires (Black Summer)', date: '2019-09-01', endDate: '2020-03-04', lon: 150.3, lat: -35.3, country: 'AUS', magnitude: 186000, description: "Months of fires across south-eastern Australia's forests." },
  { id: 'ev-canada-2023', type: 'wildfire', title: '2023 Canadian wildfires', date: '2023-05-01', endDate: '2023-10-31', lon: -117.0, lat: 57.0, country: 'CAN', magnitude: null, description: "Canada's worst wildfire season on record. Smoke reached Europe." },
  { id: 'ev-lahaina-2023', type: 'wildfire', title: '2023 Lahaina wildfire, Maui', date: '2023-08-08', lon: -156.68, lat: 20.88, country: 'USA', magnitude: 9, description: 'Wind-driven fire destroyed the historic town of Lahaina.' },
  { id: 'ev-oso-2014', type: 'landslide', title: '2014 Oso landslide', date: '2014-03-22', lon: -121.85, lat: 48.28, country: 'USA', magnitude: 43, description: 'A hillside collapsed onto a rural neighborhood in Washington State.' },
  { id: 'ev-kedarnath-2013', type: 'flood', title: '2013 North India floods (Kedarnath)', date: '2013-06-16', lon: 79.07, lat: 30.73, country: 'IND', magnitude: 3, description: 'Cloudburst floods and debris flows in Uttarakhand.' },
  { id: 'ev-mocoa-2017', type: 'landslide', title: '2017 Mocoa landslide', date: '2017-04-01', lon: -76.65, lat: 1.15, country: 'COL', magnitude: null, description: 'Heavy rain triggered debris flows through the town of Mocoa.' },
  { id: 'ev-valencia-2024', type: 'flood', title: '2024 Valencia floods', date: '2024-10-29', lon: -0.42, lat: 39.4, country: 'ESP', magnitude: 3, description: "A cut-off low dumped a year's rain in hours on the Valencia region." },
  { id: 'ev-wayanad-2024', type: 'landslide', title: '2024 Wayanad landslides', date: '2024-07-30', lon: 76.13, lat: 11.48, country: 'IND', magnitude: null, description: 'Monsoon rain triggered debris flows that buried villages in Kerala.' },
  { id: 'ev-myanmar-2025', type: 'earthquake', title: '2025 Myanmar earthquake', date: '2025-03-28', lon: 95.92, lat: 22.01, country: 'MMR', magnitude: 7.7, description: 'Ruptured hundreds of kilometers of the Sagaing Fault near Mandalay.' },
  { id: 'ev-la-fires-2025', type: 'wildfire', title: 'January 2025 Los Angeles wildfires', date: '2025-01-07', endDate: '2025-01-31', lon: -118.54, lat: 34.07, country: 'USA', magnitude: 150, description: 'Wind-driven fires burned through Pacific Palisades and Altadena.' },
  { id: 'ev-derna-2023', type: 'flood', title: '2023 Derna floods (Storm Daniel)', date: '2023-09-10', lon: 22.64, lat: 32.76, country: 'LBY', magnitude: 3, description: 'Two dams failed above Derna during Storm Daniel.' },
  { id: 'ev-kashmir-2005', type: 'earthquake', title: '2005 Kashmir earthquake', date: '2005-10-08', lon: 73.59, lat: 34.49, country: 'PAK', magnitude: 7.6, description: 'Struck the mountains of Pakistan-administered Kashmir.' },
  { id: 'ev-sichuan-2008', type: 'earthquake', title: '2008 Sichuan earthquake', date: '2008-05-12', lon: 103.32, lat: 31.0, country: 'CHN', magnitude: 7.9, description: 'Thrust earthquake along the Longmen Shan. ALOS PALSAR mapped the deformation.' },
  { id: 'ev-bhola-1970', type: 'storm', title: '1970 Bhola cyclone', date: '1970-11-12', lon: 90.6, lat: 22.2, country: 'BGD', magnitude: 3, description: 'Storm surge across the Ganges delta; one of the deadliest cyclones on record.' },
  { id: 'ev-kermanshah-2017', type: 'earthquake', title: '2017 Iran–Iraq earthquake', date: '2017-11-12', lon: 45.96, lat: 34.91, country: 'IRN', magnitude: 7.3, description: 'Struck the Zagros mountains on the Iran–Iraq border. Sentinel-1 interferograms mapped the ground shift within days.' },
  { id: 'ev-kaikoura-2016', type: 'earthquake', title: '2016 Kaikōura earthquake', date: '2016-11-14', lon: 173.05, lat: -42.74, country: 'NZL', magnitude: 7.8, description: 'Ruptured more than a dozen faults and lifted parts of the coastline out of the sea.' },
  { id: 'ev-ruiz-1985', type: 'volcano', title: 'Nevado del Ruiz eruption (Armero)', date: '1985-11-13', lon: -75.32, lat: 4.89, country: 'COL', magnitude: 3, description: 'Melting ice sent mudflows down the valleys and buried the town of Armero.' },
  { id: 'ev-harvey-2017', type: 'storm', title: 'Hurricane Harvey', date: '2017-08-25', endDate: '2017-09-02', lon: -95.4, lat: 29.8, country: 'USA', magnitude: 4, description: 'Stalled over Texas and flooded Houston with record rainfall.' },
  { id: 'ev-haiti-2021', type: 'earthquake', title: '2021 Haiti earthquake', date: '2021-08-14', lon: -73.48, lat: 18.41, country: 'HTI', magnitude: 7.2, description: "Struck Haiti's Tiburon Peninsula." },
  { id: 'ev-noto-2024', type: 'earthquake', title: '2024 Noto earthquake', date: '2024-01-01', lon: 137.24, lat: 37.49, country: 'JPN', magnitude: 7.5, description: 'Lifted the Noto Peninsula coast by up to several meters.' },
  { id: 'ev-helene-2024', type: 'storm', title: 'Hurricane Helene', date: '2024-09-26', lon: -83.9, lat: 29.9, country: 'USA', magnitude: 4, sarAt: [-83.6, 30.1], description: 'Made landfall in Florida, then caused severe inland flooding in the Appalachians.' },
  { id: 'ev-kamchatka-2025', type: 'earthquake', title: '2025 Kamchatka earthquake', date: '2025-07-29', lon: 160.4, lat: 52.5, country: 'RUS', magnitude: 8.8, sarAt: [158.65, 53.02], description: 'Great subduction earthquake off Kamchatka that sent tsunami alerts across the Pacific.' },
  { id: 'ev-melissa-2025', type: 'storm', title: 'Hurricane Melissa', date: '2025-10-28', lon: -77.8, lat: 18.0, country: 'JAM', magnitude: 5, sarAt: [-77.85, 18.05], description: 'Category 5 landfall in Jamaica. One of the first big disasters of the NISAR science era.' },
  { id: 'ev-ditwah-2025', type: 'flood', title: '2025 Sri Lanka floods (Cyclone Ditwah)', date: '2025-11-28', lon: 80.6, lat: 7.3, country: 'LKA', magnitude: 3, description: 'Cyclone rain caused floods and landslides across central Sri Lanka.' },
  { id: 'ev-sumatra-flood-2025', type: 'flood', title: 'Late-2025 Sumatra floods and landslides', date: '2025-11-26', lon: 98.7, lat: 2.0, country: 'IDN', magnitude: 3, description: 'Heavy rain flooded river valleys across northern Sumatra.' },
]
