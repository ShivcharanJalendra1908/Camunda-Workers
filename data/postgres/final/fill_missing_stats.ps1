$directory = "c:\Users\lenovo\Desktop\LeMiCi\Camunda-Workers\data\postgres\final"
$statsFile = Join-Path $directory "franchise_stats.csv"
$franchiseFile = Join-Path $directory "franchises.csv"

# Get all franchise IDs
$franchiseContent = Import-Csv $franchiseFile
$franchiseIds = $franchiseContent.id

# Get existing stats franchise IDs
$statsContent = Import-Csv $statsFile
$statsIds = $statsContent.franchise_id

# Find missing
$missingIds = $franchiseIds | Where-Object { $statsIds -notcontains $_ }

foreach ($id in $missingIds) {
    $newId = [guid]::NewGuid().ToString()
    $createdAt = "2026-01-10 12:02:52"
    $updatedAt = "2026-01-10 12:02:52"
    
    # Construct CSV line
    # id,franchise_id,rating,rating_count,follow_count,likes_count,view_count,save_count,share_count,enquiry_count,news_count,created_at,updated_at
    $line = "$newId,$id,,0,0,0,0,0,0,0,0,$createdAt,$updatedAt"
    
    Add-Content -Path $statsFile -Value $line
    Write-Host "Added missing record for franchise_id: $id"
}
