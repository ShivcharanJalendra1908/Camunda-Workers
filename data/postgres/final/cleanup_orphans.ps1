$directory = "c:\Users\lenovo\Desktop\LeMiCi\Camunda-Workers\data\postgres\final"
$orphansToRemove = @(
    "ec3f96f0-08fb-4e9f-8c7f-890d7128c6b5",
    "19f4d529-70a5-4231-8055-fec1caeaa905"
)

$filesToClean = @(
    "franchise_business_overview.csv",
    "franchise_categories.csv",
    "franchise_investment_requirement.csv",
    "franchise_operations.csv"
)

foreach ($fileName in $filesToClean) {
    $filePath = Join-Path $directory $fileName
    if (Test-Path $filePath) {
        Write-Host "Cleaning $fileName..."
        $content = Import-Csv -Path $filePath
        $originalCount = $content.Count
        
        $cleanedContent = $content | Where-Object { $orphansToRemove -notcontains $_.franchise_id }
        $newCount = $cleanedContent.Count
        
        if ($originalCount -ne $newCount) {
            $cleanedContent | Export-Csv -Path $filePath -NoTypeInformation -Encoding UTF8
            $removedCount = $originalCount - $newCount
            Write-Host "  Removed $removedCount records. New count: $newCount" -ForegroundColor Green
        } else {
            Write-Host "  No orphans found in $fileName." -ForegroundColor Gray
        }
    }
}
