$directory = "c:\Users\lenovo\Desktop\LeMiCi\Camunda-Workers\data\postgres\final"
$franchiseFile = Join-Path $directory "franchises.csv"

# Function to get IDs from a CSV
function Get-Ids {
    param([string]$filePath, [string]$columnName)
    $content = Import-Csv -Path $filePath
    return $content.$columnName
}

# Get main franchise IDs
Write-Host "Reading franchises.csv..."
$franchiseIds = Get-Ids -filePath $franchiseFile -columnName "id"
$franchiseIdSet = New-Object System.Collections.Generic.HashSet[string]
foreach ($id in $franchiseIds) { [void]$franchiseIdSet.Add($id) }

Write-Host "Total Franchises in franchises.csv: $($franchiseIdSet.Count)"

$otherFiles = @(
    "franchise_business_overview.csv",
    "franchise_categories.csv",
    "franchise_investment_requirement.csv",
    "franchise_operations.csv",
    "franchise_stats.csv",
    "franchise_social_links.csv",
    "franchise_cities.csv"
)

foreach ($fileName in $otherFiles) {
    $filePath = Join-Path $directory $fileName
    if (-not (Test-Path $filePath)) {
        Write-Host "`nFile $fileName not found." -ForegroundColor Yellow
        continue
    }

    Write-Host "`nAnalysis for $fileName`:"
    try {
        $tableContent = Import-Csv -Path $filePath
        $tableIds = $tableContent.franchise_id | Where-Object { $_ -ne $null -and $_ -ne "" }
        
        $tableIdSet = New-Object System.Collections.Generic.HashSet[string]
        foreach ($id in $tableIds) { [void]$tableIdSet.Add($id) }

        Write-Host "  Total records: $($tableIds.Count)"
        Write-Host "  Unique franchise_ids: $($tableIdSet.Count)"

        $orphans = @()
        foreach ($id in $tableIdSet) {
            if (-not $franchiseIdSet.Contains($id)) { $orphans += $id }
        }

        if ($orphans.Count -gt 0) {
            Write-Host "  Orphans (in $fileName but not in franchises.csv): $($orphans.Count)" -ForegroundColor Red
            Write-Host "    IDs: $($orphans -join ', ')"
        } else {
            Write-Host "  No orphans found." -ForegroundColor Green
        }

        $missing = @()
        foreach ($id in $franchiseIdSet) {
            if (-not $tableIdSet.Contains($id)) { $missing += $id }
        }

        if ($missing.Count -gt 0) {
            Write-Host "  Missing (in franchises.csv but not in $fileName): $($missing.Count)" -ForegroundColor Cyan
            if ($missing.Count -gt 10) {
                Write-Host "    IDs (first 10): $(($missing | Select-Object -First 10) -join ', ')..."
            } else {
                Write-Host "    IDs: $($missing -join ', ')"
            }
        } else {
            Write-Host "  All franchise_ids from franchises.csv are present." -ForegroundColor Green
        }
    } catch {
        Write-Host "  Error processing file: $($_.Exception.Message)" -ForegroundColor Red
    }
}
