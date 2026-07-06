param(
  [string]$Config = ".\config.audio-local.example.json",
  [string]$Text = "Hello from cpp-studio.",
  [string]$Out = ".\out\speech-route.wav"
)

$ErrorActionPreference = "Stop"
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")

New-Item -ItemType Directory -Force -Path .\bin | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Out) | Out-Null

$exe = ".\bin\cpp-studio.exe"
go build -o $exe .\cmd\cpp-studio

$server = Start-Process -WindowStyle Hidden -PassThru -FilePath $exe -ArgumentList @("--config", $Config)
try {
  $health = $null
  for ($i = 0; $i -lt 80 -and $null -eq $health; $i++) {
    try {
      $health = Invoke-RestMethod http://127.0.0.1:8765/health
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if ($null -eq $health -or $health.status -ne "ready") {
    throw "gateway did not become ready"
  }

  $body = @{ input = $Text; voice = "default"; format = "wav" } | ConvertTo-Json
  Invoke-WebRequest -Uri http://127.0.0.1:8765/v1/audio/speech -Method Post -ContentType "application/json" -Body $body -OutFile $Out
  py -3.11 -c "import wave, pathlib; p=pathlib.Path(r'$Out'); w=wave.open(str(p),'rb'); print({'bytes': p.stat().st_size, 'channels': w.getnchannels(), 'sample_width_bytes': w.getsampwidth(), 'sample_rate': w.getframerate(), 'frames': w.getnframes(), 'duration_seconds': round(w.getnframes()/w.getframerate(), 3)})"
} finally {
  if ($server -and -not $server.HasExited) {
    Stop-Process -Id $server.Id -Force
  }
}
