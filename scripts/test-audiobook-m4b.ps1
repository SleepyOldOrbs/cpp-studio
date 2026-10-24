param(
  [string]$FFmpeg = '',
  [string]$FFprobe = '',
  [int]$GatewayPort = 8883,
  [string]$OutDir = (Join-Path $env:TEMP ('audiobook-m4b-' + [guid]::NewGuid().ToString('N')))
)

$ErrorActionPreference = 'Stop'
if (-not $FFmpeg) { $FFmpeg = (Get-Command ffmpeg -ErrorAction SilentlyContinue).Source }
if (-not $FFprobe) { $FFprobe = (Get-Command ffprobe -ErrorAction SilentlyContinue).Source }
if (-not $FFmpeg -or -not $FFprobe -or -not (Test-Path -LiteralPath $FFmpeg) -or -not (Test-Path -LiteralPath $FFprobe)) {
  [ordered]@{status='SKIP';reason='FFmpeg and FFprobe are required for real M4B validation'} | ConvertTo-Json
  return
}
if (Get-NetTCPConnection -LocalPort $GatewayPort -State Listen -ErrorAction SilentlyContinue) { throw "Port $GatewayPort is in use" }
New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
$runtimeDir = (Resolve-Path $OutDir).Path
$bookID = 'book_20261024_120000_001'
$bookDir = Join-Path $runtimeDir "out/audiobooks/$bookID"
New-Item -ItemType Directory -Path $bookDir -Force | Out-Null
$masterPath = Join-Path $bookDir 'book.wav'
$stream = [IO.File]::Create($masterPath)
$writer = [IO.BinaryWriter]::new($stream)
try {
  $samples = 128000
  $writer.Write([Text.Encoding]::ASCII.GetBytes('RIFF'))
  $writer.Write([uint32](36+$samples*2))
  $writer.Write([Text.Encoding]::ASCII.GetBytes('WAVEfmt '))
  $writer.Write([uint32]16); $writer.Write([uint16]1); $writer.Write([uint16]1)
  $writer.Write([uint32]16000); $writer.Write([uint32]32000); $writer.Write([uint16]2); $writer.Write([uint16]16)
  $writer.Write([Text.Encoding]::ASCII.GetBytes('data')); $writer.Write([uint32]($samples*2))
  for ($i=0; $i -lt $samples; $i++) {
    $hz = if ($i -lt 64000) { 220 } else { 440 }
    $writer.Write([int16](1500*[Math]::Sin(2*[Math]::PI*$hz*$i/16000)))
  }
} finally { $writer.Dispose(); $stream.Dispose() }
$secondTitle = 'Café # = ; \ chapter\'
$metadata = @{title='The Clock #1=two; \ book';author='Fixture Writer';narrator='Fixture Narrator';year='2026';genre='Fiction';description="Line one`nLine two; # = \"}
$created = [DateTime]::UtcNow.ToString('o')
# A completed, generated PCM fixture exercises delivery without spending an
# inference run. Its editorial intervals are deliberately distinct from text.
@{
  id=$bookID; title='Fixture';engine='audio';status='complete';createdAt=$created;chunks=2;durationSeconds=8
  artifactUrl="/v1/audiobooks/$bookID/artifact/book.wav";currentRenderId='render-0001';sampleRate=16000
  documentPlan=@{digest='fixture';extractionPolicyVersion=1;chapters=@(@{id='chapter_001';title='Opening';startByte=0;endByte=12},@{id='chapter_002';title=$secondTitle;startByte=12;endByte=24})}
  chapterBoundaries=@(@{chapterId='chapter_001';startSample=0;endSample=64000},@{chapterId='chapter_002';startSample=64000;endSample=128000})
  renderRevisions=@(@{id='render-0001';artifactFile='book.wav';artifactUrl="/v1/audiobooks/$bookID/artifact/book.wav";createdAt=$created;durationSeconds=8;sampleRate=16000;chapterBoundaries=@(@{chapterId='chapter_001';startSample=0;endSample=64000},@{chapterId='chapter_002';startSample=64000;endSample=128000})})
} | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $bookDir 'manifest.json')
$coverPath = Join-Path $runtimeDir 'cover.png'
& $FFmpeg -nostdin -v error -y -f lavfi -i 'color=c=orange:s=120x180' -frames:v 1 $coverPath
if ($LASTEXITCODE) { throw 'Cover fixture generation failed' }
$gatewayExe = Join-Path $runtimeDir 'cpp-studio.exe'
go build -o $gatewayExe ./cmd/cpp-studio
if ($LASTEXITCODE) { throw 'Gateway build failed' }
$configPath = Join-Path $runtimeDir 'config.json'
@{gateway=@{host='127.0.0.1';port=$GatewayPort};engines=@{ffmpeg=@{command=$FFmpeg;mode='subprocess';requestTimeoutSeconds=60}}} | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 $configPath
$base = "http://127.0.0.1:$GatewayPort"
$server = Start-Process -WindowStyle Hidden -PassThru -FilePath $gatewayExe -ArgumentList @('--config',$configPath) -WorkingDirectory $runtimeDir -RedirectStandardError (Join-Path $runtimeDir 'gateway.log')
try {
  $ready=$false
  for ($i=0; $i -lt 60; $i++) {
    try { if ((Invoke-WebRequest "$base/health").StatusCode -eq 200) {$ready=$true;break} } catch {}
    Start-Sleep -Milliseconds 200
  }
  if (-not $ready) { throw 'Gateway did not become ready' }
  Invoke-RestMethod "$base/v1/audiobooks/$bookID/metadata" -Method Put -ContentType 'application/json' -Body ($metadata | ConvertTo-Json) | Out-Null
  Invoke-RestMethod "$base/v1/audiobooks/$bookID/cover" -Method Put -Form @{file=Get-Item $coverPath} | Out-Null
  $plan = Invoke-RestMethod "$base/v1/audiobooks/$bookID/exports/plan?render=render-0001"
  if (-not $plan.available -or $plan.chapters.Count -ne 2) { throw 'Real M4B export plan unavailable' }
  $before = (Get-FileHash -Algorithm SHA256 -LiteralPath $masterPath).Hash
  $export = Invoke-RestMethod "$base/v1/audiobooks/$bookID/exports/m4b" -Method Post -ContentType 'application/json' -Body (@{renderId=$plan.renderId;masterSha256=$plan.masterSha256} | ConvertTo-Json)
  $outputPath = Join-Path $runtimeDir 'chaptered-fixture.m4b'
  Invoke-WebRequest ($base+$export.url) -OutFile $outputPath | Out-Null
  $probeText = & $FFprobe -v error -show_format -show_streams -show_chapters -of json $outputPath
  if ($LASTEXITCODE) { throw 'FFprobe failed' }
  $probe = ($probeText -join "`n") | ConvertFrom-Json
  $probeText | Set-Content (Join-Path $runtimeDir 'ffprobe.json')
  $audio = @($probe.streams | Where-Object codec_type -eq audio)
  if ($audio.Count -ne 1 -or $audio[0].codec_name -ne 'aac' -or $audio[0].sample_rate -ne '16000' -or $audio[0].channels -ne 1 -or $probe.format.format_name -notlike '*mp4*') { throw 'Wrong M4B audio codec/container/format' }
  if ($probe.chapters.Count -ne 2 -or $probe.chapters[0].tags.title -ne 'Opening' -or $probe.chapters[1].tags.title -ne $secondTitle) { throw 'Chapter names did not round-trip' }
  $tolerance = 1024.0 / 16000
  if ([Math]::Abs([double]$audio[0].duration-8) -gt $tolerance) { throw 'Encoded audio does not cover the complete master' }
  for ($i=0; $i -lt 2; $i++) {
    if ([Math]::Abs([double]$probe.chapters[$i].start_time - $i*4) -gt $tolerance -or [Math]::Abs([double]$probe.chapters[$i].end_time - ($i+1)*4) -gt $tolerance) { throw 'Chapter markers differ by more than one AAC frame' }
  }
  if ($probe.format.tags.title -ne $metadata.title -or $probe.format.tags.artist -ne $metadata.author -or $probe.format.tags.composer -ne $metadata.narrator -or $probe.format.tags.description -ne $metadata.description) { throw 'Book metadata did not round-trip' }
  $cover = @($probe.streams | Where-Object { $_.codec_type -eq 'video' -and $_.disposition.attached_pic -eq 1 })
  if ($cover.Count -ne 1 -or $cover[0].width -ne 120 -or $cover[0].height -ne 180) { throw 'Cover is missing or changed' }
  & $FFmpeg -nostdin -v error -xerror -i $outputPath -map '0:a:0' -f null -
  if ($LASTEXITCODE) { throw 'Full AAC decode failed' }
  if ((Get-FileHash -Algorithm SHA256 -LiteralPath $masterPath).Hash -ne $before) { throw 'Export changed the WAV master' }
  $plan | ConvertTo-Json -Depth 8 | Set-Content (Join-Path $runtimeDir 'export-plan.json')
  [ordered]@{status='ok';output=$outputPath;probe=(Join-Path $runtimeDir 'ffprobe.json');chapters=2;masterUnchanged=$true} | ConvertTo-Json
} finally {
  if (-not $server.HasExited) { Stop-Process -Id $server.Id }
}
