param(
  [int]$GatewayPort = 8777,
  [int]$LlamaPort = 8798,
  [int]$VisionPort = 8797,
  [string]$OutDir = ".\out\demo-ui-smoke"
)

# Exercises every API flow the browser demo (internal/demo/static/app.js)
# makes, against the deterministic fixture engines: demo assets, health,
# transcription, the voice loop with conversation history, voice cloning,
# a DramaBox-routed factual audiobook with persisted provenance,
# (create / list / play / speak-with / delete), image generation, and a
# fixed-voice story with a stitched WAV artifact.

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
Stop-FixtureListener -Port $VisionPort -ExpectedPath $fixtureCommand
Assert-PortFree -Port $VisionPort -Label "fixture vision"
& $fixtureExe speech --text "fixture input" --out $inputWav

# A bring-your-own-model directory with one stand-in file. The bytes are
# deliberately not GGUF: the fit preflight must degrade to size-only
# judgement rather than refuse the listing.
$byomDir = Join-Path $OutDir "byom"
New-Item -ItemType Directory -Force -Path $byomDir | Out-Null
Set-Content -Encoding ascii -Path (Join-Path $byomDir "smoke-model.gguf") -Value "stand-in model bytes"

$config = [ordered]@{
  gateway = [ordered]@{
    host = "127.0.0.1"
    port = $GatewayPort
  }
  engines = [ordered]@{
    llama = [ordered]@{
      command = $fixtureCommand
      mode = "server"
      healthUrl = "http://127.0.0.1:$LlamaPort/health"
      startupTimeoutSeconds = 10
      shutdownTimeoutSeconds = 5
      requestTimeoutSeconds = 30
      defaultVariant = "fixture"
      byomDir = $byomDir
      byomArgs = @("server", "--host", "127.0.0.1", "--port", "$LlamaPort", "-m", "{model}")
      variants = [ordered]@{
        fixture = [ordered]@{
          label = "fixture default"
          args = @("server", "--host", "127.0.0.1", "--port", "$LlamaPort")
        }
      }
    }
    vision = [ordered]@{
      command = $fixtureCommand
      args = @("server", "--host", "127.0.0.1", "--port", "$VisionPort")
      mode = "server"
      healthUrl = "http://127.0.0.1:$VisionPort/health"
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
    dramabox = [ordered]@{
      command = $fixtureCommand
      args = @("speech")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
    voicedesign = [ordered]@{
      command = $fixtureCommand
      args = @("design")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
    omnivoice = [ordered]@{
      command = $fixtureCommand
      args = @("design")
      mode = "subprocess"
      requestTimeoutSeconds = 30
    }
    voxcpm2 = [ordered]@{
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
$visionPid = $null
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
  $visionPid = $health.engines.vision.pid
  if (-not ($health.engines.PSObject.Properties.Name -contains "dramabox")) {
    throw "health did not report the configured DramaBox engine"
  }

  # Demo assets, exactly as the browser fetches them.
  $index = Invoke-WebRequest -Uri "$base/demo/" -UseBasicParsing
  if ($index.Content -notlike "*cpp-studio local studio*") {
    throw "demo index is missing its title marker"
  }
  if ($index.Content -notlike "*audiobookDramaBoxOption*" -or $index.Content -notlike "*audiobookDramaBoxWarning*") {
    throw "demo index is missing DramaBox audiobook controls"
  }
  $appJs = Invoke-WebRequest -Uri "$base/demo/app.js" -UseBasicParsing
  if ($appJs.Content -notlike "*refreshStoryLibrary*") {
    throw "demo app.js is missing its marker"
  }
  if ($appJs.Content -notlike "*updateAudiobookEngines*" -or $appJs.Content -notlike '*form.append("direction"*') {
    throw "demo app.js is missing DramaBox health or direction behavior"
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

  # Voice clone: create from the reference WAV (whisper transcribes it),
  # list it, fetch the reference for playback, speak through it in the
  # voice loop, then delete it so the smoke leaves no library entries.
  $clone = Invoke-RestMethod -Uri "$base/v1/voices" -Method Post -Form @{ name = "Smoke Voice"; file = Get-Item $inputWav }
  if (-not $clone.id -or $clone.transcript -ne "fixture transcript") {
    throw "unexpected voice clone response: $($clone | ConvertTo-Json -Depth 4)"
  }
  $voices = Invoke-RestMethod "$base/v1/voices"
  if (-not ($voices.voices | Where-Object { $_.id -eq $clone.id })) {
    throw "voice list did not include cloned voice"
  }
  $refWav = Invoke-WebRequest -Uri "$base$($clone.audio_url)" -UseBasicParsing
  Assert-WavBytes -Bytes $refWav.Content -Label "voice reference"
  $clonedVoice = Invoke-RestMethod -Uri "$base/v1/voice" -Method Post -Form @{ message = "speak as the clone"; voice = $clone.id }
  if (-not $clonedVoice.audio_b64) {
    throw "cloned voice loop returned no audio"
  }
  Assert-WavBytes -Bytes ([Convert]::FromBase64String($clonedVoice.audio_b64)) -Label "cloned voice reply"
  $speakBody = @{ input = "read this in the cloned voice"; voice = $clone.id; format = "wav" } | ConvertTo-Json
  $spoken = Invoke-WebRequest -Uri "$base/v1/audio/speech" -Method Post -ContentType "application/json" -Body $speakBody -UseBasicParsing
  Assert-WavBytes -Bytes $spoken.Content -Label "spoken text"
  Invoke-RestMethod -Uri "$base/v1/voices/$($clone.id)" -Method Delete | Out-Null
  $voices = Invoke-RestMethod "$base/v1/voices"
  if ($voices.voices | Where-Object { $_.id -eq $clone.id }) {
    throw "voice list still includes deleted voice"
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

  # Voice designer: describe a voice, audition it, save the audition as a
  # library voice, speak with it, then delete it.
  $design = Invoke-RestMethod -Uri "$base/v1/voices/design" -Method Post -ContentType "application/json" -Body (@{ description = "deep gravelly cowboy" } | ConvertTo-Json)
  if (-not $design.reference_b64 -or -not $design.preview_b64 -or -not $design.transcript) {
    throw "voice design response missing fields: $($design | ConvertTo-Json -Depth 4)"
  }
  foreach ($designModel in @("omnivoice", "voxcpm2")) {
    $altDesign = Invoke-RestMethod -Uri "$base/v1/voices/design" -Method Post -ContentType "application/json" -Body (@{ description = "female, british accent"; model = $designModel } | ConvertTo-Json)
    if ($altDesign.model -ne $designModel -or -not $altDesign.preview_b64) {
      throw "voice design via $designModel failed: $($altDesign | ConvertTo-Json -Depth 4)"
    }
    Assert-WavBytes -Bytes ([Convert]::FromBase64String($altDesign.preview_b64)) -Label "$designModel design preview"
  }
  Assert-WavBytes -Bytes ([Convert]::FromBase64String($design.preview_b64)) -Label "designed voice preview"
  $designedRefPath = Join-Path (Resolve-Path $OutDir).Path "designed-ref.wav"
  [IO.File]::WriteAllBytes($designedRefPath, [Convert]::FromBase64String($design.reference_b64))
  $designedVoice = Invoke-RestMethod -Uri "$base/v1/voices" -Method Post -Form @{ name = "Designed Cowboy"; transcript = $design.transcript; file = Get-Item $designedRefPath }
  if (-not $designedVoice.id) {
    throw "designed voice save failed: $($designedVoice | ConvertTo-Json -Depth 4)"
  }
  $designedReply = Invoke-RestMethod -Uri "$base/v1/voice" -Method Post -Form @{ message = "speak as the designed voice"; voice = $designedVoice.id }
  Assert-WavBytes -Bytes ([Convert]::FromBase64String($designedReply.audio_b64)) -Label "designed voice reply"
  Invoke-RestMethod -Uri "$base/v1/voices/$($designedVoice.id)" -Method Delete | Out-Null

  # Vision: describe the generated image; the fixture vision server answers
  # with a fixed marker for any request carrying an image part.
  $describeBody = @{ image_b64 = $image.data[0].b64_json; voice = "" } | ConvertTo-Json
  $describe = Invoke-RestMethod -Uri "$base/v1/images/descriptions" -Method Post -ContentType "application/json" -Body $describeBody
  if ($describe.description -ne "fixture image description") {
    throw "unexpected image description: $($describe.description)"
  }
  Assert-WavBytes -Bytes ([Convert]::FromBase64String($describe.audio_b64)) -Label "image description"

  # Expressive factual audiobook: the model-free fixture stands in for the
  # DramaBox executable, but the browser-facing selection, prompt path,
  # selected-engine routing, job lifecycle, manifest, and artifact are real.
  $bookTextPath = Join-Path (Resolve-Path $OutDir).Path "facts.txt"
  Set-Content -Encoding UTF8 -Path $bookTextPath -Value "Saturn is the sixth planet from the Sun. Its rings contain ice and rock."
  $bookDirection = "Warm, precise documentary delivery."
  $bookCreate = Invoke-RestMethod -Uri "$base/v1/audiobooks" -Method Post -Form @{
    file = Get-Item $bookTextPath
    title = "Smoke Facts"
    engine = "dramabox"
    direction = $bookDirection
  }
  if (-not $bookCreate.id -or $bookCreate.chunks -lt 1) {
    throw "unexpected audiobook create response: $($bookCreate | ConvertTo-Json -Depth 4)"
  }
  $bookJob = $null
  $bookDeadline = (Get-Date).AddSeconds(30)
  do {
    Start-Sleep -Milliseconds 100
    $bookJob = Invoke-RestMethod "$base/v1/jobs/$($bookCreate.id)"
    if ($bookJob.status -eq "failed" -or $bookJob.status -eq "cancelled") {
      throw "DramaBox audiobook failed: $($bookJob | ConvertTo-Json -Depth 6)"
    }
  } until ($bookJob.status -eq "complete" -or (Get-Date) -gt $bookDeadline)
  if ($bookJob.status -ne "complete" -or $bookJob.result.engine -ne "dramabox") {
    throw "DramaBox audiobook did not complete with provenance: $($bookJob | ConvertTo-Json -Depth 6)"
  }
  $books = Invoke-RestMethod "$base/v1/audiobooks"
  $bookManifest = $books.audiobooks | Where-Object { $_.id -eq $bookCreate.id }
  if (-not $bookManifest -or $bookManifest.engine -ne "dramabox" -or $bookManifest.direction -ne $bookDirection) {
    throw "DramaBox audiobook manifest lost provenance: $($books | ConvertTo-Json -Depth 6)"
  }
  $bookWav = Invoke-WebRequest -Uri "$base$($bookJob.result.artifactUrl)" -UseBasicParsing
  Assert-WavBytes -Bytes $bookWav.Content -Label "DramaBox audiobook artifact"

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

  # Draft first: the fixture llama returns its canned script, which we edit
  # and hand back for production (the draft -> edit -> produce flow).
  $draft = Invoke-RestMethod -Uri "$base/v1/stories/draft" -Method Post -ContentType "application/json" -Body $storyBody
  if ($draft.title -ne "Fixture Story" -or -not $draft.script -or $draft.script.Count -lt 4) {
    throw "unexpected story draft: $($draft | ConvertTo-Json -Depth 6)"
  }
  $draft.script[0].text = "An edited opening line from the smoke."
  $produce = $storyBody | ConvertFrom-Json
  $produce | Add-Member -NotePropertyName title -NotePropertyValue "Edited Fixture Story"
  $produce | Add-Member -NotePropertyName script -NotePropertyValue $draft.script
  $storyBody = $produce | ConvertTo-Json -Depth 8
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
  if ($status.manifest.title -ne "Edited Fixture Story") {
    throw "expected the edited draft title, got: $($status.manifest.title)"
  }
  if ($status.manifest.script[0].text -ne "An edited opening line from the smoke.") {
    throw "expected the edited line to survive production"
  }

  $storyWav = Invoke-WebRequest -Uri "$base$($status.artifact_url)" -UseBasicParsing
  Assert-WavBytes -Bytes $storyWav.Content -Label "story artifact"

  $list = Invoke-RestMethod "$base/v1/stories"
  if (-not $list.stories -or -not ($list.stories | Where-Object { $_.id -eq $created.id })) {
    throw "story list did not include created story"
  }

  # Bring-your-own-model: the listing synthesizes a variant per byomDir
  # file with its byte size, a switch restarts the fixture llama on it, a
  # traversal-shaped id is refused, and the default comes back cleanly.
  $variants = Invoke-RestMethod "$base/v1/engines/llama/variants"
  $byomEntry = $variants.variants | Where-Object { $_.id -eq "byom:smoke-model.gguf" }
  if (-not $byomEntry) {
    throw "variant listing did not include the byom model: $($variants | ConvertTo-Json -Depth 5)"
  }
  if (-not $byomEntry.bytes -or $byomEntry.bytes -le 0) {
    throw "byom entry carried no byte size"
  }
  $switched = Invoke-RestMethod -Method Post -Uri "$base/v1/engines/llama/variant" -ContentType "application/json" -Body '{"id":"byom:smoke-model.gguf"}'
  $nowActive = $switched.variants | Where-Object { $_.active }
  if ($nowActive.id -ne "byom:smoke-model.gguf") {
    throw "expected the byom model active after the switch, got: $($nowActive.id)"
  }
  $traversalRefused = $false
  try {
    Invoke-RestMethod -Method Post -Uri "$base/v1/engines/llama/variant" -ContentType "application/json" -Body '{"id":"byom:..\\evil.gguf"}' | Out-Null
  } catch {
    $traversalRefused = $true
  }
  if (-not $traversalRefused) {
    throw "a traversal-shaped byom id was accepted"
  }
  $restored = Invoke-RestMethod -Method Post -Uri "$base/v1/engines/llama/variant" -ContentType "application/json" -Body '{"id":"fixture"}'
  if (-not ($restored.variants | Where-Object { $_.id -eq "fixture" -and $_.active })) {
    throw "switching back to the configured variant failed"
  }
  # Each swap restarted llama; re-capture the pid the cleanup must target.
  $llamaPid = (Invoke-RestMethod "$base/health").engines.llama.pid
  $voiceAfterSwap = Invoke-RestMethod -Method Post -Uri "$base/v1/voice" -Form @{ message = "still chatting after the swaps?" }
  if (-not $voiceAfterSwap.reply) {
    throw "voice loop returned no reply after variant swaps"
  }

  [pscustomobject]@{
    health = $health.status
    transcript = $transcription.text
    voice_reply = $voice.reply
    cloned_voice = $clone.id
    designed_voice = $designedVoice.id
    image_bytes = $png.Length
    image_description = $describe.description
    audiobook = $bookCreate.id
    audiobook_engine = $bookJob.result.engine
    audiobook_wav_bytes = $bookWav.Content.Length
    story = $created.id
    story_status = $status.status
    story_wav_bytes = $storyWav.Content.Length
  } | ConvertTo-Json
}
finally {
  if ($server -and -not $server.HasExited) {
    Stop-Process -Id $server.Id -Force
  }
  foreach ($enginePid in @($llamaPid, $visionPid)) {
    if (-not $enginePid) {
      continue
    }
    try {
      $process = Get-Process -Id $enginePid -ErrorAction Stop
      if ($process.ProcessName -like "cpp-studio-fixture*") {
        Stop-Process -Id $process.Id -Force
      }
    } catch {
      # The gateway may already have cleaned up the fixture server.
    }
  }
  Stop-FixtureListener -Port $LlamaPort -ExpectedPath $fixtureCommand
  Stop-FixtureListener -Port $VisionPort -ExpectedPath $fixtureCommand
}
