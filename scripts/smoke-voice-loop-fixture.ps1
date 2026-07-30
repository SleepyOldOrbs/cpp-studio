param(
  [int]$GatewayPort = 8765,
  [int]$LlamaPort = 8799,
  [string]$OutDir = ".\out\fixture-loop"
)

$ErrorActionPreference = "Stop"
$env:Path = $env:Path + ";" + [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")

function Get-WavInfo {
  param([string]$Path)

  $bytes = [System.IO.File]::ReadAllBytes((Resolve-Path $Path).Path)
  if ($bytes.Length -lt 44) {
    throw "WAV file is too small: $($bytes.Length) bytes"
  }
  $riff = [System.Text.Encoding]::ASCII.GetString($bytes, 0, 4)
  $wave = [System.Text.Encoding]::ASCII.GetString($bytes, 8, 4)
  if ($riff -ne "RIFF" -or $wave -ne "WAVE") {
    throw "WAV file does not have RIFF/WAVE header"
  }
  $channels = [BitConverter]::ToUInt16($bytes, 22)
  $sampleRate = [BitConverter]::ToUInt32($bytes, 24)
  $sampleWidthBytes = [BitConverter]::ToUInt16($bytes, 34) / 8
  $frames = [BitConverter]::ToUInt32($bytes, 40) / ($channels * $sampleWidthBytes)
  [pscustomobject]@{
    bytes = $bytes.Length
    channels = $channels
    sample_width_bytes = $sampleWidthBytes
    sample_rate = $sampleRate
    frames = [int]$frames
    duration_seconds = [math]::Round($frames / $sampleRate, 3)
  }
}

function Get-PngInfo {
  param([byte[]]$Bytes)

  $signature = [byte[]](0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)
  if ($Bytes.Length -lt $signature.Length) {
    throw "PNG payload is too small: $($Bytes.Length) bytes"
  }
  for ($i = 0; $i -lt $signature.Length; $i++) {
    if ($Bytes[$i] -ne $signature[$i]) {
      throw "PNG payload does not have the expected signature"
    }
  }
  [pscustomobject]@{
    bytes = $Bytes.Length
    signature = "PNG"
  }
}

function Stop-FixtureListener {
  param(
    [int]$Port,
    [string]$ExpectedPath
  )

  $connections = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue |
    Where-Object { $_.State -eq "Listen" -or $_.State -eq 2 }
  foreach ($connection in $connections) {
    try {
      $process = Get-Process -Id $connection.OwningProcess -ErrorAction Stop
      $pathMatches = $false
      if ($ExpectedPath -and $process.Path) {
        $pathMatches = ([System.IO.Path]::GetFullPath($process.Path) -eq [System.IO.Path]::GetFullPath($ExpectedPath))
      }
      $nameMatches = $process.ProcessName -like "cpp-studio-fixture*"
      if ($pathMatches -or $nameMatches) {
        Stop-Process -Id $process.Id -Force
      }
    } catch {
      # Best effort cleanup for a fixture-only smoke port.
    }
  }
}

function Assert-PortFree {
  param(
    [int]$Port,
    [string]$Label
  )

  $connections = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue |
    Where-Object { $_.State -eq "Listen" -or $_.State -eq 2 }
  if ($connections) {
    $owners = ($connections | Select-Object -ExpandProperty OwningProcess -Unique) -join ", "
    throw "$Label port $Port is already in use by process id(s): $owners"
  }
}

function Stop-ProcessIfFixture {
  param(
    [int]$ProcessId,
    [string]$ExpectedPath
  )

  try {
    $process = Get-Process -Id $ProcessId -ErrorAction Stop
    $pathMatches = $false
    if ($ExpectedPath -and $process.Path) {
      $pathMatches = ([System.IO.Path]::GetFullPath($process.Path) -eq [System.IO.Path]::GetFullPath($ExpectedPath))
    }
    $nameMatches = $process.ProcessName -like "cpp-studio-fixture*"
    if ($pathMatches -or $nameMatches) {
      Stop-Process -Id $process.Id -Force
    }
  } catch {
    # The gateway may already have cleaned up the fixture server.
  }
}

New-Item -ItemType Directory -Force -Path .\bin | Out-Null
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$gatewayExe = ".\bin\cpp-studio.exe"
$fixtureExe = ".\bin\cpp-studio-fixture.exe"
$configPath = Join-Path $OutDir "config.fixture.json"
$inputWav = Join-Path $OutDir "input.wav"
$outputWav = Join-Path $OutDir "reply.wav"

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
      args = @("speech", "--require-contains", "fixture transcript")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
    sd = [ordered]@{
      command = $fixtureCommand
      args = @("image", "--require-size", "64x64")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
  }
}
$config | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 -Path $configPath

