package postgres

import "fmt"

// Nearest-country searches (P2, P5(c), AssignCellCountries) need a geodesic
// distance between points or tracks and country polygons. Casting a whole
// country (Canada, Russia) to geography for every candidate pair costs
// ~10 ms per incident, far too slow for ~150k offshore quakes. Instead:
//
//  1. Parts: the countries are cut once into small pieces (ST_Subdivide,
//     ≤ 128 vertices), each kept as geometry and geography with its envelope.
//     The distance to a country is the minimum over its pieces.
//  2. Boxes, per search radius R: each piece's envelope grown by R in degrees,
//     wide enough at the piece's highest latitude, as a GiST-indexed temp
//     table. Joins run box-first, so consecutive geodesic calls share the
//     same piece and PostGIS reuses its cached edge tree.
//
// For a POINT (P2, AssignCellCountries) the box is a strict superset of
// "within R" (a necessary condition), so the geodesic test after it decides
// alone and nothing within R is missed:
//   - 1° of latitude is ≥ 110.57 km, so dy = R / 110 km is enough;
//   - any point within R of the piece lies at |lat| ≤ maxLat + dy, where 1° of
//     longitude is ≥ 111.32 km · cos(maxLat + dy), so dx = dy / cos(maxLat + dy);
//   - near the poles (maxLat + dy ≥ 89°, or dx ≥ 180°) the box spans every
//     longitude;
//   - a box that crosses ±180° gets a copy shifted by ∓360° (with the piece
//     geometry shifted alike), so a point on the other side of the
//     antimeridian is still a candidate.
//
// Two cheap planar tests in degree space bracket the geodesic one:
//   - necessary: a point within R differs by ≤ dx in longitude and ≤ dy in
//     latitude, so its planar distance is ≤ dlim = √(dx² + dy²);
//   - sufficient: one degree in any direction is ≤ 111.7 km, so a planar
//     distance ≤ R / 111.7 km is within R.
//
// For a track SEGMENT (P5(c)) these bounds are exact only for the segment as
// drawn in lon/lat. The geodesic test treats it as a great-circle arc, which
// bulges poleward of the straight lon/lat line, so the box/dlim prefilter
// (and the sufficient bound) can differ from a pure geodesic test by a few km
// at high latitude for long segments (~L²·tan(lat)/8R_earth: ≈ 2 km for a
// 300 km segment at 45°). On the seed and on 13k synthetic tracks the result
// still equalled the geodesic-only query exactly.

// subdivideVertices is the ST_Subdivide vertex limit for country pieces
// (128 was about the fastest of 32/64/128/256 on 21k synthetic offshore points).
const subdivideVertices = 128

// maxMetresPerDegree bounds the length of one degree of latitude or longitude
// anywhere on the WGS84 ellipsoid (111,694 m for latitude at the poles).
const maxMetresPerDegree = 111700

// countryPartsSQL builds the temp table name (iso3, geom, geog, x0, x1, y0,
// y1) of country pieces. onCommitDrop scopes it to the transaction;
// otherwise it lives for the session and the caller drops it.
func countryPartsSQL(name string, onCommitDrop bool) []string {
	scope := ""
	if onCommitDrop {
		scope = " ON COMMIT DROP"
	}
	return []string{fmt.Sprintf(`
CREATE TEMP TABLE %s%s AS
  SELECT c.iso3, part AS geom, part::geography AS geog,
         ST_XMin(part) AS x0, ST_XMax(part) AS x1, ST_YMin(part) AS y0, ST_YMax(part) AS y1
    FROM countries c, ST_Subdivide(c.geom, %d) AS part`, name, scope, subdivideVertices)}
}

// countryBoxesSQL builds the transaction temp table name (iso3, geog, geom,
// dlim, env) from the parts table for radius radiusM metres: geom is the
// piece (shifted with its box copy), env its search box (GiST-indexed) and
// dlim the necessary planar distance bound in degrees.
func countryBoxesSQL(name, parts string, radiusM float64) []string {
	dy := radiusM / 110000
	return []string{
		fmt.Sprintf(`
CREATE TEMP TABLE %[1]s ON COMMIT DROP AS
WITH grown AS (
  SELECT iso3, geom, geog, x0, x1, y0 - %[3]g AS y0, y1 + %[3]g AS y1,
         CASE WHEN greatest(abs(y0), abs(y1)) + %[3]g >= 89 THEN NULL
              ELSE %[3]g / cos(radians(greatest(abs(y0), abs(y1)) + %[3]g)) END AS dx
    FROM %[2]s
)
SELECT iso3, geog, ST_Translate(geom, s, 0) AS geom,
       CASE WHEN dx IS NULL OR dx >= 180 THEN 1e9 ELSE sqrt(dx * dx + %[3]g * %[3]g) END AS dlim,
       CASE WHEN dx IS NULL OR dx >= 180 THEN ST_MakeEnvelope(-180, y0, 180, y1, 4326)
            ELSE ST_MakeEnvelope(x0 - dx + s, y0, x1 + dx + s, y1, 4326) END AS env
  FROM grown, (VALUES (0::float8), (360), (-360)) AS shift(s)
 WHERE s = 0
    OR (dx < 180 AND ((s = 360 AND x0 - dx < -180) OR (s = -360 AND x1 + dx > 180)))`,
			name, parts, dy),
		fmt.Sprintf(`CREATE INDEX ON %s USING gist (env)`, name),
		fmt.Sprintf(`ANALYZE %s`, name),
	}
}
