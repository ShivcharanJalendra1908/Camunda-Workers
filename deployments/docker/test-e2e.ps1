# ============================================================
# Basic Request ID Propagation Test Suite
# Tests X-Request-ID propagation across Kong, API Gateway,
# Camunda Workers, and Error Responses.
# Usage: .\test-e2e.ps1
# ============================================================

# ============================================================
# SETUP - Generate unique test ID
# ============================================================
$id = "e2e-" + [guid]::NewGuid().ToString().Substring(0,8)
Write-Host "Test ID: $id" -ForegroundColor Cyan

# ============================================================
# TEST 1 - Health endpoint (simplest flow)
# ============================================================
Write-Host "`n=== TEST 1: Health ===" -ForegroundColor Yellow
curl.exe -s -H "X-Request-ID: $id" http://localhost:8000/health -D h.tmp -o nul
$kongId = (Select-String "^X-Request-Id: " h.tmp).Line -replace '^X-Request-Id: ', ''
Write-Host "Sent:     $id"
Write-Host "Received: $kongId"
if ($kongId -eq $id) { Write-Host "`u{2705} Kong propagated ID correctly" -ForegroundColor Green } else { Write-Host "`u{274C} ID mismatch" -ForegroundColor Red }

# ============================================================
# TEST 2 - API endpoint (Kong Backend)
# ============================================================
Write-Host "`n=== TEST 2: API Endpoint ===" -ForegroundColor Yellow
curl.exe -s -H "X-Request-ID: $id" -H "Host: api.mvp.lemici.com" http://localhost:8000/api/v1/franchises/home -D h.tmp -o nul
$kongId = (Select-String "^X-Request-Id: " h.tmp).Line -replace '^X-Request-Id: ', ''
Write-Host "Sent:     $id"
Write-Host "Received: $kongId"
if ($kongId -eq $id) { Write-Host "`u{2705} ID propagated to backend" -ForegroundColor Green } else { Write-Host "`u{274C} ID mismatch" -ForegroundColor Red }

# ============================================================
# TEST 3 - Error response carries same ID
# ============================================================
Write-Host "`n=== TEST 3: Error Response ID ===" -ForegroundColor Yellow
curl.exe -s -H "X-Request-ID: $id" http://localhost:8000/api/v1/nonexistent -D h.tmp -o b.tmp
$bodyId = (Get-Content b.tmp | ConvertFrom-Json).requestId
Write-Host "Sent:      $id"
Write-Host "Body ID:   $bodyId"
if ($bodyId -eq $id) { Write-Host "`u{2705} Error body carries same ID" -ForegroundColor Green } else { Write-Host "`u{274C} ID mismatch in error body" -ForegroundColor Red }

# ============================================================
# TEST 4 - Workflow flow (Kong Backend Camunda)
# ============================================================
Write-Host "`n=== TEST 4: Workflow Flow ===" -ForegroundColor Yellow
curl.exe -s -X POST -H "X-Request-ID: $id" -H "Content-Type: application/json" -d "{}" http://localhost:8000/api/v1/auth/login -D h.tmp -o nul
$kongId = (Select-String "^X-Request-Id: " h.tmp).Line -replace '^X-Request-Id: ', ''
Write-Host "Sent:     $id"
Write-Host "Received: $kongId"
if ($kongId -eq $id) { Write-Host "`u{2705} ID flows into workflow" -ForegroundColor Green } else { Write-Host "`u{274C} ID mismatch" -ForegroundColor Red }

# ============================================================
# TEST 5 - Check OpenTelemetry trace headers
# ============================================================
Write-Host "`n=== TEST 5: OpenTelemetry Headers ===" -ForegroundColor Yellow
curl.exe -s -H "X-Request-ID: $id" http://localhost:8000/health -D h.tmp -o nul
Write-Host "=== Tracing Headers ===" -ForegroundColor Cyan
Select-String "X-Request-Id|X-Trace-Id|X-Span-Id|X-Kong-Request-Id|X-Kong-Proxy" h.tmp | ForEach-Object { Write-Host $_.Line }

# ============================================================
# TEST 6 - Backend logs verification
# ============================================================
Write-Host "`n=== TEST 6: Backend Logs ===" -ForegroundColor Yellow
Start-Sleep -Seconds 2
$logs = docker logs api-gateway --tail 50 2>&1 | Select-String $id
if ($logs) {
    Write-Host "`u{2705} Found in backend logs:" -ForegroundColor Green
    $logs | ForEach-Object { Write-Host $_.Line }
} else {
    Write-Host "`u{274C} Not found in backend logs" -ForegroundColor Red
}

# ============================================================
# TEST 7 - Verify Kong auto-generates ID when none provided
# ============================================================
Write-Host "`n=== TEST 7: Kong Auto-generates ID ===" -ForegroundColor Yellow
curl.exe -s http://localhost:8000/health -D h.tmp -o nul
$autoId = (Select-String "^X-Request-Id: " h.tmp).Line -replace '^X-Request-Id: ', ''
Write-Host "Kong generated: $autoId"
if ($autoId) {
    Write-Host "`u{2705} Kong generated an ID" -ForegroundColor Green
} else {
    Write-Host "`u{274C} No ID generated" -ForegroundColor Red
}
Start-Sleep -Seconds 2
$backendLog = docker logs api-gateway --tail 20 2>&1 | Select-String $autoId
if ($backendLog) {
    Write-Host "`u{2705} Kong-generated ID in backend logs" -ForegroundColor Green
} else {
    Write-Host "`u{274C} ID not found in backend logs" -ForegroundColor Red
}

# ============================================================
# TEST 8 - E2E Correlation Check
# ============================================================
Write-Host "`n=== TEST 8: E2E Correlation Check ===" -ForegroundColor Yellow
$newId = "verify-" + [guid]::NewGuid().ToString().Substring(0,6)
curl.exe -s -H "X-Request-ID: $newId" -H "Content-Type: application/json" -X POST -d "{}" http://localhost:8000/api/v1/auth/login -o nul
Start-Sleep -Seconds 3

Write-Host "Checking ID: $newId" -ForegroundColor Cyan

$apiFound = docker logs api-gateway --tail 100 2>&1 | Select-String $newId
if ($apiFound) {
    Write-Host "  `u{2705} API Gateway: FOUND" -ForegroundColor Green
} else {
    Write-Host "  `u{274C} API Gateway: NOT FOUND" -ForegroundColor Red
}

$kongHeader = curl.exe -s -I -H "X-Request-ID: $newId" http://localhost:8000/health 2>&1 | Select-String $newId
if ($kongHeader) {
    Write-Host "  `u{2705} Kong Header: ECHOES BACK" -ForegroundColor Green
} else {
    Write-Host "  `u{274C} Kong Header: NOT FOUND" -ForegroundColor Red
}

Write-Host "  `u{1F4CA} Jaeger: Check http://localhost:16686 for trace with http.request_id=$newId" -ForegroundColor Cyan

# ============================================================
# SUMMARY
# ============================================================
Write-Host "`n=== TEST SUMMARY ===" -ForegroundColor Magenta
Write-Host "Test ID: $id" -ForegroundColor White
Write-Host "All tests completed!" -ForegroundColor Green

# ============================================================
# CLEANUP
# ============================================================
Remove-Item h.tmp, b.tmp -ErrorAction SilentlyContinue
Write-Host "`n=== DONE ===" -ForegroundColor Cyan
