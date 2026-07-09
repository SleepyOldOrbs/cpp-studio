param(
  [int]$GatewayPort = 8777,
  [int]$LlamaPort = 8798,
  [string]$OutDir = ".\out\demo-ui-smoke"
)

# Exercises every API flow the browser demo (internal/demo/static/app.js)
# makes, against the deterministic fixture engines: demo assets, health,
# transcription, the voice loop with conversation history, image generation,
# and a fixed-voice story with a stitched WAV artifact.

$ErrorActionPreference = "Stop"
$env:Path = $env:Path + ";" + [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")

function Assert-PortFree {
  param([int]$Port, [string]$Label)

  $connections = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue |
    Where-Object { $_.State -eq "Listen" -or $_.State -eq 2 }
  if ($connections) {
    $owners = ($connections | Select-Object -ExpandProperty OwningProcess -Unique) -join ", "
    throw "$Label port $Port is already in use by process id(s): $owners"
  }
}

function Stop-FixtureListener {
  param([int]$Port, [string]$ExpectedPath)

  $connections = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue |
    Where-Object { $_.State -eq "Listen" -or $_.State -eq 2 }
  foreach ($connection in $connections) {
    try {
      $process = Get-Process -Id $connection.OwningProcess -ErrorAction Stop
      $pathMatches = $false
      if ($ExpectedPath -and $process.Path) {
        $pathMatches = ([System.IO.Path]::GetFullPath($process.Path) -eq [System.IO.Path]::GetFullPath($ExpectedPath))
      }
      if ($pathMatches -or $process.ProcessName -like "cpp-studio-fixture*") {
        Stop-Process -Id $process.Id -Force
      }
    } catch {
      # Best effort cleanup for a fixture-only smoke port.
    }
  }
}

function Assert-WavBytes {
  param([byte[]]$Bytes, [string]$Label)

  if ($Bytes.Length -lt 44) {
    throw "$Label WAV is too small: $($Bytes.Length) bytes"
  }
  $riff = [System.Text.Encoding]::ASCII.GetString($Bytes, 0, 4)
  $wave = [System.Text.Encoding]::ASCII.GetString($Bytes, 8, 4)
  if ($riff -ne "RIFF" -or $wave -ne "WAVE") {
    throw "$Label does not have a RIFF/WAVE header"
  }
}

New-Item -ItemType Directory -Force -Path .\bin | Out-Null
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$gatewayExe = ".\bin\cpp-studio.exe"
$fixtureExe = ".\bin\cpp-studio-fixture.exe"
$configPath = Join-Path $OutDir "config.demo-ui.json"
$inputWav = Join-Path $OutDir "input.wav"

go build -o $gatewayExe .\cmd\cpp-studio
go build -o $fixtureExe .\cmd\cpp-studio-fixture

$fixtureCommand = (Resolve-Path $fixtureExe).Path
Assert-PortFree -Port $GatewayPort -Label "gateway"
Stop-FixtureListener -Port $LlamaPort -ExpectedPath $fixtureCommand
Assert-PortFree -Port $LlamaPort -Label "fixture llama"
& $fixtureExe speech --text "fixture input" --out $inputWav

$config = [ordered]@{
  gateway = [ordered]@{
    host = "127.0.0.1"
    port = $GatewayPort
  }
  engines = [ordered]@{
    llama = [ordered]@{
      command = $fixtureCommand
      args = @("server", "--host", "127.0.0.1", "--port", "$LlamaPort")
      mode = "server"
      healthUrl = "http://127.0.0.1:$LlamaPort/health"
      startupTimeoutSeconds = 10
      shutdownTimeoutSeconds = 5
      requestTimeoutSeconds = 30
    }
    whisper = [ordered]@{
      command = $fixtureCommand
      args = @("whisper")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
    audio = [ordered]@{
      command = $fixtureCommand
      args = @("speech")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
    sd = [ordered]@{
      command = $fixtureCommand
      args = @("image")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
  }
}
$config | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 -Path $configPath

$base = "http://127.0.0.1:$GatewayPort"
$server = Start-Process -WindowStyle Hidden -PassThru -FilePath (Resolve-Path $gatewayExe).Path -ArgumentList @("--config", (Resolve-Path $configPath).Path)
$llamaPid = $null
try {
  $health = $null
  for ($i = 0; $i -lt 80 -and ($null -eq $health -or $health.status -ne "ready"); $i++) {
    if ($server.HasExited) {
      throw "gateway exited before becoming ready with exit code $($server.ExitCode)"
    }
    try {
      $health = Invoke-RestMethod "$base/health"
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if ($null -eq $health -or $health.status -ne "ready") {
    throw "gateway did not become ready"
  }
  $llamaPid = $health.engines.llama.pid

  # Demo assets, exactly as the browser fetches them.
  $index = Invoke-WebRequest -Uri "$base/demo/" -UseBasicParsing
  if ($index.Content -notlike "*cpp-studio local studio*") {
    throw "demo index is missing its title marker"
  }
  $appJs = Invoke-WebRequest -Uri "$base/demo/app.js" -UseBasicParsing
  if ($appJs.Content -notlike "*refreshStoryLibrary*") {
    throw "demo app.js is missing its marker"
  }
  $css = Invoke-WebRequest -Uri "$base/demo/styles.css" -UseBasicParsing
  if ($css.Content -notlike "*.story-library-item*") {
    throw "demo styles.css is missing its marker"
  }

  # Transcription (live-transcribe request path).
  $transcription = Invoke-RestMethod -Uri "$base/v1/audio/transcriptions" -Method Post -Form @{ file = Get-Item $inputWav }
  if ($transcription.text -ne "fixture transcript") {
    throw "unexpected transcript: $($transcription.text)"
  }

  # Voice loop with typed message plus conversation history.
  $history = '[{"role":"user","text":"earlier question"},{"role":"assistant","text":"earlier answer"}]'
  $voice = Invoke-RestMethod -Uri "$base/v1/voice" -Method Post -Form @{ message = "follow-up question"; history = $history }
  if (-not $voice.reply -or -not $voice.audio_b64) {
    throw "voice loop response missing reply or audio: $($voice | ConvertTo-Json -Depth 4)"
  }
  Assert-WavBytes -Bytes ([Convert]::FromBase64String($voice.audio_b64)) -Label "voice reply"

  # Bad history must be rejected.
  try {
    Invoke-RestMethod -Uri "$base/v1/voice" -Method Post -Form @{ message = "x"; history = "not-json" } | Out-Null
    throw "expected bad history to be rejected"
  } catch {
    if ($_.Exception.Response.StatusCode.value__ -ne 400) {
      throw "expected 400 for bad history, got: $_"
    }
  }

  # Image generation.
  $imageBody = @{ prompt = "fixture image"; size = "64x64"; response_format = "b64_json" } | ConvertTo-Json
  $image = Invoke-RestMethod -Uri "$base/v1/images/generations" -Method Post -ContentType "application/json" -Body $imageBody
  $png = [Convert]::FromBase64String($image.data[0].b64_json)
  $pngSignature = [byte[]](0x89, 0x50, 0x4e, 0x47)
  for ($i = 0; $i -lt 4; $i++) {
    if ($png[$i] -ne $pngSignature[$i]) {
      throw "image payload is not a PNG"
    }
  }

  # Fixed-voice story: every line synthesized through the audio engine and
  # stitched into one WAV artifact.
  $storyBody = [ordered]@{
    subject = "how stars are born"
    target_seconds = 90
    source_mode = "curated"
    voice_mode = "fixed"
    sources = @(
      [ordered]@{ id = "src-1"; title = "NASA Science: Star Basics"; url = "https://science.nasa.gov/universe/stars/"; excerpt = "Stars form inside molecular clouds of gas and dust. Cold cloud conditions help gas clump into denser pockets. As clumps gain mass, gravity can make them collapse." },
      [ordered]@{ id = "src-2"; title = "NASA Webb: Fiery Hourglass"; url = "https://science.nasa.gov/missions/webb/"; excerpt = "A forming protostar gathers material from its surrounding molecular cloud. Falling material spirals inward and forms an accretion disk. The disk feeds material onto the protostar." },
      [ordered]@{ id = "src-3"; title = "NASA Hubble: Planet-Forming Disks"; url = "https://science.nasa.gov/missions/hubble/"; excerpt = "Some falling material forms a rotating disk around the protostar. Jets from magnetic poles are part of star formation. Jets help carry away angular momentum so material can continue collecting." }
    )
  } | ConvertTo-Json -Depth 8
  $created = Invoke-RestMethod -Uri "$base/v1/stories" -Method Post -ContentType "application/json" -Body $storyBody

  $status = $null
  $deadline = (Get-Date).AddSeconds(30)
  do {
    Start-Sleep -Milliseconds 250
    $status = Invoke-RestMethod "$base/v1/stories/$($created.id)"
    if ($status.status -eq "failed") {
      throw "story failed: $($status | ConvertTo-Json -Depth 8)"
    }
  } until ($status.status -eq "complete" -or (Get-Date) -gt $deadline)
  if ($status.status -ne "complete") {
    throw "story did not complete: $($status | ConvertTo-Json -Depth 8)"
  }

  $storyWav = Invoke-WebRequest -Uri "$base$($status.artifact_url)" -UseBasicParsing
  Assert-WavBytes -Bytes $storyWav.Content -Label "story artifact"

  $list = Invoke-RestMethod "$base/v1/stories"
  if (-not $list.stories -or -not ($list.stories | Where-Object { $_.id -eq $created.id })) {
    throw "story list did not include created story"
  }

  [pscustomobject]@{
    health = $health.status
    transcript = $transcription.text
    voice_reply = $voice.reply
    image_bytes = $png.Length
    story = $created.id
    story_status = $status.status
    story_wav_bytes = $storyWav.Content.Length
  } | ConvertTo-Json
}
finally {
  if ($server -and -not $server.HasExited) {
    Stop-Process -Id $server.Id -Force
  }
  if ($llamaPid) {
    try {
      $process = Get-Process -Id $llamaPid -ErrorAction Stop
      if ($process.ProcessName -like "cpp-studio-fixture*") {
        Stop-Process -Id $process.Id -Force
      }
    } catch {
      # The gateway may already have cleaned up the fixture server.
    }
  }
  Stop-FixtureListener -Port $LlamaPort -ExpectedPath $fixtureCommand
}