$server = Start-Process -WindowStyle Hidden -PassThru -FilePath $gatewayExe -ArgumentList @("--config", $configPath)
$llamaPid = $null
try {
  $health = $null
  for ($i = 0; $i -lt 80 -and $null -eq $health; $i++) {
    if ($server.HasExited) {
      throw "gateway exited before becoming ready with exit code $($server.ExitCode)"
    }
    try {
      $health = Invoke-RestMethod "http://127.0.0.1:$GatewayPort/health"
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if ($null -eq $health -or $health.status -ne "ready") {
    throw "gateway did not become ready"
  }
  if ($server.HasExited) {
    throw "gateway exited after health check with exit code $($server.ExitCode)"
  }
  $llamaPid = $health.engines.llama.pid

  $form = @{
    file = Get-Item $inputWav
  }
  $transcription = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/audio/transcriptions" -Method Post -Form $form
  if ($transcription.text -ne "fixture transcript") {
    throw "unexpected transcript: $($transcription.text)"
  }

  $chatBody = @{
    model = "fixture"
    messages = @(
      @{ role = "user"; content = $transcription.text }
    )
  } | ConvertTo-Json -Depth 8
  $chat = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/chat/completions" -Method Post -ContentType "application/json" -Body $chatBody
  $reply = $chat.choices[0].message.content
  if (-not $reply -or -not $reply.Contains("fixture transcript")) {
    throw "unexpected chat reply: $reply"
  }

  $speechBody = @{ input = $reply; voice = "default"; format = "wav" } | ConvertTo-Json
  Invoke-WebRequest -Uri "http://127.0.0.1:$GatewayPort/v1/audio/speech" -Method Post -ContentType "application/json" -Body $speechBody -OutFile $outputWav

  $wavInfo = Get-WavInfo -Path $outputWav

  $voiceForm = @{
    file = Get-Item $inputWav
  }
  $voice = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/voice" -Method Post -Form $voiceForm
  if ($voice.transcript -ne "fixture transcript") {
    throw "unexpected voice transcript: $($voice.transcript)"
  }
  if (-not $voice.reply -or -not $voice.reply.Contains("fixture transcript")) {
    throw "unexpected voice reply: $($voice.reply)"
  }
  if (-not $voice.audio_b64) {
    throw "voice response did not include audio_b64"
  }
  $voiceWavPath = Join-Path $OutDir "voice-reply.wav"
  [System.IO.File]::WriteAllBytes((Join-Path (Resolve-Path $OutDir).Path "voice-reply.wav"), [Convert]::FromBase64String($voice.audio_b64))
  $voiceWavInfo = Get-WavInfo -Path $voiceWavPath

  # Hands-free pattern: a second WAV turn carrying the first exchange as
  # history, exactly as the browser submits consecutive utterances.
  $historyJson = ConvertTo-Json -Compress -Depth 4 @(
    @{ role = "user"; text = $voice.transcript },
    @{ role = "assistant"; text = $voice.reply }
  )
  $voice2 = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/voice" -Method Post -Form @{
    file = Get-Item $inputWav
    history = $historyJson
  }
  if ($voice2.transcript -ne "fixture transcript") {
    throw "unexpected second-turn transcript: $($voice2.transcript)"
  }
  if (-not $voice2.reply -or -not $voice2.audio_b64) {
    throw "second voice turn missing reply or audio"
  }

  $imageBody = @{ prompt = "fixture image"; size = "64x64"; response_format = "b64_json" } | ConvertTo-Json
  $image = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/images/generations" -Method Post -ContentType "application/json" -Body $imageBody
  if (-not $image.data -or -not $image.data[0].b64_json) {
    throw "image response did not include data[0].b64_json"
  }
  $imageBytes = [Convert]::FromBase64String($image.data[0].b64_json)
  $imageInfo = Get-PngInfo -Bytes $imageBytes

  [pscustomobject]@{
    health = $health.status
    transcript = $transcription.text
    reply = $reply
    output = (Resolve-Path $outputWav).Path
    wav = $wavInfo
    voice = [pscustomobject]@{
      transcript = $voice.transcript
      reply = $voice.reply
      wav = $voiceWavInfo
    }
    image = $imageInfo
  } | ConvertTo-Json -Depth 4
} finally {
  if ($server -and -not $server.HasExited) {
    Stop-Process -Id $server.Id -Force
  }
  if ($llamaPid) {
    Stop-ProcessIfFixture -ProcessId $llamaPid -ExpectedPath $fixtureCommand
  }
  Stop-FixtureListener -Port $LlamaPort -ExpectedPath $fixtureCommand
}
