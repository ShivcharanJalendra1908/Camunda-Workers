$dataDir = "data\postgres\new_extracted"

# 1. franchises.csv
$franchisesPath = "$dataDir\franchises.csv"
$lines = Get-Content $franchisesPath
$mf_id = "m0000000-0000-0000-0000-000000000001"
$exists = $false
foreach ($line in $lines) {
    if ($line.StartsWith($mf_id)) { $exists = $true; break }
}
if (-not $exists) {
    $newRow = '"m0000000-0000-0000-0000-000000000001","Brew & Blend Master Franchise","brew-blend-master","Premier Master Franchise Opportunity for Coffee and Bakery Chain","Brew & Blend is a premier coffee and bakery brand offering exclusive regional/state master franchise rights. Build and support sub-franchises in your region.",2018,TRUE,TRUE,15,"10-20","Brew & Blend Foods Pvt Ltd","Master Franchise",2018,15,"Rohan Mehra","Franchise Director","master@brewblend.com","/FranchiseHomePage/FranchiseLogos/Circle/esspresso_bar_co_circle.jpg","/FranchiseHomePage/FranchiseLogos/Square/esspresso_bar_co_square.jpg","00000000-0000-0000-0000-000000000001","","2026-1-10 12:02","2026-1-10 12:02","master_franchise","live","{}",0,0.00,0.00,"2026-01-10 12:02:00"'
    Add-Content $franchisesPath $newRow
    Write-Host "Added master franchise to franchises.csv"
} else {
    Write-Host "Master franchise already exists in franchises.csv"
}

# 2. franchise_categories.csv
$categoriesPath = "$dataDir\franchise_categories.csv"
$lines = Get-Content $categoriesPath
$fc_id = "fc000000-0000-0000-0000-000000009999"
$exists = $false
foreach ($line in $lines) {
    if ($line.StartsWith($fc_id)) { $exists = $true; break }
}
if (-not $exists) {
    $newRow = 'fc000000-0000-0000-0000-000000009999,m0000000-0000-0000-0000-000000000001,8dcc57a0-79ac-45dd-9db0-efb3d1fd5299,f5aa12c0-a668-4178-b870-908ba1c7ac67,TRUE,2025-01-01 00:47:58'
    Add-Content $categoriesPath $newRow
    Write-Host "Added mapping to franchise_categories.csv"
} else {
    Write-Host "Mapping already exists in franchise_categories.csv"
}

# 3. franchise_stats.csv
$statsPath = "$dataDir\franchise_stats.csv"
$lines = Get-Content $statsPath
$stat_id = "4fa2cca6-3660-478c-a1c4-3c087c9f9999"
$exists = $false
foreach ($line in $lines) {
    if ($line.StartsWith($stat_id)) { $exists = $true; break }
}
if (-not $exists) {
    $newRow = '4fa2cca6-3660-478c-a1c4-3c087c9f9999,m0000000-0000-0000-0000-000000000001,4.8,12,140,95,1200,45,30,5,1,2026-01-10 12:02:52,2026-01-10 12:02:52'
    Add-Content $statsPath $newRow
    Write-Host "Added stats to franchise_stats.csv"
} else {
    Write-Host "Stats already exist in franchise_stats.csv"
}

