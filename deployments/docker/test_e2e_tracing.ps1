# ==============================================================================
# End-to-End Request Tracing Test Suite
# Usage: ./test_e2e_tracing.ps1
# ==============================================================================

$H = "api.mvp.lemici.com"
$KONG = "http://localhost:8000"
$JAEGER = "http://localhost:16686"

$PASS = 0; $FAIL = 0; $START = Get-Date

function Test-Step {
    param([string]$Name, [scriptblock]$Block)
    Write-Host ("-" * 70) -ForegroundColor DarkGray
    Write-Host "TEST: $Name" -ForegroundColor Yellow
    try {
        & $Block
        Write-Host "  [PASS]" -ForegroundColor Green
        $script:PASS++
    } catch {
        Write-Host "  [FAIL] $_" -ForegroundColor Red
        $script:FAIL++
    }
    Write-Host ""
}

function Get-ReqId { return [guid]::NewGuid().ToString() }

# Helper: curl headers as single string
function Get-Headers($url, [hashtable]$extra = @{}) {
    $h = "-H", ("Host: " + $H)
    foreach ($k in $extra.Keys) { $h += "-H"; $h += ($k + ": " + $extra[$k]) }
    $raw = & curl.exe -s -D- $h $url 2>&1
    return ($raw | Out-String)
}

# ------------------------------------------------------------------------------
# HAPPY PATH
# ------------------------------------------------------------------------------

Test-Step -Name "1. X-Request-ID echoed back" -Block {
    $R = Get-ReqId
    $resp = Get-Headers ($KONG + "/health") @{"X-Request-ID" = $R}
    if ($resp -notmatch [regex]::Escape($R)) { throw "X-Request-ID not echoed" }
    Write-Host "  ID: $R"
}

Test-Step -Name "2. X-Trace-ID and X-Span-ID in response" -Block {
    $resp = Get-Headers ($KONG + "/health")
    if ($resp -notmatch "X-Trace-Id\: [a-f0-9]{32}") { throw "X-Trace-ID missing" }
    if ($resp -notmatch "X-Span-Id\: [a-f0-9]{16}") { throw "X-Span-ID missing" }
}

Test-Step -Name "3. Kong security headers" -Block {
    $resp = Get-Headers ($KONG + "/health")
    foreach ($h in @("X-Frame-Options", "X-Content-Type-Options", "X-XSS-Protection", "Content-Security-Policy")) {
        if ($resp -notmatch [regex]::Escape($h)) { throw "Missing: $h" }
    }
}

Test-Step -Name "4. Rate limiting headers" -Block {
    $resp = Get-Headers ($KONG + "/health")
    if ($resp -notmatch "RateLimit-Limit") { throw "RateLimit-Limit missing" }
}

Test-Step -Name "5. CORS with valid origin" -Block {
    $resp = Get-Headers ($KONG + "/health") @{"Origin" = "https://dev.lemici.com"}
    if ($resp -notmatch "Access-Control-Allow-Origin") { throw "ACAO missing" }
}

Test-Step -Name "6. Kong auto-generates X-Request-ID" -Block {
    $resp = Get-Headers ($KONG + "/health")
    if ($resp -notmatch "X-Request-Id\: [a-f0-9-]{36}") { throw "No auto-generated X-Request-ID" }
}

Test-Step -Name "7. X-Kong-Request-Id present" -Block {
    $resp = Get-Headers ($KONG + "/health")
    if ($resp -notmatch "X-Kong-Request-Id\:") { throw "X-Kong-Request-Id missing" }
}

# ------------------------------------------------------------------------------
# ERROR CASES
# ------------------------------------------------------------------------------

Test-Step -Name "8. Invalid path -> 404" -Block {
    $code = curl.exe -s -w "%{http_code}" -o nul -H ("Host: " + $H) ($KONG + "/nonexistent") 2>&1
    if ($code -ne "404") { throw "Expected 404, got $code" }
}

Test-Step -Name "9. Unknown Host -> 404" -Block {
    $code = curl.exe -s -w "%{http_code}" -o nul -H "Host: unknown.example.com" ($KONG + "/api/v1/franchises/home") 2>&1
    if ($code -ne "404") { throw "Expected 404, got $code" }
}

