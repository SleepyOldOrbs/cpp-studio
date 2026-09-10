param(
  [switch]$IncludeLocalAudio
)

$ErrorActionPreference = "Stop"
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")

$files = gofmt -l .\catalog.go .\cmd .\internal
if ($LASTEXITCODE -ne 0) { throw "gofmt failed with exit code $LASTEXITCODE" }
if ($files) {
  $files
  throw "gofmt reported unformatted files"
}

go test ./... -count=1
if ($LASTEXITCODE -ne 0) { throw "Native command failed with exit code $LASTEXITCODE" }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "Native command failed with exit code $LASTEXITCODE" }
node --test ./scripts/test-story-builder-state.cjs ./scripts/test-studio-ux.cjs
if ($LASTEXITCODE -ne 0) { throw "Native command failed with exit code $LASTEXITCODE" }
go run .\cmd\cpp-studio --config .\config.ci.json --check
if ($LASTEXITCODE -ne 0) { throw "Native command failed with exit code $LASTEXITCODE" }
go run .\cmd\cpp-studio --config .\config.smoke.json --check
if ($LASTEXITCODE -ne 0) { throw "Native command failed with exit code $LASTEXITCODE" }
.\scripts\test-benchmark-story-local.ps1 | Out-Null
.\scripts\smoke-demo-ui.ps1

if ($IncludeLocalAudio) {
  go run .\cmd\cpp-studio --config .\config.audio-local.example.json --check
  if ($LASTEXITCODE -ne 0) { throw "Native command failed with exit code $LASTEXITCODE" }
}
