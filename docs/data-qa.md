# Data QA (M17)

Run on the local dev DB after `make ingest-all` (2026-11-15, 10 sources, postprocess 1m03s).

- `ingest_runs`: 28 runs, all `ok`, none failed.
- Rows per source: usgs 313379, gdacs 43080, eonet 26141, glc 11033, ibtracs 6458, dfo 5130, gvp 4470, noaa_eq 4089, noaa_ts 1518, noaa_vo 530.
- Type/year ranges are plausible: dfo 1985-2021, glc 1988-2017, gdacs 1985-2026 (wildfire only from 2021), eonet 1980-2026, others 1900-2026.
- Known events: Tohoku 2011 (usgs, JPN), Pinatubo 1991 (gvp/noaa_vo/eonet, PHL), Haiyan 2013 (dfo PHL, eonet, ibtracs), Katrina 2005 (ibtracs). Note `search_text % 'word'` returns nothing for single words in long text (trigram threshold); use `ILIKE` for name lookups.
- `country_method`: contains 161163, nearest 180735, ocean 73911, source 19. Ocean is a minority overall and is dominated by earthquakes (67393 of 73911 in mv_counts); storms 1775, tsunamis 84, volcano 218.
- No rows with empty title or NULL severity in any source.
- `--since 30d` and `make ingest-recent` exist (usgs, eonet, gdacs, ibtracs); `ingest schedule` was not implemented (optional).

Observations, no mapping change needed (hashVersion unchanged):
- GVP lists Pinatubo eruption episodes for 1992, 1993 and 2021 (GVP continuing-eruption entries / dated records); kept as published.
- ibtracs storms whose first point is over the ocean have no country (4255 rows); expected.
- usgs `nocountry` rows are the ocean quakes (67367).
