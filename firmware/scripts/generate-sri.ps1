<#
.SYNOPSIS
    Generate SRI (Subresource Integrity) hashes for ANCF firmware components.

.DESCRIPTION
    Reads all .js files from the dist/ directory, computes their SHA-384 hash,
    and outputs SRI integrity strings in the format sha384-<base64hash>.
    Also generates a firmware-manifest.json file compatible with the
    ANCF Discovery Manifest ui_firmware.components field.

.PARAMETER DistDir
    Path to the dist/ directory containing compiled .js files.
    Default: ../components/dist (relative to this script)

.PARAMETER OutputFile
    Path to write the firmware-manifest.json output file.
    Default: $DistDir/firmware-manifest.json

.EXAMPLE
    .\generate-sri.ps1
    .\generate-sri.ps1 -DistDir "..\components\dist" -OutputFile "manifest.json"

.NOTES
    Requires PowerShell 5.1 or later on Windows.
    For Linux/macOS, use the generate-sri.sh script instead.
#>

param(
    [string]$DistDir = $null,
    [string]$OutputFile = $null
)

$ErrorActionPreference = 'Stop'

# Resolve paths
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
if (-not $DistDir) {
    $DistDir = Join-Path (Join-Path $ScriptDir '..') 'components\dist'
}
$DistDir = [System.IO.Path]::GetFullPath($DistDir)

if (-not $OutputFile) {
    $OutputFile = Join-Path $DistDir 'firmware-manifest.json'
}

Write-Host "ANCF Firmware SRI Generator" -ForegroundColor Green
Write-Host "===========================" -ForegroundColor Green
Write-Host "Dist directory: $DistDir"
Write-Host "Output file:    $OutputFile"
Write-Host ""

if (-not (Test-Path $DistDir)) {
    Write-Error "Dist directory not found: $DistDir"
    Write-Host "Run 'tsc' first to compile TypeScript components." -ForegroundColor Yellow
    exit 1
}

# Find all compiled .js files
$jsFiles = Get-ChildItem -Path $DistDir -Filter '*.js' -File | Sort-Object Name
if ($jsFiles.Count -eq 0) {
    Write-Error "No .js files found in $DistDir"
    Write-Host "Run 'tsc' first to compile TypeScript components." -ForegroundColor Yellow
    exit 1
}

Write-Host "Found $($jsFiles.Count) component file(s):" -ForegroundColor Cyan
$manifestComponents = @()

foreach ($file in $jsFiles) {
    Write-Host "  Processing: $($file.Name) ..." -NoNewline

    try {
        # Read file bytes
        $bytes = [System.IO.File]::ReadAllBytes($file.FullName)

        # Compute SHA-384 hash
        $sha384 = [System.Security.Cryptography.SHA384]::Create()
        $hashBytes = $sha384.ComputeHash($bytes)
        $base64Hash = [System.Convert]::ToBase64String($hashBytes)

        # Compute SHA-256 for short content hash (first 12 hex chars)
        $sha256 = [System.Security.Cryptography.SHA256]::Create()
        $sha256Bytes = $sha256.ComputeHash($bytes)
        $shortHash = [System.BitConverter]::ToString($sha256Bytes).Replace('-', '').ToLower().Substring(0, 12)

        # Build SRI string
        $sri = "sha384-$base64Hash"

        Write-Host " SRI: $sri" -ForegroundColor White
        Write-Host "    Short hash: $shortHash" -ForegroundColor DarkGray
        Write-Host "    Size: $($bytes.Length) bytes" -ForegroundColor DarkGray

        # Build component entry
        $baseName = $file.BaseName
        $hashName = "$baseName.$shortHash.js"

        $manifestComponents += @{
            name       = $baseName
            url        = "https://cdn.yourshop.com/firmware/v1/$hashName"
            integrity  = $sri
            type       = 'module'
            size_bytes = $bytes.Length
            short_hash = $shortHash
        }

        # Copy file with hash-named filename
        $hashPath = Join-Path $DistDir $hashName
        Copy-Item -Path $file.FullName -Destination $hashPath -Force
        Write-Host "    Copied to: $hashName" -ForegroundColor DarkGray

    } catch {
        Write-Host " ERROR: $_" -ForegroundColor Red
    } finally {
        if ($sha384) { $sha384.Dispose() }
        if ($sha256) { $sha256.Dispose() }
    }
}

# Generate firmware manifest
$manifest = @{
    generated_at = [DateTime]::UtcNow.ToString('o')
    components   = $manifestComponents
}

$manifestJson = $manifest | ConvertTo-Json -Depth 4
Set-Content -Path $OutputFile -Value $manifestJson -Encoding UTF8

Write-Host ""
Write-Host "Firmware manifest written to: $OutputFile" -ForegroundColor Green
Write-Host ""

# Print summary table
Write-Host "SRI Integrity Hashes:" -ForegroundColor Cyan
Write-Host ("{0,-30} {1}" -f 'Component', 'SRI (sha384)')
Write-Host ("{0,-30} {1}" -f '---------', '--------------')
foreach ($comp in $manifestComponents) {
    $sriShort = $comp.integrity.Substring(0, [Math]::Min(50, $comp.integrity.Length)) + '...'
    Write-Host ("{0,-30} {1}" -f "$($comp.name).js", $sriShort)
}

Write-Host ""
Write-Host "Done. Copy the integrity values into your manifest's ui_firmware.components[].integrity fields." -ForegroundColor Yellow