# 4. franchise_investment_requirement.csv
$invPath = "$dataDir\franchise_investment_requirement.csv"
$lines = [System.IO.File]::ReadAllLines($invPath)
$header = $lines[0]
if ($header -notlike "*revenue_model*") {
    $lines[0] = $header + ",revenue_model"
    for ($i = 1; $i -lt $lines.Length; $i++) {
        if ($lines[$i] -ne "") {
            $lines[$i] = $lines[$i] + ","
        }
    }
    [System.IO.File]::WriteAllLines($invPath, $lines)
    Write-Host "Added revenue_model column to franchise_investment_requirement.csv"
}
$lines = Get-Content $invPath
$inv_id = "0c41acfa-7c46-4b85-a3d1-167d46519999"
$exists = $false
foreach ($line in $lines) {
    if ($line.StartsWith($inv_id)) { $exists = $true; break }
}
if (-not $exists) {
    $newRow = '"0c41acfa-7c46-4b85-a3d1-167d46519999","m0000000-0000-0000-0000-000000000001",5000000,10000000,1500000,8.5,2.0,18,24,25.00,35.00,800000,1500000,2000000,3000000,"Regional brand rights, initial training, grand opening support, and setup manuals.","00000000-0000-0000-0000-000000000001","","2025-1-1 18:15","2025-1-1 18:15","{""payback_period"": ""18-24 months"", ""performance_bonuses"": ""10% bonus on exceeding sub-unit target"", ""roi_calculator_inputs"": {""avg_unit_revenue"": 500000, ""royalty_percentage"": 8.5, ""target_sub_units"": 5, ""territory_size_sqft"": 10000, ""master_royalty_share_percentage"": 50.0}}"'
    Add-Content $invPath $newRow
    Write-Host "Added investment details to franchise_investment_requirement.csv"
} else {
    Write-Host "Investment details already exist in franchise_investment_requirement.csv"
}

# 5. franchise_operations.csv
$opsPath = "$dataDir\franchise_operations.csv"
$lines = [System.IO.File]::ReadAllLines($opsPath)
$header = $lines[0]
$newFields = @("territory_details", "development_schedule", "support_training", "legal_compliance")
$addedAny = $false
foreach ($fld in $newFields) {
    if ($header -notlike "*$fld*") {
        $header = $header + ",$fld"
        for ($i = 1; $i -lt $lines.Length; $i++) {
            if ($lines[$i] -ne "") {
                $lines[$i] = $lines[$i] + ","
            }
        }
        $addedAny = $true
    }
}
if ($addedAny) {
    $lines[0] = $header
    [System.IO.File]::WriteAllLines($opsPath, $lines)
    Write-Host "Added operations JSONB columns to franchise_operations.csv"
}
$lines = Get-Content $opsPath
$ops_id = "5068fe2d-be5a-4b5c-9d92-82677f0f9999"
$exists = $false
foreach ($line in $lines) {
    if ($line.StartsWith($ops_id)) { $exists = $true; break }
}
if (-not $exists) {
    $newRow = '"5068fe2d-be5a-4b5c-9d92-82677f0f9999","m0000000-0000-0000-0000-000000000001",1200,2500,"Commercial High Street",4,8,"[{""role"": ""Manager"", ""count"": 1}, {"role"": ""Barista"", ""count"": 4}, {"role"": ""Cashier"", ""count"": 1}]","09:00 - 23:00",TRUE,"Comprehensive 2-week training at HQ for Master Franchise owner and staff.","POS billing machine with internet and printer.","Local marketing campaigns, social media kit, and billboard designs.","Metros and Tier-1 cities","Prior retail/restaurant management experience.",TRUE,TRUE,"00000000-0000-0000-0000-000000000001","","2024-1-15 19:03","2024-01-15 03:19:05.00","{""scope"": ""State-wide exclusivity"", ""exclusivity_terms"": ""Exclusive regional rights to open up to 10 sub-units"", ""available_territories"": [""North India"", ""West India""], ""taken_territories"": [""South India""]}","{""obligations"": ""Must open minimum 5 units within first 3 years"", ""timeline_months"": 36, ""target_units"": 5}","{""brand_toolkits"": ""Full advertising and marketing assets package"", ""operational_manuals"": ""Operations, recipe, and service guidelines manuals"", ""training_duration_days"": 14}","{""agreement_term_years"": 10, ""renewal_term_years"": 5, ""regulatory_licences"": [""FSSAI"", ""Trade License"", ""GST registration""]}"'
    Add-Content $opsPath $newRow
    Write-Host "Added operations details to franchise_operations.csv"
} else {
    Write-Host "Operations details already exist in franchise_operations.csv"
}

Write-Host "All CSV seeding operations completed successfully!"
