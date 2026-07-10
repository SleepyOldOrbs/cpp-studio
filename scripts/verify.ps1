param(
  [switch]$IncludeLocalAudio
)

$ErrorActionPreference = "Stop"
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")

$files = gofmt -l .\cmd .\internal
if ($files) {
  $files
  throw "gofmt reported unformatted files"
}

go test ./... -count=1
go vet ./...
go run .\cmd\cpp-studio --config .\config.ci.json --check
go run .\cmd\cpp-studio --config .\config.smoke.json --check
.\scripts\smoke-demo-ui.ps1

if ($IncludeLocalAudio) {
  go run .\cmd\cpp-studio --config .\config.audio-local.example.json --check
}
