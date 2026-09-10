param([string]$ModelsRoot = (Join-Path $PSScriptRoot '../models'))
$ErrorActionPreference = 'Stop'
$manifest = Get-Content (Join-Path $PSScriptRoot '../models.json') -Raw | ConvertFrom-Json
$ids = @('index-tts2.5', 'higgs-audio', 'fireredtts3-base', 'fireredtts3-instruct')
$rootPath = [IO.Path]::GetFullPath($ModelsRoot)
foreach ($id in $ids) {
  $model = $manifest.models | Where-Object id -eq $id
  if (-not $model -or -not $model.sha256 -or -not $model.downloadUrl) { throw "Missing pinned package for $id" }
  $target = [IO.Path]::GetFullPath((Join-Path $rootPath $model.path))
  if (-not $target.StartsWith($rootPath.TrimEnd('\','/') + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Model path escapes models root' }
  if (-not (Test-Path -LiteralPath $target)) {
    New-Item -ItemType Directory -Force ([IO.Path]::GetDirectoryName($target)) | Out-Null
    $partial = $target + '.part'
    Write-Host "Downloading $($model.displayName)"
    for ($attempt = 1; $attempt -le 8; $attempt++) {
      & curl.exe --fail --location --continue-at - --output $partial $model.downloadUrl
      if ($LASTEXITCODE -eq 0) { break }
      if ($attempt -lt 8) { Start-Sleep -Seconds 3 }
    }
    if ($LASTEXITCODE -ne 0) { throw "Download failed for $id; rerun to resume" }
    $candidate = $partial
  } else { $candidate = $target }
  if ((Get-Item -LiteralPath $candidate).Length -ne $model.bytes -or (Get-FileHash -LiteralPath $candidate -Algorithm SHA256).Hash -ne $model.sha256) {
    throw "Verification failed for $id; file preserved at $candidate"
  }
  if ($candidate -ne $target) { Move-Item -LiteralPath $candidate -Destination $target }
  Write-Host "Verified $($model.displayName)"
}
