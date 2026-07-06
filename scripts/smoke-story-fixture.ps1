param(
  [int]$GatewayPort = 8776,
  [string]$OutDir = ".\out\story-fixture"
)

$ErrorActionPreference = "Stop"
$env:Path = $env:Path + ";" + [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")

function Get-WavInfo {
  param([string]$Path)

  $bytes = [System.IO.File]::ReadAllBytes((Resolve-Path $Path))
  if ($bytes.Length -lt 12) {
    throw "WAV is too small: $Path"
  }
  $riff = [System.Text.Encoding]::ASCII.GetString($bytes, 0, 4)
  $wave = [System.Text.Encoding]::ASCII.GetString($bytes, 8, 4)
  if ($riff -ne "RIFF" -or $wave -ne "WAVE") {
    throw "WAV header mismatch: riff=$riff wave=$wave"
  }
  $channels = 0
  $sampleRate = 0
  $bitsPerSample = 0
  $dataBytes = 0
  $offset = 12
  while ($offset + 8 -le $bytes.Length) {
    $chunk = [System.Text.Encoding]::ASCII.GetString($bytes, $offset, 4)
    $size = [System.BitConverter]::ToUInt32($bytes, $offset + 4)
    $dataStart = $offset + 8
    if ($dataStart + $size -gt $bytes.Length) {
      throw "WAV chunk $chunk extends past end of file"
    }
    if ($chunk -eq "fmt ") {
      if ($size -lt 16) {
        throw "WAV fmt chunk is too small"
      }
      $channels = [System.BitConverter]::ToUInt16($bytes, $dataStart + 2)
      $sampleRate = [System.BitConverter]::ToUInt32($bytes, $dataStart + 4)
      $bitsPerSample = [System.BitConverter]::ToUInt16($bytes, $dataStart + 14)
    } elseif ($chunk -eq "data") {
      $dataBytes = $size
    }
    $offset = $dataStart + $size
    if ($size % 2 -eq 1) {
      $offset += 1
    }
  }
  if (-not $channels -or -not $sampleRate -or -not $bitsPerSample -or -not $dataBytes) {
    throw "WAV missing fmt or data chunk"
  }
  $durationSeconds = [math]::Round($dataBytes / ($sampleRate * $channels * ($bitsPerSample / 8)), 3)
  [pscustomobject]@{
    bytes = $bytes.Length
    riff = $riff
    wave = $wave
    channels = $channels
    sample_rate = $sampleRate
    bits_per_sample = $bitsPerSample
    duration_seconds = $durationSeconds
  }
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
New-Item -ItemType Directory -Force -Path ".\bin" | Out-Null

$gatewayExe = ".\bin\cpp-studio.exe"
$configPath = Join-Path $OutDir "config.story.json"
$storyWav = Join-Path $OutDir "story.wav"

go build -o $gatewayExe .\cmd\cpp-studio
$gatewayCommand = (Resolve-Path $gatewayExe).Path

$config = [ordered]@{
  gateway = [ordered]@{
    host = "127.0.0.1"
    port = $GatewayPort
  }
  engines = [ordered]@{
    ci = [ordered]@{
      command = "go"
      args = @("version")
      mode = "subprocess"
      requestTimeoutSeconds = 10
    }
  }
}
$config | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 -Path $configPath

$server = Start-Process -WindowStyle Hidden -PassThru -FilePath $gatewayCommand -ArgumentList @("--config", (Resolve-Path $configPath).Path)
try {
  $deadline = (Get-Date).AddSeconds(10)
  do {
    Start-Sleep -Milliseconds 250
    try {
      $health = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/health" -Method Get
    } catch {
      $health = $null
    }
    if ($server.HasExited) {
      throw "gateway exited before health check with code $($server.ExitCode)"
    }
  } until ($health -or (Get-Date) -gt $deadline)

  if (-not $health) {
    throw "gateway did not become reachable"
  }

  $body = [ordered]@{
    subject = "how stars are born"
    target_seconds = 90
    source_mode = "curated"
    voice_mode = "placeholder"
    sources = @(
      [ordered]@{
        id = "src-1"
        title = "NASA Science: Star Basics"
        url = "https://science.nasa.gov/universe/stars/"
        excerpt = "Stars form inside molecular clouds of gas and dust. Cold cloud conditions help gas clump into denser pockets. As clumps gain mass, gravity can make them collapse."
      },
      [ordered]@{
        id = "src-2"
        title = "NASA Webb: Fiery Hourglass"
        url = "https://science.nasa.gov/missions/webb/nasas-webb-catches-fiery-hourglass-as-new-star-forms/"
        excerpt = "A forming protostar gathers material from its surrounding molecular cloud. Falling material spirals inward and forms an accretion disk. The disk feeds material onto the protostar."
      },
      [ordered]@{
        id = "src-3"
        title = "NASA Hubble: Planet-Forming Disks"
        url = "https://science.nasa.gov/missions/hubble/hubbles-album-of-planet-forming-disks/"
        excerpt = "Some falling material forms a rotating disk around the protostar. Jets from magnetic poles are part of star formation. Jets help carry away angular momentum so material can continue collecting."
      }
    )
  } | ConvertTo-Json -Depth 8

  $created = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/stories" -Method Post -ContentType "application/json" -Body $body
  if (-not $created.id -or $created.status -ne "queued") {
    throw "unexpected create response: $($created | ConvertTo-Json -Depth 8)"
  }

  $status = $null
  $deadline = (Get-Date).AddSeconds(10)
  do {
    Start-Sleep -Milliseconds 250
    $status = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/stories/$($created.id)" -Method Get
    if ($status.status -eq "failed") {
      throw "story failed: $($status | ConvertTo-Json -Depth 8)"
    }
  } until ($status.status -eq "complete" -or (Get-Date) -gt $deadline)

  if ($status.status -ne "complete") {
    throw "story did not complete: $($status | ConvertTo-Json -Depth 8)"
  }
  if (-not $status.manifest -or $status.manifest.fact_cards.Count -lt 8) {
    throw "completed story missing fact cards: $($status | ConvertTo-Json -Depth 8)"
  }
  if ($status.manifest.duration_seconds -ne 90) {
    throw "unexpected story duration: $($status.manifest.duration_seconds)"
  }
  foreach ($line in $status.manifest.script) {
    if (-not $line.fact_ids -or $line.fact_ids.Count -eq 0) {
      throw "script line missing fact ids: $($line | ConvertTo-Json -Depth 8)"
    }
  }

  Invoke-WebRequest -Uri "http://127.0.0.1:$GatewayPort$($status.artifact_url)" -Method Get -OutFile $storyWav
  $wav = Get-WavInfo -Path $storyWav
  if ($wav.channels -ne 1 -or $wav.sample_rate -ne 16000 -or $wav.bits_per_sample -ne 16) {
    throw "unexpected WAV format: $($wav | ConvertTo-Json -Depth 8)"
  }
  if ([math]::Abs($wav.duration_seconds - 90) -gt 0.01) {
    throw "unexpected WAV duration: $($wav.duration_seconds)"
  }

  $list = Invoke-RestMethod -Uri "http://127.0.0.1:$GatewayPort/v1/stories" -Method Get
  if (-not $list.stories -or $list.stories[0].id -ne $created.id) {
    throw "story list did not include created story: $($list | ConvertTo-Json -Depth 8)"
  }

  [pscustomobject]@{
    health = $health.status
    story = $created.id
    status = $status.status
    wav = $wav
  } | ConvertTo-Json -Depth 8
}
finally {
  if ($server -and -not $server.HasExited) {
    Stop-Process -Id $server.Id -Force
  }
}
