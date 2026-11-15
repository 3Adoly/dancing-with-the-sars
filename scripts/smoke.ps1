# Smoke test for the DWTS API (Ahmed, A7). Calls every endpoint E1–E15 and prints a PASS/FAIL/SKIP table.
# Usage: powershell -ExecutionPolicy Bypass -File scripts\smoke.ps1 [-BaseUrl http://127.0.0.1:8080] [-IncidentId 500812]
# Exit code: 0 = no FAIL, 1 = at least one FAIL. Endpoints answering 501 (not built yet) → SKIP.
param(
  [string]$BaseUrl = $(if ($env:BASE_URL) { $env:BASE_URL } else { "http://127.0.0.1:8080" }),
  [string]$IncidentId = $(if ($env:INCIDENT_ID) { $env:INCIDENT_ID } else { "500812" })
)
$ErrorActionPreference = "Stop"
$BaseUrl = $BaseUrl.TrimEnd("/")
Add-Type -AssemblyName System.Net.Http
$handler = New-Object System.Net.Http.HttpClientHandler
$handler.AllowAutoRedirect = $false
$client = New-Object System.Net.Http.HttpClient($handler)
$client.Timeout = [TimeSpan]::FromSeconds(30)
$script:pass = 0; $script:fail = 0; $script:skip = 0
$script:last = $null

function Row($res, $code, $id, $path, $note) { "{0,-6} {1,-5} {2,-4} {3,-62} {4}" -f $res, $code, $id, $path, $note }

function Check($id, $method, $path, $want, $key, $headers = @{}, $body = $null) {
  $req = New-Object System.Net.Http.HttpRequestMessage([System.Net.Http.HttpMethod]::new($method), "$BaseUrl$path")
  foreach ($k in $headers.Keys) { [void]$req.Headers.TryAddWithoutValidation($k, $headers[$k]) }
  if ($body) { $req.Content = New-Object System.Net.Http.StringContent($body, [Text.Encoding]::UTF8, "application/json") }
  try { $resp = $client.SendAsync($req).GetAwaiter().GetResult() } catch { Row "FAIL" "000" $id $path $_.Exception.Message; $script:fail++; return }
  $code = [int]$resp.StatusCode
  $text = $resp.Content.ReadAsStringAsync().GetAwaiter().GetResult()
  $script:last = @{ Code = $code; Body = $text; Resp = $resp }
  if ($code -eq 501) { Row "SKIP" $code $id $path "not implemented yet"; $script:skip++; return }
  if ($code -eq 503 -and $text -match '"chat_disabled"') { Row "SKIP" $code $id $path "chat disabled (optional)"; $script:skip++; return }
  if ($code -ne $want) { Row "FAIL" $code $id $path ("want $want`: " + $text.Substring(0, [Math]::Min(120, $text.Length))); $script:fail++; return }
  if ($key -and $text -notmatch [regex]::Escape("`"$key`"")) { Row "FAIL" $code $id $path "missing field `"$key`""; $script:fail++; return }
  if (-not $resp.Headers.Contains("X-Request-Id")) { Row "FAIL" $code $id $path "missing X-Request-Id"; $script:fail++; return }
  Row "PASS" $code $id $path $(if ($key) { "has `"$key`"" } else { "" }); $script:pass++
}

Row "RESULT" "CODE" "ID" "PATH" "NOTE"
"-" * 96
Check "E1" "GET" "/healthz" 200 "status"
Check "E2" "GET" "/api/v1/meta" 200 "datasetVersion"
$etag = $null
if ($script:last.Resp.Headers.ETag) { $etag = $script:last.Resp.Headers.ETag.ToString() }
if ($etag) { Check "E2" "GET" "/api/v1/meta" 304 "" @{ "If-None-Match" = $etag } }
Check "E3" "GET" "/api/v1/incident-types" 200 "types"
Check "E4" "GET" "/api/v1/stats/choropleth?types=earthquake,flood&from=1950&to=2000" 200 "counts"
Check "E4" "GET" "/api/v1/stats/choropleth" 400 "error"
Check "E5" "GET" "/api/v1/stats/timeseries?types=storm&from=1980&to=2000&iso3=PHL" 200 "points"
Check "E6" "GET" "/api/v1/stats/grid?types=earthquake&from=2000&to=2020&bbox=120,20,150,50&cell=1" 200 "cells"
Check "E7" "GET" "/api/v1/incidents?types=volcano&from=1990&to=1995&limit=5" 200 "items"
Check "E8" "GET" "/api/v1/incidents/$IncidentId" 200 "id"
Check "E9" "GET" "/api/v1/search?q=katrina" 200 "items"
Check "E10" "GET" "/api/v1/incidents/$IncidentId/sar" 200 "coverage"
Check "E10" "GET" "/api/v1/incidents/abc/sar" 400 "error"
Check "E11" "GET" "/api/v1/sar/coverage-timeline" 200 "missions"
Check "E12" "GET" "/api/v1/stories" 200 "stories"
$story = $null
if ($script:last.Code -eq 200 -and $script:last.Body -match '"id":"([^"]+)"') { $story = $Matches[1] }
if ($story) {
  Check "E13" "GET" "/api/v1/stories/$story" 200 "layers"
  if ($script:last.Body -match '"thumbnail":"([^"]+)"') {
    $thumb = $Matches[1]
    Check "E14" "GET" $thumb 200 ""
    if ($script:last.Resp.Content.Headers.ContentType.MediaType -notlike "image/*") { Row "FAIL" "-" "E14" $thumb "not an image content type"; $script:fail++ }
  }
  Check "E14" "GET" "/media/stories/$story/..%2f..%2fgo.mod" 404 "error"
} else { Row "SKIP" "-" "E13" "/api/v1/stories/{id}" "no stories loaded"; $script:skip++ }
Check "E13" "GET" "/api/v1/stories/does-not-exist" 404 "error"
Check "E15" "POST" "/api/v1/chat" 200 "answer" @{} '{"messages":[{"role":"user","content":"How many stories are there?"}]}'
Check "-" "POST" "/api/v1/meta" 405 "error"
"-" * 96
"PASS=$($script:pass) FAIL=$($script:fail) SKIP=$($script:skip)  (base $BaseUrl)"
if ($script:fail -gt 0) { exit 1 } else { exit 0 }