Test-Step -Name "10. Blocked admin route -> 404" -Block {
    $code = curl.exe -s -w "%{http_code}" -o nul -H ("Host: " + $H) ($KONG + "/api/admin") 2>&1
    if ($code -ne "404") { throw "Expected 404, got $code" }
}

Test-Step -Name "11. Missing auth -> 401" -Block {
    $body = '{"searchText":"test","ownerId":"test"}'
    $code = curl.exe -s -w "%{http_code}" -o nul -X POST -H ("Host: " + $H) -H "Content-Type: application/json" -d $body ($KONG + "/api/v1/franchises/search") 2>&1
    if ($code -ne "401") { throw "Expected 401, got $code" }
}

Test-Step -Name "12. Invalid Origin excluded from CORS" -Block {
    $resp = Get-Headers ($KONG + "/health") @{"Origin" = "http://evil.com"}
    if ($resp -match "Access-Control-Allow-Origin\: http://evil\.com") { throw "Evil origin got ACAO" }
}

# ------------------------------------------------------------------------------
# JAEGER VERIFICATION (~70s for batch export)
# ------------------------------------------------------------------------------

Test-Step -Name "13. Trace confirm in Jaeger by trace ID" -Block {
    $R = Get-ReqId
    Write-Host "  Request ID: $R"
    $resp = Get-Headers ($KONG + "/health") @{"X-Request-ID" = $R}
    $m = [regex]::Match($resp, "X-Trace-Id\: ([a-f0-9]{32})")
    if (-not $m.Success) { throw "No X-Trace-ID in response" }
    $traceId = $m.Groups[1].Value

    Write-Host "  Waiting 35s for OTel batch export..."
    Start-Sleep -Seconds 35

    $jr = curl.exe -s ($JAEGER + "/api/traces/" + $traceId) 2>&1 | Out-String
    if ($jr -match '"data":null') { throw "Trace $traceId not found in Jaeger" }
    Write-Host "  Trace confirmed in Jaeger!"
}

Test-Step -Name "14. Jaeger has api-gateway traces" -Block {
    $jr = curl.exe -s ($JAEGER + "/api/traces?service=lemici-api-gateway&lookback=1h") 2>&1 | Out-String
    $count = ($jr | Select-String -Pattern "traceID" | Measure-Object).Count
    if ($count -eq 0) { throw "Zero traces" }
    Write-Host "  $count traces found"
}

Test-Step -Name "15. Search Jaeger by http.request_id tag" -Block {
    $R = Get-ReqId
    curl.exe -s -H ("Host: " + $H) -H ("X-Request-ID: " + $R) ($KONG + "/health") > $null 2>&1
    Write-Host "  Waiting 35s..."
    Start-Sleep -Seconds 35

    $tags = [Uri]::EscapeDataString('{"http.request_id":"' + $R + '"}')
    $jr = curl.exe -s ($JAEGER + "/api/traces?service=lemici-api-gateway&tags=" + $tags + "&lookback=5m") 2>&1 | Out-String
    if ($jr -match '"data":null') { throw "Not found" }
    Write-Host "  Found by http.request_id tag!"
}

# ------------------------------------------------------------------------------
# RESULTS
# ------------------------------------------------------------------------------
$END = Get-Date
$TOTAL = $PASS + $FAIL
$DURATION = [math]::Round(($END - $START).TotalSeconds)

Write-Host ("=" * 70) -ForegroundColor Cyan
if ($FAIL -eq 0) {
    Write-Host "ALL $TOTAL TESTS PASSED (${DURATION}s)" -ForegroundColor Green
    Write-Host "End-to-end request tracing is working!" -ForegroundColor Green
} else {
    Write-Host "$PASS/$TOTAL PASSED, $FAIL FAILED (${DURATION}s)" -ForegroundColor Red
}
Write-Host ("=" * 70) -ForegroundColor Cyan
Write-Host ""
Write-Host "  Jaeger: $JAEGER/search?service=lemici-api-gateway" -ForegroundColor White
.
+