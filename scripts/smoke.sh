#!/usr/bin/env bash
# Smoke test for the DWTS API (Ahmed, A7). Calls every endpoint E1–E15 and prints a PASS/FAIL/SKIP table.
# Usage: BASE_URL=http://127.0.0.1:8080 INCIDENT_ID=500812 bash scripts/smoke.sh
# Exit code: 0 = no FAIL, 1 = at least one FAIL. Endpoints answering 501 (not built yet, e.g. need the DB) → SKIP.
set -u
BASE_URL="${BASE_URL:-${1:-http://127.0.0.1:8080}}"
BASE_URL="${BASE_URL%/}"
INCIDENT_ID="${INCIDENT_ID:-500812}"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
pass=0; fail=0; skip=0
printf '%-6s %-5s %-4s %-62s %s\n' RESULT CODE ID PATH NOTE
printf '%s\n' "------------------------------------------------------------------------------------------------"

row() { printf '%-6s %-5s %-4s %-62s %s\n' "$1" "$2" "$3" "$4" "$5"; }

# check ID METHOD PATH WANT_STATUS KEY [extra curl args...]
check() {
  local id="$1" method="$2" path="$3" want="$4" key="$5"; shift 5
  local code
  : > "$TMP/body"; : > "$TMP/head"
  code=$(curl -s -o "$TMP/body" -D "$TMP/head" -w '%{http_code}' -X "$method" "$@" "$BASE_URL$path" 2>/dev/null) || true
  [ "$code" = "000" ] && { row FAIL 000 "$id" "$path" "connection failed"; fail=$((fail+1)); return; }
  if [ "$code" = "501" ]; then row SKIP "$code" "$id" "$path" "not implemented yet"; skip=$((skip+1)); return; fi
  if [ "$code" = "503" ] && grep -q '"chat_disabled"' "$TMP/body"; then row SKIP "$code" "$id" "$path" "chat disabled (optional)"; skip=$((skip+1)); return; fi
  if [ "$code" != "$want" ]; then row FAIL "$code" "$id" "$path" "want $want: $(head -c 120 "$TMP/body")"; fail=$((fail+1)); return; fi
  if [ -n "$key" ] && ! grep -q "\"$key\"" "$TMP/body"; then row FAIL "$code" "$id" "$path" "missing field \"$key\""; fail=$((fail+1)); return; fi
  if ! grep -qi '^x-request-id:' "$TMP/head"; then row FAIL "$code" "$id" "$path" "missing X-Request-Id"; fail=$((fail+1)); return; fi
  row PASS "$code" "$id" "$path" "${key:+has \"$key\"}"; pass=$((pass+1))
}

check E1  GET  "/healthz" 200 status
check E2  GET  "/api/v1/meta" 200 datasetVersion
etag=$(grep -i '^etag:' "$TMP/head" | cut -d' ' -f2- | tr -d '\r')
if [ -n "$etag" ]; then check E2  GET "/api/v1/meta" 304 "" -H "If-None-Match: $etag"; fi
check E3  GET  "/api/v1/incident-types" 200 types
check E4  GET  "/api/v1/stats/choropleth?types=earthquake,flood&from=1950&to=2000" 200 counts
check E4  GET  "/api/v1/stats/choropleth" 400 error
check E5  GET  "/api/v1/stats/timeseries?types=storm&from=1980&to=2000&iso3=PHL" 200 points
check E6  GET  "/api/v1/stats/grid?types=earthquake&from=2000&to=2020&bbox=120,20,150,50&cell=1" 200 cells
check E7  GET  "/api/v1/incidents?types=volcano&from=1990&to=1995&limit=5" 200 items
check E8  GET  "/api/v1/incidents/$INCIDENT_ID" 200 id
check E9  GET  "/api/v1/search?q=katrina" 200 items
check E10 GET  "/api/v1/incidents/$INCIDENT_ID/sar" 200 coverage
check E10 GET  "/api/v1/incidents/abc/sar" 400 error
check E11 GET  "/api/v1/sar/coverage-timeline" 200 missions
check E12 GET  "/api/v1/stories" 200 stories
story=$(grep -o '"id":"[^"]*"' "$TMP/body" | head -1 | cut -d'"' -f4)
if [ -n "$story" ]; then
  check E13 GET "/api/v1/stories/$story" 200 layers
  thumb=$(grep -o '"thumbnail":"[^"]*"' "$TMP/body" | head -1 | cut -d'"' -f4)
  if [ -n "$thumb" ]; then
    check E14 GET "$thumb" 200 ""
    grep -qi '^content-type: image/' "$TMP/head" || { row FAIL "-" E14 "$thumb" "not an image content type"; fail=$((fail+1)); }
  fi
  check E14 GET "/media/stories/$story/..%2f..%2fgo.mod" 404 error --path-as-is
else
  row SKIP "-" E13 "/api/v1/stories/{id}" "no stories loaded"; skip=$((skip+1))
fi
check E13 GET  "/api/v1/stories/does-not-exist" 404 error
check E15 POST "/api/v1/chat" 200 answer -H "Content-Type: application/json" --data '{"messages":[{"role":"user","content":"How many stories are there?"}]}'
check -   POST "/api/v1/meta" 405 error

printf '%s\n' "------------------------------------------------------------------------------------------------"
echo "PASS=$pass FAIL=$fail SKIP=$skip  (base $BASE_URL)"
[ "$fail" -eq 0 ]
