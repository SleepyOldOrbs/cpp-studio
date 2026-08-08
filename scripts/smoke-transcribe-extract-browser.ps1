param(
  [int]$GatewayPort = 8887,
  [int]$WhisperPort = 8897,
  [string]$OutDir = ".\out\transcribe-extract-browser-smoke"
)

$ErrorActionPreference = "Stop"
$npx = Get-Command npx -ErrorAction Stop

if (Get-NetTCPConnection -LocalPort $GatewayPort -State Listen -ErrorAction SilentlyContinue) {
  throw "Transcribe/Extract browser smoke port $GatewayPort is already in use"
}
if (Get-NetTCPConnection -LocalPort $WhisperPort -State Listen -ErrorAction SilentlyContinue) {
  throw "Transcribe/Extract fixture Whisper port $WhisperPort is already in use"
}

function Write-SmokeWav {
  param([string]$Path)

  $rate = 16000
  $samples = $rate * 3
  $stream = [IO.MemoryStream]::new()
  $writer = [IO.BinaryWriter]::new($stream)
  try {
    $writer.Write([Text.Encoding]::ASCII.GetBytes("RIFF"))
    $writer.Write([uint32](36 + $samples * 2))
    $writer.Write([Text.Encoding]::ASCII.GetBytes("WAVEfmt "))
    $writer.Write([uint32]16)
    $writer.Write([uint16]1)
    $writer.Write([uint16]1)
    $writer.Write([uint32]$rate)
    $writer.Write([uint32]($rate * 2))
    $writer.Write([uint16]2)
    $writer.Write([uint16]16)
    $writer.Write([Text.Encoding]::ASCII.GetBytes("data"))
    $writer.Write([uint32]($samples * 2))
    for ($i = 0; $i -lt $samples; $i += 1) {
      $writer.Write([int16](1200 * [Math]::Sin(2 * [Math]::PI * 220 * $i / $rate)))
    }
    [IO.File]::WriteAllBytes($Path, $stream.ToArray())
  } finally {
    $writer.Dispose()
    $stream.Dispose()
  }
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
New-Item -ItemType Directory -Force -Path ".\output\playwright\transcribe-extract" | Out-Null
$runtimeDir = (Resolve-Path $OutDir).Path
$playwrightDir = (Resolve-Path ".\output\playwright\transcribe-extract").Path
$gatewayExe = Join-Path $runtimeDir "cpp-studio-transcribe-extract-smoke.exe"
$fixtureExe = Join-Path $runtimeDir "cpp-studio-fixture.exe"
$configPath = Join-Path $runtimeDir "config.json"
$inputWav = Join-Path $runtimeDir "input.wav"
$transcribeScreenshotPath = Join-Path $playwrightDir "transcribe.png"
$extractScreenshotPath = Join-Path $playwrightDir "extract.png"

go build -o $gatewayExe .\cmd\cpp-studio
if ($LASTEXITCODE -ne 0) {
  throw "failed to build the Transcribe/Extract browser-smoke Gateway"
}
go build -o $fixtureExe .\cmd\cpp-studio-fixture
if ($LASTEXITCODE -ne 0) {
  throw "failed to build the browser-smoke fixture Engine"
}
Write-SmokeWav -Path $inputWav

$config = [ordered]@{
  gateway = [ordered]@{ host = "127.0.0.1"; port = $GatewayPort }
  engines = [ordered]@{
    whisper = [ordered]@{
      command = $fixtureExe
      args = @("server", "--host", "127.0.0.1", "--port", "$WhisperPort")
      mode = "server"
      healthUrl = "http://127.0.0.1:$WhisperPort/health"
      startupTimeoutSeconds = 10
      shutdownTimeoutSeconds = 5
      requestTimeoutSeconds = 10
    }
  }
}
$config | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 -Path $configPath

$baseURL = "http://127.0.0.1:$GatewayPort"
$session = "transcribe-extract-smoke-$PID"
$server = Start-Process -WindowStyle Hidden -PassThru -FilePath $gatewayExe -ArgumentList @("--config", $configPath) -WorkingDirectory $runtimeDir
$whisperPid = $null

function Invoke-BrowserCLI {
  param([string[]]$Arguments)

  Push-Location $playwrightDir
  try {
    & $npx.Source --yes --package "@playwright/cli" playwright-cli "-s=$session" @Arguments
    if ($LASTEXITCODE -ne 0) {
      throw "Playwright CLI failed: $($Arguments[0])"
    }
  } finally {
    Pop-Location
  }
}

function Invoke-BrowserCode {
  param([string]$Code)

  $jsonCode = $Code | ConvertTo-Json -Compress
  Invoke-BrowserCLI -Arguments @("run-code", "async page => await (eval($jsonCode))(page)")
}

try {
  $health = $null
  for ($i = 0; $i -lt 40 -and $null -eq $health; $i += 1) {
    if ($server.HasExited) {
      throw "gateway exited before the browser smoke started"
    }
    try {
      $health = Invoke-RestMethod "$baseURL/health"
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if ($null -eq $health) {
    throw "gateway did not become ready"
  }
  $whisperPid = $health.engines.whisper.pid

  Invoke-BrowserCLI -Arguments @("open", "$baseURL/demo/#transcription")
  Invoke-BrowserCLI -Arguments @("snapshot")

  $browserCode = @'
async page => {
  const origin = await page.evaluate(() => location.origin);
  const response = await page.request.get(origin + '/v1/library');
  if (!response.ok()) throw new Error('could not inspect isolated smoke Library');
  const library = await response.json();
  for (const item of library.items || []) {
    if (item.meta && item.meta.source === 'input.wav') {
      const deleted = await page.request.delete(origin + '/v1/library/' + item.id);
      if (deleted.status() !== 204) throw new Error('could not clean prior smoke item ' + item.id);
    }
  }
}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const inputWav = __INPUT_WAV__;

  await page.waitForLoadState('networkidle');
  await page.getByRole('heading', { name: 'Transcribe', exact: true }).waitFor();
  assert(!(await page.locator('#extractCloneButton').isVisible()), 'Transcribe exposed clone-reference action');
  assert(!(await page.locator('#extractLibraryButton').isVisible()), 'Transcribe exposed Library clip action');
  assert(!(await page.locator('#extractCastButton').isVisible()), 'Transcribe exposed cast cloning');

  await page.locator('#extractFileInput').setInputFiles(inputWav);
  await page.waitForFunction(() => document.querySelector('#extractFileStatus').textContent.includes('input.wav'));
  await page.locator('#extractTranscribeButton').click();
  await page.waitForFunction(() => document.querySelectorAll('.extract-segment').length > 0);

  assert((await page.locator('.extract-segment').count()) === 3, 'fixture did not produce three transcript segments');
  assert(await page.locator('#extractCastButton').isDisabled(), 'cast cloning was enabled before speaker tagging');
  await page.locator('.extract-segment').nth(0).locator('.extract-segment-tags button').filter({ hasText: /^A$/ }).click();
  await page.locator('.extract-segment').nth(1).locator('.extract-segment-tags button').filter({ hasText: /^A$/ }).click();
  await page.locator('.extract-segment').nth(2).locator('.extract-segment-tags button').filter({ hasText: /^B$/ }).click();

  const editedText = 'Edited second fixture line';
  await page.locator('.extract-segment-text').nth(1).fill(editedText);
  await page.locator('#transcribeRenameFrom').selectOption('A');
  await page.locator('#transcribeRenameTo').fill('Narrator');
  await page.locator('#transcribeRenameButton').click();
  const renamedSpeakers = await page.locator('.extract-segment-speaker').allTextContents();
  assert(JSON.stringify(renamedSpeakers) === JSON.stringify(['Narrator', 'Narrator', 'B']),
    'whole-speaker rename changed the wrong segments: ' + JSON.stringify(renamedSpeakers));

  await page.locator('#transcribeSearchInput').fill('edited');
  assert((await page.locator('#transcribeSearchStatus').textContent()) === '1 of 1', 'edited-text search count was wrong');
  assert((await page.locator('.search-active .extract-segment-text').textContent()) === editedText,
    'edited-text search selected the wrong line');
  await page.locator('#transcribeSearchInput').fill('Narrator');
  assert((await page.locator('#transcribeSearchStatus').textContent()) === '1 of 2', 'speaker search count was wrong');
  await page.locator('#transcribeSearchNext').click();
  assert((await page.locator('#transcribeSearchStatus').textContent()) === '2 of 2', 'speaker search navigation did not advance');
  assert(Number(await page.locator('#extractCanvas').getAttribute('aria-valuenow')) === 0.9,
    'speaker search did not seek to the second timestamp');
  await page.locator('#transcribeSearchClear').click();
}
'@
  $browserCode = $browserCode.Replace("__INPUT_WAV__", ($inputWav | ConvertTo-Json -Compress))
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const editedText = 'Edited second fixture line';

  const pureTools = await page.evaluate(() => {
    const tools = window.__cppStudioTranscriptTools;
    const sample = [{ start: 59.9996, end: 59.5, speaker: 'Speaker_[1]', text: '*hello*' }];
    const renameSample = [{ speaker: 'A' }, { speaker: 'A' }, { speaker: 'B' }];
    return {
      timestamp: tools.formatTimestamp(59.9996, ','),
      markdown: tools.buildMarkdown(sample, { sourceName: 'source_[x]' }),
      srt: tools.buildSRT(sample),
      vtt: tools.buildVTT(sample),
      txt: tools.buildTXT(sample),
      json: tools.buildJSON(sample, { sourceName: 'source.wav', duration: 60, sampleRate: 16000 }),
      matches: tools.findMatches([{ text: 'One', speaker: 'A' }, { text: 'Two', speaker: 'Narrator' }], 'narrator'),
      renamed: tools.renameSpeaker(renameSample, 'A', 'Host'),
      renameSample
    };
  });
  assert(pureTools.timestamp === '00:01:00,000', 'timestamp rounding crossed a boundary incorrectly');
  assert(pureTools.markdown.includes('Speaker\\_\\[1\\]') && pureTools.markdown.includes('\\*hello\\*'),
    'Markdown export did not escape user text');
  assert(pureTools.srt.includes('00:01:00,000 --> 00:01:00,000'), 'SRT did not prevent reversed timestamps');
  assert(pureTools.vtt.startsWith('WEBVTT\n\n'), 'WebVTT header was missing');
  assert(pureTools.txt.includes('Speaker_[1]: *hello*'), 'TXT content was incomplete');
  assert(JSON.parse(pureTools.json).sample_rate === 16000, 'JSON metadata was incomplete');
  assert(JSON.stringify(pureTools.matches) === '[1]', 'speaker-name search helper returned the wrong match');
  assert(pureTools.renamed === 2 && JSON.stringify(pureTools.renameSample.map(item => item.speaker)) === '["Host","Host","B"]',
    'speaker rename helper changed non-matching segments');

  const captureDownload = async (name) => {
    const pending = page.waitForEvent('download');
    await page.getByRole('button', { name, exact: true }).click();
    const download = await pending;
    const stream = await download.createReadStream();
    let text = '';
    for await (const chunk of stream) text += chunk.toString('utf8');
    return { filename: download.suggestedFilename(), text };
  };
  const downloads = {
    txt: await captureDownload('TXT'),
    md: await captureDownload('Markdown'),
    srt: await captureDownload('SRT'),
    vtt: await captureDownload('WebVTT'),
    json: await captureDownload('JSON')
  };
  Object.entries(downloads).forEach(([format, result]) => {
    assert(result.filename === 'input.' + format, format + ' used the wrong source-derived filename');
    assert(result.text.includes(editedText) && result.text.includes('Narrator'), format + ' lost edited transcript data');
  });
  assert(downloads.srt.text.includes('00:00:00,900 --> 00:00:01,700'), 'SRT timestamps were wrong');
  assert(downloads.vtt.text.startsWith('WEBVTT\n\n'), 'downloaded WebVTT header was wrong');
  const downloadedJSON = JSON.parse(downloads.json.text);
  assert(downloadedJSON.source_name === 'input.wav' && downloadedJSON.sample_rate === 48000 && downloadedJSON.segments.length === 3,
    'downloaded JSON metadata was wrong');

  const waveform = page.locator('#extractCanvas');
  await waveform.focus();
  await waveform.press('Home');
  await waveform.press('ArrowRight');
  assert(Number(await waveform.getAttribute('aria-valuenow')) === 1, 'keyboard waveform seek did not move one second');
  assert(await page.locator('#transcribeRecordButton').isEnabled(), 'microphone recording action was unavailable');

  const activeEdit = 'Final active edit';
  const textLine = page.locator('.extract-segment-text').first();
  await textLine.fill(activeEdit);
  await textLine.focus();
}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const activeEdit = 'Final active edit';
  const sourceStatus = await page.locator('#extractFileStatus').textContent();

  await page.waitForLoadState('networkidle');
  const switchRequests = [];
  const trackRequest = request => switchRequests.push(request.url());
  page.on('request', trackRequest);

  await page.locator('#openExtractButton').click();
  await page.waitForFunction(() => location.hash === '#extract');
  await page.getByRole('heading', { name: 'Extract', exact: true }).waitFor();
  assert((await page.locator('.extract-segment-text').first().textContent()) === activeEdit,
    'active transcript edit was lost on Transcribe to Extract');
  assert((await page.locator('#extractFileStatus').textContent()) === sourceStatus,
    'loaded source changed on Transcribe to Extract');
  assert(await page.locator('#extractCloneButton').isVisible(), 'Extract did not expose clone-reference action');
  assert(await page.locator('#extractLibraryButton').isVisible(), 'Extract did not expose Library clip action');

  await page.locator('.extract-segment-time').first().click();
  const tick = page.locator('.extract-segment-tick').first();
  await tick.check();
  await page.locator('#extractZoomInButton').click();
  const regionStatus = await page.locator('#extractRegionStatus').textContent();
  const viewStart = await page.locator('#extractViewStart').textContent();
  const viewEnd = await page.locator('#extractViewEnd').textContent();

  await page.locator('#openTranscribeButton').click();
  await page.waitForFunction(() => location.hash === '#transcription');
  await page.waitForFunction(() => document.querySelector('#extractCloneButton').offsetParent === null);
  assert(!(await page.locator('#extractCloneButton').isVisible()), 'Transcribe leaked extraction actions after returning');
  assert((await page.locator('.extract-segment-text').first().textContent()) === activeEdit,
    'edited transcript changed on Extract to Transcribe');
  assert((await page.locator('.extract-segment-speaker').first().textContent()) === 'Narrator',
    'speaker correction changed on Extract to Transcribe');

  await page.locator('#openExtractButton').click();
  await page.waitForFunction(() => location.hash === '#extract');
  assert(await page.locator('.extract-segment-tick').first().isChecked(), 'checked segment was not preserved');
  assert((await page.locator('#extractRegionStatus').textContent()) === regionStatus, 'marked region was not preserved');
  assert((await page.locator('#extractViewStart').textContent()) === viewStart, 'waveform view start was not preserved');
  assert((await page.locator('#extractViewEnd').textContent()) === viewEnd, 'waveform zoom was not preserved');

  page.off('request', trackRequest);
  assert(switchRequests.length === 0, 'mode switching made network requests: ' + switchRequests.join(', '));
}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  assert(await page.locator('#extractCastButton').isEnabled(), 'cast cloning stayed disabled after speaker tagging');
  await page.locator('.extract-segment-tick').nth(2).check();
  assert((await page.locator('#extractSelectionDuration').textContent()) === '2.3s', 'stitched selection duration was wrong');
  assert((await page.locator('#extractSelectionSpeakers').textContent()) === 'Narrator, B', 'selection speaker provenance was wrong');
  assert((await page.locator('#extractSelectionSpanCount').textContent()) === '2', 'stitched span count was wrong');

  await page.locator('#extractPlayButton').click();
  await page.waitForFunction(() => !document.querySelector('#extractStopButton').disabled);
  await page.locator('#extractStopButton').click();
  assert(await page.locator('#extractStopButton').isDisabled(), 'selection playback did not stop');

  const saveResponsePending = page.waitForResponse(response =>
    response.url().endsWith('/v1/library') && response.request().method() === 'POST');
  await page.locator('#extractLibraryButton').click();
  const saveResponse = await saveResponsePending;
  assert(saveResponse.status() === 201, 'selection save failed with ' + saveResponse.status());
  const saved = await saveResponse.json();
  assert(saved.durationMs >= 2290 && saved.durationMs <= 2310, 'saved stitched WAV duration was wrong: ' + saved.durationMs);
  assert(saved.meta.source === 'input.wav' && saved.meta.segments === '2' && saved.meta.spans === '2',
    'saved selection provenance was incomplete: ' + JSON.stringify(saved.meta));
  await page.waitForFunction(() => document.querySelector('#extractLibraryButton').textContent === 'Saved ✓');
  const origin = await page.evaluate(() => location.origin);
  const deleted = await page.request.delete(origin + '/v1/library/' + saved.id);
  assert(deleted.status() === 204, 'browser smoke could not clean up its saved Library item');

  await page.locator('.extract-segment-tick').nth(2).uncheck();
  assert((await page.locator('#extractSelectionSpeakers').textContent()) === 'Narrator', 'single-speaker selection provenance was wrong');
  await page.locator('#extractCloneButton').click();
  await page.waitForFunction(() => location.hash === '#voices');
  const cloneStatus = await page.locator('#cloneWavStatus').textContent();
  assert(cloneStatus.includes('from input.wav') && cloneStatus.includes('speaker Narrator') && cloneStatus.includes('0:00.0–0:01.1'),
    'clone-reference handoff lost provenance: ' + cloneStatus);
  await page.locator('[data-page-link="extract"]').click();
  await page.waitForFunction(() => location.hash === '#extract');
}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const transcribeScreenshotPath = __TRANSCRIBE_SCREENSHOT_PATH__;
  const extractScreenshotPath = __EXTRACT_SCREENSHOT_PATH__;

  await page.locator('#openTranscribeButton').click();
  await page.waitForFunction(() => location.hash === '#transcription');
  await page.evaluate(() => {
    window.__transcribeTrackStopped = false;
    class FakeRecordingContext {
      constructor() {
        this.sampleRate = 16000;
        this.state = 'running';
        this.destination = {};
      }
      createMediaStreamSource() {
        return { connect() {}, disconnect() {} };
      }
      createScriptProcessor() {
        const processor = { connect() {}, disconnect() {}, onaudioprocess: null };
        window.__transcribeFakeProcessor = processor;
        return processor;
      }
      close() {
        this.state = 'closed';
        return Promise.resolve();
      }
    }
    Object.defineProperty(window, 'AudioContext', { configurable: true, value: FakeRecordingContext });
    Object.defineProperty(navigator.mediaDevices, 'getUserMedia', {
      configurable: true,
      value: async () => ({ getTracks: () => [{ stop: () => { window.__transcribeTrackStopped = true; } }] })
    });
  });
  await page.locator('#transcribeRecordButton').click();
  await page.waitForFunction(() => !document.querySelector('#transcribeStopRecordButton').disabled);
  await page.evaluate(() => {
    const samples = new Float32Array(8192);
    samples.fill(0.15);
    window.__transcribeFakeProcessor.onaudioprocess({
      inputBuffer: { getChannelData: () => samples },
      outputBuffer: { getChannelData: () => new Float32Array(samples.length) }
    });
  });
  await page.locator('#transcribeStopRecordButton').click();
  await page.waitForFunction(() => document.querySelector('#extractFileStatus').textContent.includes('microphone-recording.wav'));
  assert(await page.evaluate(() => window.__transcribeTrackStopped), 'microphone track was not stopped after recording');
  await page.waitForFunction(() => document.querySelectorAll('.extract-segment').length === 0);
  assert((await page.locator('.extract-segment').count()) === 0, 'stopping a recording transcribed implicitly');
  await page.locator('#extractTranscribeButton').click();
  await page.waitForFunction(() => document.querySelectorAll('.extract-segment').length === 3);
  await page.screenshot({ path: transcribeScreenshotPath, fullPage: true });
  await page.locator('#openExtractButton').click();
  await page.waitForFunction(() => location.hash === '#extract');
  await page.locator('.extract-segment-time').first().click();
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({ path: extractScreenshotPath, fullPage: true });
}
'@
  $browserCode = $browserCode.Replace("__TRANSCRIBE_SCREENSHOT_PATH__", ($transcribeScreenshotPath | ConvertTo-Json -Compress))
  $browserCode = $browserCode.Replace("__EXTRACT_SCREENSHOT_PATH__", ($extractScreenshotPath | ConvertTo-Json -Compress))
  Invoke-BrowserCode -Code $browserCode
}
finally {
  try {
    Invoke-BrowserCLI -Arguments @("close")
  } catch {
    # Best-effort cleanup; the gateway still needs to be stopped.
  }
  if ($server -and -not $server.HasExited) {
    Stop-Process -Id $server.Id -Force
  }
  if ($whisperPid) {
    $fixtureProcess = Get-Process -Id $whisperPid -ErrorAction SilentlyContinue
    if ($fixtureProcess -and $fixtureProcess.Path -eq $fixtureExe) {
      Stop-Process -Id $whisperPid -Force
    }
  }
}
