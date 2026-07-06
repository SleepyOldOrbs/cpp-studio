param(
  [string]$Runtime = "",
  [string]$OutDir = "dist"
)

$ErrorActionPreference = "Stop"
$env:Path = $env:Path + ";" + [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")

function Get-DefaultRuntime {
  $goHostOS = ""
  $goHostArch = ""
  try {
    $goHostOS = (go env GOHOSTOS).Trim()
    $goHostArch = (go env GOHOSTARCH).Trim()
  } catch {
    $goHostOS = ""
    $goHostArch = ""
  }

  if ($goHostOS -and $goHostArch) {
    return "$goHostOS-$goHostArch"
  }

  if ($IsWindows -or $env:OS -eq "Windows_NT") {
    return "windows-amd64"
  }
  if ($IsLinux) {
    return "linux-amd64"
  }
  if ($IsMacOS) {
    return "darwin-amd64"
  }
  return "local-amd64"
}

function Copy-RequiredFile {
  param(
    [string]$Path,
    [string]$Destination
  )

  if (-not (Test-Path $Path)) {
    throw "missing required package file: $Path"
  }
  Copy-Item -LiteralPath $Path -Destination $Destination
}

if (-not $Runtime) {
  $Runtime = Get-DefaultRuntime
}

$hostRuntime = Get-DefaultRuntime
if ($Runtime -ne $hostRuntime) {
  throw "package-release builds native packages only: requested runtime $Runtime does not match host runtime $hostRuntime"
}

$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $root

$packageName = "cpp-studio-$Runtime"
$distRoot = Join-Path $root $OutDir
$packageDir = Join-Path $distRoot $packageName

if (Test-Path $packageDir) {
  Remove-Item -LiteralPath $packageDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $packageDir | Out-Null

$exe = ""
if ($Runtime.StartsWith("windows")) {
  $exe = ".exe"
}

$gatewayPath = Join-Path $packageDir "cpp-studio$exe"
$fixturePath = Join-Path $packageDir "cpp-studio-fixture$exe"

go build -trimpath -o $gatewayPath ./cmd/cpp-studio
go build -trimpath -o $fixturePath ./cmd/cpp-studio-fixture

Copy-RequiredFile -Path "./README.md" -Destination $packageDir
Copy-RequiredFile -Path "./config.audio-local.example.json" -Destination $packageDir
Copy-RequiredFile -Path "./config.example.json" -Destination $packageDir
Copy-RequiredFile -Path "./config.ci.json" -Destination $packageDir
Copy-RequiredFile -Path "./config.smoke.json" -Destination $packageDir

Copy-Item -LiteralPath "./docs" -Destination (Join-Path $packageDir "docs") -Recurse

$manifest = [ordered]@{
  name = "cpp-studio"
  runtime = $Runtime
  createdAt = (Get-Date).ToUniversalTime().ToString("o")
  binaries = @("cpp-studio$exe", "cpp-studio-fixture$exe")
  configs = @("config.audio-local.example.json", "config.example.json", "config.ci.json", "config.smoke.json")
  docs = @(
    "README.md",
    "docs/API.md",
    "docs/CONFIG.md",
    "docs/FIXTURE.md",
    "docs/RELEASE.md",
    "docs/STORY_API.md",
    "docs/story-packets/how-stars-are-born/cast.json",
    "docs/story-packets/how-stars-are-born/fact-cards.json",
    "docs/story-packets/how-stars-are-born/script.md",
    "docs/story-packets/how-stars-are-born/sources.md"
  )
}
$manifest | ConvertTo-Json -Depth 4 | Set-Content -Encoding UTF8 -Path (Join-Path $packageDir "PACKAGE.json")

New-Item -ItemType Directory -Force -Path $distRoot | Out-Null
if ($Runtime.StartsWith("windows")) {
  $archive = Join-Path $distRoot "$packageName.zip"
  if (Test-Path $archive) {
    Remove-Item -LiteralPath $archive -Force
  }
  Push-Location $distRoot
  try {
    Compress-Archive -Path $packageName -DestinationPath $archive
  } finally {
    Pop-Location
  }
} else {
  $archive = Join-Path $distRoot "$packageName.tar.gz"
  if (Test-Path $archive) {
    Remove-Item -LiteralPath $archive -Force
  }
  tar -czf $archive -C $distRoot $packageName
}

[pscustomobject]@{
  runtime = $Runtime
  packageDir = $packageDir
  archive = $archive
} | ConvertTo-Json
