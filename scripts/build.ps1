$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$Version = if ($env:VERSION) { $env:VERSION } else { '0.1.0-dev' }
New-Item -ItemType Directory -Force dist | Out-Null
$oldCGO, $oldOS, $oldArch = $env:CGO_ENABLED, $env:GOOS, $env:GOARCH
try {
    $env:CGO_ENABLED = '0'
    foreach ($Target in @('linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64', 'windows/arm64')) {
        $env:GOOS, $env:GOARCH = $Target.Split('/')
        $Extension = if ($env:GOOS -eq 'windows') { '.exe' } else { '' }
        Write-Host "Building $Target"
        go build -mod=vendor -trimpath -ldflags "-s -w -X steamcli.local/steam/internal/cli.Version=$Version" -o "dist/steamcli-$($env:GOOS)-$($env:GOARCH)$Extension" ./cmd/steamcli
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $Target" }
    }
} finally {
    $env:CGO_ENABLED, $env:GOOS, $env:GOARCH = $oldCGO, $oldOS, $oldArch
}
Copy-Item docs/DEPENDENCY_LICENSES.txt dist/THIRD_PARTY_LICENSES.txt
$Checksums = Get-ChildItem dist/steamcli-* | Sort-Object Name | ForEach-Object {
    $Hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    "$Hash  $($_.Name)"
}
$Checksums | Set-Content -Encoding ascii dist/SHA256SUMS
