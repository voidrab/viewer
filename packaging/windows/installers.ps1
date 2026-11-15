# installers.ps1 builds an installer from each portable zip in dist\, as
# dist\viewrab-<version>-windows-<arch>-setup.exe. It runs on Windows, in CI
# and for releases; it installs Inno Setup with Chocolatey when it is missing.
$ErrorActionPreference = 'Stop'
$root = Resolve-Path "$PSScriptRoot\..\.."
$dist = Join-Path $root 'dist'

$iscc = (Get-Command iscc -ErrorAction SilentlyContinue).Source
$known = @("${env:ProgramFiles}\Inno Setup 7\ISCC.exe", "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe")
if (-not $iscc) { $iscc = $known | Where-Object { Test-Path $_ } | Select-Object -First 1 }
if (-not $iscc) {
    choco install innosetup -y --no-progress
    $iscc = $known | Where-Object { Test-Path $_ } | Select-Object -First 1
}
if (-not $iscc) { throw 'Inno Setup''s ISCC.exe was not found' }

$zips = Get-ChildItem "$dist\viewrab-*-windows-*.zip"
if (-not $zips) { throw "no portable zips in $dist" }
foreach ($zip in $zips) {
    if ($zip.BaseName -notmatch '^viewrab-(.+)-windows-(amd64|arm64)$') { throw "unexpected zip $($zip.Name)" }
    $version, $arch = $Matches[1], $Matches[2]
    Expand-Archive $zip.FullName -DestinationPath $dist -Force
    & $iscc /Q "/DVersion=$version" "/DArch=$arch" (Join-Path $root 'packaging\windows\viewrab.iss')
    if ($LASTEXITCODE -ne 0) { throw "iscc failed for $($zip.Name)" }
    Remove-Item -Recurse -Force (Join-Path $dist $zip.BaseName)
}
Get-ChildItem "$dist\*-setup.exe" | Format-Table Name, Length
