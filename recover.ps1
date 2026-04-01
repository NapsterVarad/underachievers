$historyPath = "C:\Users\varad\AppData\Roaming\Cursor\User\History"
$projectPath = "d:\projects antigravity\underachievers\delta-altcoin-scalper"
Write-Host "Scanning $historyPath for deleted files..."

$entries = Get-ChildItem -Path $historyPath -Recurse -Filter entries.json -ErrorAction SilentlyContinue

$foundFiles = @{}

foreach ($entry in $entries) {
    try {
        $raw = Get-Content $entry.FullName -Raw
        $json = $raw | ConvertFrom-Json -ErrorAction Stop
        
        $origPath = $json.resource
        
        if ($origPath -match "delta-altcoin-scalper" -or $origPath -match "delta-altcoin-scaler") {
            # Remove protocol and unescape URL
            $normalizedPath = $origPath -replace '^file:\/\/\/', '' -replace '%3A', ':' -replace '%20', ' ' -replace '/', '\'
            
            # Get the last saved entry for this file
            $latestEntry = $null
            if ($json.entries -and $json.entries.Count -gt 0) {
                $latestEntry = $json.entries[-1]
            }
            
            if ($latestEntry) {
                $contentFilePath = Join-Path $entry.DirectoryName $latestEntry.id
                $timestamp = $latestEntry.timestamp
                
                if (-not $foundFiles.ContainsKey($normalizedPath) -or $foundFiles[$normalizedPath].timestamp -lt $timestamp) {
                    $foundFiles[$normalizedPath] = @{
                        contentFilePath = $contentFilePath
                        timestamp = $timestamp
                    }
                }
            }
        }
    } catch {
        # Ignore parse errors
    }
}

Write-Host "Found $($foundFiles.Count) files..."

$recoveredCount = 0
foreach ($key in $foundFiles.Keys) {
    if (-not (Test-Path $key)) {
        Write-Host "Restoring: $key"
        $destFile = $key
        $destDir = Split-Path $destFile
        if (-not (Test-Path $destDir)) {
            New-Item -ItemType Directory -Force -Path $destDir | Out-Null
        }
        Copy-Item -Path $foundFiles[$key].contentFilePath -Destination $destFile -Force
        $recoveredCount++
    }
}
Write-Host "Restored $recoveredCount files."
