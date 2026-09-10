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
$homepageScreenshotPath = Join-Path $playwrightDir "talk-voice-home.png"
$transcribeScreenshotPath = Join-Path $playwrightDir "transcribe.png"
$extractScreenshotPath = Join-Path $playwrightDir "extract.png"
$trainingScreenshotPath = Join-Path $playwrightDir "training.png"

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
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const screenshotPath = __HOMEPAGE_SCREENSHOT__;

  await page.waitForLoadState('networkidle');
  assert(await page.locator('[data-parent-link="talk-voice"]').evaluate(element => element.classList.contains('active')),
    'direct Transcribe link did not activate Talk & voice');
  assert(await page.locator('[data-parent-nav="talk-voice"]').isVisible(),
    'direct Transcribe link did not show its sub-navigation');
  assert(await page.locator('[data-page-link="transcription"]').first().evaluate(element => element.classList.contains('active')),
    'direct Transcribe link was not marked active');

  await page.locator('[data-parent-link="talk-voice"]').click();
  await page.waitForFunction(() => location.hash === '#talk-voice');
  const talkHome = page.locator('[data-page="talk-voice"]');
  await talkHome.waitFor({ state: 'visible' });
  assert(await talkHome.isVisible(), 'Talk & voice homepage did not open');
  assert((await talkHome.locator('.tool-card').count()) === 6, 'Talk & voice homepage tool count was wrong');
  await page.screenshot({ path: screenshotPath, fullPage: true });

  const homes = [
    ['music', 2],
    ['imagery', 1],
    ['stories-audiobooks', 4]
  ];
  for (const [name, count] of homes) {
    await page.locator('[data-parent-link="' + name + '"]').click();
    await page.waitForFunction(expected => location.hash === '#' + expected, name);
    const home = page.locator('[data-page="' + name + '"]');
    await home.waitFor({ state: 'visible' });
    assert(await home.isVisible(), name + ' homepage did not open');
    assert((await home.locator('.tool-card').count()) === count, name + ' homepage tool count was wrong');
    assert(await page.locator('[data-parent-nav="' + name + '"]').isVisible(), name + ' sub-navigation was hidden');
  }
  assert((await page.locator('[data-page="stories-audiobooks"] a[href="/demo/story-builder.html"]').count()) === 1,
    'Stories homepage did not link to Story Builder');

  await page.locator('[data-parent-link="talk-voice"]').click();
  await page.locator('[data-parent-nav="talk-voice"] [data-page-link="voice-cloning"]').click();
  await page.waitForFunction(() => location.hash === '#voice-cloning');
  await page.getByRole('heading', { name: 'Voice clone', exact: true }).waitFor();
  const voiceLayout = await page.locator('[data-page="voice-cloning"] .workspace').evaluate(workspace => {
    const bounds = workspace.getBoundingClientRect();
    const left = workspace.children[0].getBoundingClientRect();
    const right = workspace.children[1].getBoundingClientRect();
    return {
      widthDifference: Math.abs(left.width - right.width),
      insetDifference: Math.abs((left.left - bounds.left) - (bounds.right - right.right))
    };
  });
  assert(voiceLayout.widthDifference < 1, 'Voice cloning columns were not equal width');
  assert(voiceLayout.insetDifference < 1, 'Voice cloning columns were not equally inset');

  await page.evaluate(() => { location.hash = 'transcription'; });
  await page.waitForFunction(() => location.hash === '#transcription');
  await page.getByRole('heading', { name: 'Transcribe', exact: true }).waitFor();
  assert(await page.getByRole('heading', { name: 'Transcribe', exact: true }).isVisible(),
    'Transcribe did not reopen from grouped navigation');
}
'@
  $browserCode = $browserCode.Replace("__HOMEPAGE_SCREENSHOT__", ($homepageScreenshotPath | ConvertTo-Json -Compress))
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  await page.route('**/health', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      status: 'ready', updatedAt: new Date().toISOString(),
      engines: Object.fromEntries([
        'audio', 'diarize', 'diarize-sherpa', 'ffmpeg', 'llama', 'music', 'omnivoice',
        'sd', 'vision', 'voiceconvert', 'voicedesign', 'voxcpm2', 'whisper'
      ].map(name => [name, { status: 'ready', ready: true }]))
    })
  }));
  await page.waitForFunction(() => !document.querySelector('#healthButton').disabled);
  const mockedHealth = page.waitForResponse(response =>
    response.request().method() === 'GET' && response.url().endsWith('/health'));
  await page.locator('#healthButton').click();
  await mockedHealth;
  await page.waitForFunction(() => document.querySelectorAll('#engineRack .led-chip').length > 1);
  const rack = page.locator('#engineRack');
  const rackNames = await rack.locator('.led-chip').allTextContents();
  assert(rackNames.filter(name => name.trim() === 'diarize').length === 1,
    'Engine rack did not combine the two diarization implementations');
  assert(!rackNames.some(name => name.includes('diarize-sherpa')),
    'Engine rack exposed the sherpa implementation as a second capability');
  const rackMetrics = await rack.evaluate(node => ({
    rowTops: Array.from(node.children, child => Math.round(child.getBoundingClientRect().top)),
    scrollWidth: node.scrollWidth,
    clientWidth: node.clientWidth
  }));
  assert(new Set(rackMetrics.rowTops).size === 1, 'Engine rack wrapped onto multiple lines');
  assert(rackMetrics.scrollWidth <= rackMetrics.clientWidth + 1, 'Engine rack did not fit its available width');
  await page.unroute('**/health');

  const studioPages = [
    'talk-voice', 'text-to-speech', 'transcription', 'voice-cloning', 'voice-design', 'voice-convert', 'training',
    'music', 'music-generation', 'imagery', 'image-generation', 'stories-audiobooks', 'audiobook',
    'story', 'extract', 'library', 'models', 'engines'
  ];
  for (const studioPage of studioPages) {
    await page.evaluate(name => { location.hash = name; }, studioPage);
    await page.waitForFunction(name => location.hash === '#' + name, studioPage);
    const refreshed = page.waitForResponse(response =>
      response.request().method() === 'GET' && response.url().endsWith('/health'));
    await page.locator('#healthButton').click();
    assert((await refreshed).ok(), 'Refresh health failed from ' + studioPage);
    await page.waitForFunction(() => !document.querySelector('#healthButton').disabled);
  }
  await page.evaluate(() => { location.hash = 'transcription'; });
  await page.waitForFunction(() => location.hash === '#transcription');
}
'@
  Invoke-BrowserCode -Code $browserCode

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
  const playPause = page.locator('#extractPlayButton');
  assert(await playPause.isVisible(), 'Transcribe did not expose playback beside the loaded source');
  assert((await playPause.textContent()) === 'Play audio', 'Transcribe playback did not start in Play state');
  await playPause.click();
  await page.waitForFunction(() => document.querySelector('#extractPlayButton').textContent === 'Pause audio');
  assert((await playPause.getAttribute('aria-pressed')) === 'true', 'Transcribe playback did not expose playing state');
  await page.waitForTimeout(80);
  await playPause.click();
  await page.waitForFunction(() => document.querySelector('#extractPlayButton').textContent === 'Play audio');
  assert((await playPause.getAttribute('aria-pressed')) === 'false', 'Transcribe playback stayed pressed after pause');
  await page.locator('#extractTranscribeButton').click();
  await page.waitForFunction(() => document.querySelectorAll('.extract-segment').length > 0);

  assert((await page.locator('.extract-segment').count()) === 3, 'fixture did not produce three transcript segments');
  const segmentPlay = page.locator('.extract-segment-play');
  assert((await segmentPlay.count()) === 3, 'every transcript segment did not receive a Play control');
  await segmentPlay.nth(0).click();
  await page.waitForFunction(() => document.querySelectorAll('.extract-segment-play')[0].textContent === 'Pause segment');
  assert((await segmentPlay.nth(0).getAttribute('aria-pressed')) === 'true', 'first segment did not expose playing state');
  await segmentPlay.nth(1).click();
  await page.waitForFunction(() => document.querySelectorAll('.extract-segment-play')[1].textContent === 'Pause segment');
  assert((await segmentPlay.nth(0).textContent()) === 'Play segment', 'starting a second segment did not reset the first');
  await segmentPlay.nth(1).click();
  await page.waitForFunction(() => document.querySelectorAll('.extract-segment-play')[1].textContent === 'Play segment');
  assert((await segmentPlay.nth(1).getAttribute('aria-pressed')) === 'false', 'second segment stayed pressed after pause');
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

  const workspace = await page.evaluate(() => window.__cppStudioAudioWorkspace.snapshot());
  assert(workspace.mode === 'transcribe', 'Audio Workspace did not own Transcribe mode');
  assert(workspace.sourceName === 'input.wav' && workspace.sampleRate > 0,
    'Audio Workspace did not own the decoded source');
  assert(workspace.segments.length === 3 && workspace.segments[1].text === editedText,
    'Audio Workspace did not own the edited transcript');

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
  assert(downloadedJSON.source_name === 'input.wav' && downloadedJSON.sample_rate > 0 && downloadedJSON.segments.length === 3,
    'downloaded JSON metadata was wrong: ' + JSON.stringify(downloadedJSON));

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
  const extractWorkspace = await page.evaluate(() => window.__cppStudioAudioWorkspace.snapshot());
  assert(extractWorkspace.mode === 'extract' && extractWorkspace.sourceName === 'input.wav' &&
    extractWorkspace.segments[0].text === activeEdit,
    'Audio Workspace interface lost mode, source, or transcript ordering on Transcribe to Extract');
  assert(await page.locator('#extractCloneButton').isVisible(), 'Extract did not expose clone-reference action');
  assert(await page.locator('#extractLibraryButton').isVisible(), 'Extract did not expose Library clip action');
  assert(await page.locator('#transcribeRenameFrom').isVisible(), 'Extract did not expose speaker rename controls');
  await page.locator('#transcribeRenameFrom').selectOption('Narrator');
  await page.locator('#transcribeRenameTo').fill('Host');
  await page.locator('#transcribeRenameButton').click();
  const extractRenamedSpeakers = await page.locator('.extract-segment-speaker').allTextContents();
  assert(JSON.stringify(extractRenamedSpeakers) === JSON.stringify(['Host', 'Host', 'B']),
    'Extract speaker rename changed the wrong segments: ' + JSON.stringify(extractRenamedSpeakers));

  await page.locator('#extractTimeline .extract-segment-time').first().click();
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
  assert((await page.locator('.extract-segment-speaker').first().textContent()) === 'Host',
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
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  await page.locator('#extractActorInput').fill('Kenneth Williams');
  await page.locator('#extractCharacterInput').fill('Rambling Sid Rumpo');
  assert(await page.locator('#extractAddSpeechButton').isEnabled(), 'labelled waveform range could not be added');
  await page.locator('#extractAddSpeechButton').click();
  const labelledWorkspace = await page.evaluate(() => window.__cppStudioAudioWorkspace.snapshot());
  assert(labelledWorkspace.segments[0].speaker === 'Kenneth Williams',
    'clean speech actor did not replace the provisional transcript speaker');
  assert((await page.locator('#transcribeRenameFrom option').allTextContents()).includes('Kenneth Williams'),
    'clean speech actor did not appear in the transcript rename control');
  assert(await page.locator('#extractTimeline .extract-segment').first().locator('.tag-button.active').filter({ hasText: /^Kenneth Williams$/ }).isVisible(),
    'clean speech actor was not shown as the active transcript speaker');
  await page.locator('#extractTimeline .extract-segment-time').nth(1).click();
  await page.locator('#extractCharacterInput').fill('Snide');
  await page.locator('#extractAddSpeechButton').click();
  const twiceLabelledWorkspace = await page.evaluate(() => window.__cppStudioAudioWorkspace.snapshot());
  assert(JSON.stringify(twiceLabelledWorkspace.segments.map(segment => segment.speaker)) ===
    JSON.stringify(['Kenneth Williams', 'Kenneth Williams', 'B']),
    'clean speech labels leaked through a short playback-tail overlap: ' +
      JSON.stringify(twiceLabelledWorkspace.segments.map(segment => segment.speaker)));
  assert((await page.locator('.extract-speech-item').count()) === 2, 'Extract did not retain two clean speech selections');
  const speechLabels = await page.locator('.extract-speech-item').allTextContents();
  assert(speechLabels[0].includes('Kenneth Williams') && speechLabels[0].includes('Rambling Sid Rumpo') &&
    speechLabels[1].includes('Kenneth Williams') && speechLabels[1].includes('Snide'),
    'clean speech labels were not retained: ' + JSON.stringify(speechLabels));

}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const waveform = page.locator('#extractCanvas');
  const tick = page.locator('.extract-segment-tick').first();
  await tick.uncheck();
  const canvasBox = await waveform.boundingBox();
  await waveform.click({ position: { x: canvasBox.width - 5, y: canvasBox.height / 2 } });
  assert(!(await page.locator('#extractSelectionActions').isVisible()),
    'Extract showed selection actions before a waveform range existed');

  await page.locator('#extractZoomFitButton').click();
  const fittedBox = await waveform.boundingBox();
  const fixtureDuration = Number(await waveform.getAttribute('aria-valuemax'));
  await page.mouse.move(fittedBox.x + (0.9 / fixtureDuration) * fittedBox.width, fittedBox.y + fittedBox.height / 2);
  await page.mouse.down();
  await page.mouse.move(fittedBox.x + (1.7 / fixtureDuration) * fittedBox.width, fittedBox.y + fittedBox.height / 2);
  await page.mouse.up();
  const actions = page.locator('#extractSelectionActions');
  await actions.waitFor({ state: 'visible' });
  assert((await actions.locator('#extractPlayButton').count()) === 1 &&
    (await actions.locator('#extractTranscribeButton').count()) === 1,
    'Extract did not move Play selection and Transcribe into the waveform range');
  const cardActionLayout = await page.locator('.extract-selection-card .extract-selection-actions').evaluateAll(rows => rows.map(row => {
    const card = row.closest('.extract-selection-card').getBoundingClientRect();
    const bounds = row.getBoundingClientRect();
    return {
      position: getComputedStyle(row).position,
      insideCard: bounds.left >= card.left && bounds.right <= card.right && bounds.top >= card.top && bounds.bottom <= card.bottom
    };
  }));
  assert(cardActionLayout.every(row => row.position !== 'absolute' && row.insideCard),
    'ordinary Extract action rows escaped their cards: ' + JSON.stringify(cardActionLayout));
  assert((await actions.locator('#extractTranscribeButton').textContent()) === 'Transcribe selection',
    'Extract did not distinguish selection transcription from whole-audio transcription');
}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const actions = page.locator('#extractSelectionActions');
  const tick = page.locator('.extract-segment-tick').first();
  let selectionUploadBytes = 0;
  await page.route('**/v1/audio/transcriptions?format=segments', async route => {
    selectionUploadBytes = route.request().postDataBuffer().length;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        text: 'Edited second fixture line',
        segments: [{ start: 0, end: 0.8, text: 'Edited second fixture line', speaker: 'Kenneth Williams' }]
      })
    });
  });
  await actions.locator('#extractTranscribeButton').evaluate(element => element.click());
  await page.waitForFunction(() => document.querySelector('#extractTranscribeStatus').textContent.startsWith('Selection transcribed'));
  await page.unroute('**/v1/audio/transcriptions?format=segments');
  assert(selectionUploadBytes > 0 && selectionUploadBytes < 60000,
    'Extract uploaded the whole fixture instead of the highlighted range: ' + selectionUploadBytes + ' bytes');
  const selectedTranscript = await page.evaluate(() => window.__cppStudioAudioWorkspace.snapshot().segments.find(segment =>
    segment.text === 'Edited second fixture line'));
  assert(selectedTranscript && Math.abs(selectedTranscript.start - 0.9) < 0.01 && Math.abs(selectedTranscript.end - 1.7) < 0.01,
    'selection transcript was not restored to its source time range: ' + JSON.stringify(selectedTranscript));
  const position = await page.evaluate(() => {
    const canvas = document.querySelector('#extractCanvas').getBoundingClientRect();
    const actions = document.querySelector('#extractSelectionActions').getBoundingClientRect();
    const state = window.__cppStudioAudioWorkspace.snapshot();
    return {
      center: actions.left + actions.width / 2,
      start: canvas.left + (state.region.start / state.duration) * canvas.width,
      end: canvas.left + (state.region.end / state.duration) * canvas.width,
      top: actions.top,
      canvasTop: canvas.top,
      canvasBottom: canvas.bottom
    };
  });
  assert(position.center >= position.start && position.center <= position.end &&
    position.top >= position.canvasTop && position.top < position.canvasBottom,
    'selection actions were not positioned over the highlighted waveform range');

  await page.locator('#openTranscribeButton').click();
  await page.waitForFunction(() => location.hash === '#transcription');
  await page.waitForFunction(() => document.querySelector('#extractPrimaryActions')?.parentElement?.id === 'extractToolbarActions');
  assert((await page.locator('#extractToolbarActions #extractPrimaryActions').count()) === 1,
    'Transcribe did not return its actions to the source toolbar');
  assert((await page.locator('#extractTranscribeButton').textContent()) === 'Transcribe',
    'Transcribe page did not retain the whole-audio action label');
  await page.locator('#openExtractButton').click();
  await page.waitForFunction(() => location.hash === '#extract');
  await page.waitForFunction(() => document.querySelector('#extractPrimaryActions')?.parentElement?.id === 'extractSelectionActions' &&
    !document.querySelector('#extractSelectionActions').hidden);
  assert((await page.locator('#extractSelectionActions #extractPrimaryActions').count()) === 1 && await actions.isVisible(),
    'Extract did not restore its actions over the retained range');
  await tick.check();
}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const waveform = page.locator('#extractCanvas');
  const duration = Number(await waveform.getAttribute('aria-valuemax'));
  await waveform.focus();
  await waveform.press('End');
  assert(Number(await waveform.getAttribute('aria-valuenow')) === duration, 'End did not seek to the Extract end');
  await waveform.press('ArrowLeft');
  assert(Number(await waveform.getAttribute('aria-valuenow')) === duration - 1, 'Left did not nudge the Extract playhead');
  await waveform.press('Home');
  await waveform.press('ArrowRight');
  assert(Number(await waveform.getAttribute('aria-valuenow')) === 1, 'Home and Right did not seek from the Extract start');
  const before = await page.locator('#extractViewEnd').textContent();
  const box = await waveform.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, -120);
  assert(await page.locator('#extractViewEnd').textContent() !== before, 'wheel up did not zoom into the Extract waveform');
  await page.locator('#extractZoomFitButton').click();
}
'@
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const trainingScreenshotPath = __TRAINING_SCREENSHOT__;
  const clipTranscriptions = [];
  const trackClipTranscription = request => {
    if (request.method() === 'POST' && request.url().includes('/v1/audio/transcriptions')) {
      clipTranscriptions.push(request.url());
    }
  };
  page.on('request', trackClipTranscription);
  const processButton = page.locator('#extractProcessSpeechButton');
  await processButton.evaluate(element => element.click());
  await page.waitForFunction(() => document.querySelector('#extractProcessSpeechStatus').textContent.includes('processed'));
  page.off('request', trackClipTranscription);
  const processStatus = await page.locator('#extractProcessSpeechStatus').textContent();
  assert(processStatus === '2 clips processed', 'clean speech processing did not finish successfully: ' + processStatus);
  assert(clipTranscriptions.length === 2, 'clean ranges were not transcribed independently: ' + clipTranscriptions.length);
  assert((await page.locator('.extract-speech-audio').count()) === 2, 'processed clips did not each receive audio playback');
  assert((await page.locator('.extract-speech-transcript').count()) === 2, 'processed clips did not each receive a transcript editor');
  assert((await page.locator('.extract-speech-verified').count()) === 2, 'processed clips did not each receive human verification');
  assert(await page.locator('#extractPassTrainingButton').isDisabled(), 'unverified clips could be passed to Training');
  await page.locator('.extract-speech-transcript').nth(1).fill('Corrected clean line');
  await page.locator('.extract-speech-verified').nth(0).check();
  assert(await page.locator('#extractPassTrainingButton').isDisabled(), 'partly verified clips could be passed to Training');
  await page.locator('.extract-speech-verified').nth(1).check();
  assert(await page.locator('#extractPassTrainingButton').isEnabled(), 'verified clips could not be passed to Training');
  const processedWorkspace = await page.evaluate(() => window.__cppStudioAudioWorkspace.snapshot());
  assert(processedWorkspace.speechClips.every(clip => clip.verified), 'human verification was not retained');
  assert(processedWorkspace.speechClips[1].transcript === 'Corrected clean line', 'corrected clean transcript was not retained');

  await page.locator('#extractPassTrainingButton').evaluate(element => element.click());
  await page.waitForFunction(() => location.hash === '#training');
  await page.waitForFunction(() => window.scrollY === 0);
  assert(await page.getByRole('heading', { name: 'Prepare training data', exact: true }).isVisible(),
    'Training page heading was not visible after the handoff');
  assert((await page.locator('#trainingClipList .training-clip').count()) === 2,
    'Training did not receive the two verified clips');
  assert((await page.locator('[data-page="training"] canvas').count()) === 0,
    'Training duplicated the waveform editor');
  const trainingText = await page.locator('#trainingClipList').textContent();
  assert(trainingText.includes('Kenneth Williams') && trainingText.includes('Rambling Sid Rumpo') &&
    trainingText.includes('Snide') && trainingText.includes('Corrected clean line'),
    'Training handoff lost clip identity or corrected text: ' + trainingText);
  await page.waitForTimeout(100);
  assert(await page.locator('#trainingClipList .training-clip').first().isVisible(),
    'Training clip cards were not visibly laid out');
  await page.screenshot({ path: trainingScreenshotPath });
}
'@
  $browserCode = $browserCode.Replace("__TRAINING_SCREENSHOT__", ($trainingScreenshotPath | ConvertTo-Json -Compress))
  Invoke-BrowserCode -Code $browserCode

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  await page.evaluate(() => {
    window.__trainingWrites = {};
    const files = window.__trainingWrites;
    const makeDirectory = prefix => ({
      async getDirectoryHandle(name) { return makeDirectory(prefix + name + '/'); },
      async getFileHandle(name) {
        return { async createWritable() {
          return {
            async write(value) { files[prefix + name] = value; },
            async close() {}
          };
        } };
      }
    });
    window.showDirectoryPicker = async () => makeDirectory('');
  });
  await page.locator('#trainingExportButton').evaluate(element => element.click());
  await page.waitForFunction(() => document.querySelector('#trainingExportStatus').textContent.includes('Exported'));
  const exportProof = await page.evaluate(async () => {
    const keys = Object.keys(window.__trainingWrites).sort();
    const manifestValue = window.__trainingWrites['train.jsonl'];
    const manifest = typeof manifestValue === 'string' ? manifestValue : await manifestValue.text();
    return { keys, manifest };
  });
  assert(exportProof.keys.filter(name => name.endsWith('.wav')).length === 2,
    'training folder did not contain two WAVs: ' + JSON.stringify(exportProof.keys));
  assert(exportProof.keys.filter(name => name.endsWith('.txt')).length === 2,
    'training folder did not contain two transcripts: ' + JSON.stringify(exportProof.keys));
  assert(exportProof.keys.includes('train.jsonl'), 'training folder did not contain train.jsonl');
  assert(exportProof.manifest.split('\n').filter(Boolean).length === 2 && exportProof.manifest.includes('Corrected clean line'),
    'training manifest was incomplete: ' + exportProof.manifest);
  await page.evaluate(() => { location.hash = 'extract'; });
  await page.waitForFunction(() => location.hash === '#extract');
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
  assert((await page.locator('#extractSelectionSpeakers').textContent()) === 'Kenneth Williams, B', 'selection speaker provenance was wrong');
  assert((await page.locator('#extractSelectionSpanCount').textContent()) === '2', 'stitched span count was wrong');

  await page.locator('#extractPlayButton').evaluate(element => element.click());
  await page.waitForFunction(() => document.querySelector('#extractPlayButton').textContent === 'Pause selection');
  assert((await page.locator('#extractPlayButton').getAttribute('aria-pressed')) === 'true', 'selection playback did not expose playing state');
  await page.waitForTimeout(80);
  await page.locator('#extractPlayButton').evaluate(element => element.click());
  await page.waitForFunction(() => document.querySelector('#extractPlayButton').textContent === 'Play selection');
  assert(!(await page.locator('#extractStopButton').isDisabled()), 'paused selection could not be stopped');
  await page.locator('#extractPlayButton').evaluate(element => element.click());
  await page.waitForFunction(() => document.querySelector('#extractPlayButton').textContent === 'Pause selection');
  await page.locator('#extractStopButton').evaluate(element => element.click());
  assert((await page.locator('#extractPlayButton').textContent()) === 'Play selection', 'selection stop did not reset Play state');
  assert(await page.locator('#extractStopButton').isDisabled(), 'selection playback did not stop');

  const saveResponsePending = page.waitForResponse(response =>
    response.url().endsWith('/v1/library') && response.request().method() === 'POST');
  await page.locator('#extractLibraryButton').evaluate(element => element.click());
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
  assert((await page.locator('#extractSelectionSpeakers').textContent()) === 'Kenneth Williams', 'single-speaker selection provenance was wrong');
  await page.locator('#extractCloneButton').evaluate(element => element.click());
  await page.waitForFunction(() => location.hash === '#voices');
  const cloneStatus = await page.locator('#cloneWavStatus').textContent();
  assert(cloneStatus.includes('from input.wav') && cloneStatus.includes('speaker Kenneth Williams') && cloneStatus.includes('0:00.0–0:01.1'),
    'clone-reference handoff lost provenance: ' + cloneStatus);
  await page.locator('[data-parent-link="stories-audiobooks"]').click();
  await page.locator('[data-parent-nav="stories-audiobooks"] [data-page-link="extract"]').click();
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
    window.__transcribePendingMic = new Promise(resolve => { window.__resolveTranscribeMic = resolve; });
    Object.defineProperty(navigator.mediaDevices, 'getUserMedia', {
      configurable: true,
      value: () => window.__transcribePendingMic
    });
  });
  await page.locator('#transcribeRecordButton').click();
  await page.waitForFunction(() => window.__cppStudioRecorders.snapshot().transcribe === 'pending');
  await page.locator('#transcribeStopRecordButton').click();
  assert((await page.evaluate(() => window.__cppStudioRecorders.snapshot().transcribe)) === 'pending',
    'Recorder did not retain the pending setup state until permission resolved');
  await page.evaluate(() => window.__resolveTranscribeMic({
    getTracks: () => [{ stop: () => { window.__transcribeTrackStopped = true; } }]
  }));
  await page.waitForFunction(() => window.__cppStudioRecorders.snapshot().transcribe === 'idle');
  assert(await page.evaluate(() => window.__transcribeTrackStopped),
    'Recorder did not clean up a microphone stopped during permission setup');
  await page.evaluate(() => {
    window.__transcribeTrackStopped = false;
    Object.defineProperty(navigator.mediaDevices, 'getUserMedia', {
      configurable: true,
      value: async () => ({ getTracks: () => [{ stop: () => { window.__transcribeTrackStopped = true; } }] })
    });
  });
  await page.locator('#transcribeRecordButton').click();
  await page.waitForFunction(() => !document.querySelector('#transcribeStopRecordButton').disabled);
  assert((await page.evaluate(() => window.__cppStudioRecorders.snapshot().transcribe)) === 'active',
    'Recorder did not publish active after microphone setup');
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
  assert((await page.evaluate(() => window.__cppStudioRecorders.snapshot().transcribe)) === 'idle',
    'Recorder did not return to idle after finishing a valid WAV');
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

  $browserCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  await page.evaluate(() => {
    document.querySelector('#messageInput').value = 'clear voice loop';
    document.querySelector('#imagePromptInput').value = 'clear image';
    document.querySelector('#designDescriptionInput').value = 'clear design';
    document.querySelector('#musicPromptInput').value = 'clear music';
    location.hash = 'models';
  });
  await page.waitForFunction(() => location.hash === '#models');
  page.once('dialog', dialog => dialog.accept());
  await Promise.all([
    page.waitForEvent('load'),
    page.locator('#clearAllButton').click()
  ]);
  await page.waitForLoadState('networkidle');
  assert((await page.evaluate(() => location.hash)) === '#models', 'Reset session did not preserve the current tool');
  assert((await page.locator('#messageInput').inputValue()) === '', 'Reset session retained voice-loop text');
  assert((await page.locator('#imagePromptInput').inputValue()) === '', 'Reset session retained image text');
  assert((await page.locator('#designDescriptionInput').inputValue()) === '', 'Reset session retained voice-design text');
  assert((await page.locator('#musicPromptInput').inputValue()) === '', 'Reset session retained music text');
  assert((await page.locator('#extractFileStatus').textContent()) === 'Nothing loaded', 'Reset session retained Transcribe audio');
}
'@
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
