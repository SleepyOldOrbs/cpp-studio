param(
  [int]$GatewayPort = 8877,
  [string]$OutDir = ".\out\story-builder-browser-smoke"
)

# Real-browser acceptance for Story Builder timeline editing and Library media. The server,
# project store, browser session, and snapshots are isolated under OutDir.

$ErrorActionPreference = "Stop"
$npx = Get-Command npx -ErrorAction Stop

function Assert-PortFree {
  param([int]$Port)

  $listener = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue |
    Where-Object { $_.State -eq "Listen" -or $_.State -eq 2 }
  if ($listener) {
    throw "Story Builder browser smoke port $Port is already in use"
  }
}

Assert-PortFree -Port $GatewayPort
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$runtimeDir = (Resolve-Path $OutDir).Path
$gatewayExe = Join-Path $runtimeDir "cpp-studio-story-builder-smoke.exe"
$fixtureExe = Join-Path $runtimeDir "cpp-studio-fixture.exe"
$configPath = Join-Path $runtimeDir "config.json"
$voiceWavPath = Join-Path $runtimeDir "actor-voice.wav"
$libraryWavPath = Join-Path $runtimeDir "library-audio.wav"

function Write-FixtureWav {
  param([string]$Path, [int]$Samples = 16000)

  $stream = [IO.MemoryStream]::new()
  $writer = [IO.BinaryWriter]::new($stream)
  try {
    $pcmBytes = $Samples * 2
    $writer.Write([Text.Encoding]::ASCII.GetBytes("RIFF"))
    $writer.Write([uint32](36 + $pcmBytes))
    $writer.Write([Text.Encoding]::ASCII.GetBytes("WAVEfmt "))
    $writer.Write([uint32]16)
    $writer.Write([uint16]1)
    $writer.Write([uint16]1)
    $writer.Write([uint32]16000)
    $writer.Write([uint32]32000)
    $writer.Write([uint16]2)
    $writer.Write([uint16]16)
    $writer.Write([Text.Encoding]::ASCII.GetBytes("data"))
    $writer.Write([uint32]$pcmBytes)
    $pcm = [byte[]]::new($pcmBytes)
    $split = [Math]::Floor($Samples / 2)
    for ($sample = 0; $sample -lt $Samples; $sample++) {
      if ($sample -lt $split) {
        $pcm[$sample * 2] = 232
        $pcm[$sample * 2 + 1] = 3
      } else {
        $pcm[$sample * 2] = 184
        $pcm[$sample * 2 + 1] = 11
      }
    }
    $writer.Write($pcm)
    [IO.File]::WriteAllBytes($Path, $stream.ToArray())
  } finally {
    $writer.Dispose()
    $stream.Dispose()
  }
}

go build -o $gatewayExe .\cmd\cpp-studio
if ($LASTEXITCODE -ne 0) {
  throw "failed to build the Story Builder browser-smoke Gateway"
}
go build -o $fixtureExe .\cmd\cpp-studio-fixture
if ($LASTEXITCODE -ne 0) {
  throw "failed to build the Story Builder browser-smoke fixture Engine"
}

$config = [ordered]@{
  gateway = [ordered]@{ host = "127.0.0.1"; port = $GatewayPort }
  engines = [ordered]@{
    audio = [ordered]@{
      command = $fixtureExe
      args = @("speech")
      mode = "subprocess"
      requestTimeoutSeconds = 10
    }
    omnivoice = [ordered]@{
      command = $fixtureExe
      args = @("speech")
      mode = "subprocess"
      requestTimeoutSeconds = 10
    }
    ffmpeg = [ordered]@{
      command = $fixtureExe
      args = @("ffmpeg")
      mode = "subprocess"
      requestTimeoutSeconds = 10
    }
    ci = [ordered]@{
      command = "go"
      args = @("version")
      mode = "subprocess"
      requestTimeoutSeconds = 10
    }
  }
}
$config | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 -Path $configPath
[IO.File]::WriteAllBytes($voiceWavPath, [Convert]::FromBase64String("UklGRiYAAABXQVZFZm10IBAAAAABAAEAgD4AAAB9AAACABAAZGF0YQIAAAAAAA=="))
Write-FixtureWav -Path $libraryWavPath

$baseURL = "http://127.0.0.1:$GatewayPort"
$session = "story-builder-smoke-$PID"
$server = Start-Process -WindowStyle Hidden -PassThru -FilePath $gatewayExe -ArgumentList @("--config", $configPath) -WorkingDirectory $runtimeDir

function Invoke-BrowserCLI {
  param([string[]]$Arguments)

  & $npx.Source --yes --package "@playwright/cli" playwright-cli "-s=$session" @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "Playwright CLI failed: $($Arguments[0])"
  }
}

function Invoke-BrowserCode {
  param([string]$Code)

  $jsonCode = $Code | ConvertTo-Json -Compress
  Invoke-BrowserCLI -Arguments @("run-code", "async page => await (eval($jsonCode))(page)")
}

$arrangementCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const waitSaved = async () => {
    await page.waitForFunction(() => document.querySelector('#storyBuilderSaveStatus')?.textContent === 'Saved');
  };
  const clipLabels = () => page.locator('.timeline-clip').evaluateAll(nodes => nodes.map(node => node.getAttribute('aria-label')));

  await page.locator('#storyBuilderNewName').fill('Story Builder browser smoke');
  await page.getByRole('button', { name: 'Create' }).click();
  await page.getByRole('button', { name: '+ SFX track' }).click();
  await page.getByRole('button', { name: /Add silence to SFX/ }).click();

  let inspector = page.locator('#storyBuilderSelectionBody');
  let startInput = inspector.getByLabel('Starts at (ms)');
  await startInput.fill('137');
  await startInput.press('Tab');
  await page.getByRole('checkbox', { name: 'Snap 250 ms' }).uncheck();
  assert((await clipLabels())[0].includes('137 milliseconds'), 'snap toggle rewrote exact timing');

  await page.getByRole('button', { name: /Add silence to SFX/ }).click();
  let labels = await clipLabels();
  assert(labels[1].includes('1137 milliseconds'), 'unsnapped add lost exact timing');

  inspector = page.locator('#storyBuilderSelectionBody');
  const durationInput = inspector.getByLabel('Duration (ms)');
  await durationInput.fill('29000');
  await durationInput.press('Tab');
  assert((await page.locator('#storyBuilderSaveStatus').textContent()).includes('inside the project length'), 'project-bound resize was not rejected');
  assert(await inspector.getByLabel('Duration (ms)').inputValue() === '1000', 'rejected resize changed the clip');

  await page.getByRole('checkbox', { name: 'Snap 250 ms' }).check();
  let clips = page.locator('.timeline-clip');
  await clips.nth(0).click();
  await clips.nth(1).click({ modifiers: ['Shift'] });
  assert((await inspector.innerText()).includes('2 clips selected'), 'Shift selection did not retain both clips');

  const stageWidth = await page.locator('.timeline-stage').evaluate(node => node.getBoundingClientRect().width);
  let box = await clips.nth(0).boundingBox();
  const movePixels = stageWidth * 500 / 30000;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + movePixels, box.y + box.height / 2);
  await page.mouse.up();
  labels = await clipLabels();
  const movedStarts = labels.map(label => Number(label.match(/from (\d+) milliseconds/)[1]));
  assert(movedStarts.every(value => value % 250 === 0), 'drag move did not snap to 250 ms');
  assert(movedStarts[1] - movedStarts[0] === 1000, 'group move changed relative timing');

  clips = page.locator('.timeline-clip');
  await clips.nth(0).click();
  const endHandle = clips.nth(0).locator('[data-trim-edge=end]');
  box = await endHandle.boundingBox();
  const trimPixels = stageWidth * 250 / 30000;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 - trimPixels, box.y + box.height / 2);
  await page.mouse.up();
  assert((await clipLabels())[0].includes('for 750 milliseconds'), 'silence trim did not resize the clip');

  await page.getByRole('button', { name: /Mute SFX/ }).click();
  await page.getByRole('button', { name: 'Undo' }).click();
  assert(await page.getByRole('button', { name: /Mute SFX/ }).count() === 1, 'undo did not restore mute state');
  await page.getByRole('button', { name: 'Redo' }).click();
  assert(await page.getByRole('button', { name: /Unmute SFX/ }).count() === 1, 'redo did not reapply mute state');

  const beforeZoom = await clipLabels();
  await page.getByRole('slider', { name: 'Zoom' }).fill('160');
  const scroll = await page.locator('#storyBuilderTimelineViewport').evaluate(node => ({ client: node.clientWidth, scroll: node.scrollWidth }));
  assert(scroll.scroll > scroll.client, 'zoomed timeline is not horizontally scrollable');
  assert(JSON.stringify(beforeZoom) === JSON.stringify(await clipLabels()), 'zoom changed persisted timing');

  return { movedStarts, scrollWidth: scroll.scroll };
}
'@

$panelCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const waitSaved = async () => {
    await page.waitForFunction(() => document.querySelector('#storyBuilderSaveStatus')?.textContent === 'Saved');
  };
  const clipLabels = () => page.locator('.timeline-clip').evaluateAll(nodes => nodes.map(node => node.getAttribute('aria-label')));

  const panel = page.locator('#storyBuilderSelectionPanel');
  const handle = page.locator('#storyBuilderSelectionHandle');
  let box = await handle.boundingBox();
  await page.mouse.move(box.x + 20, box.y + 10);
  await page.mouse.down();
  await page.mouse.move(-400, -400);
  await page.mouse.up();
  const topLeft = await panel.boundingBox();
  assert(topLeft.x >= 8 && topLeft.y >= 8, 'Selection panel escaped the top-left viewport boundary');
  box = await handle.boundingBox();
  await page.mouse.move(box.x + 20, box.y + 10);
  await page.mouse.down();
  await page.mouse.move(3000, 3000);
  await page.mouse.up();
  const bottomRight = await panel.boundingBox();
  const viewport = await page.evaluate(() => ({ width: innerWidth, height: innerHeight }));
  assert(bottomRight.x + bottomRight.width <= viewport.width - 7, 'Selection panel escaped the right viewport boundary');
  assert(bottomRight.y + bottomRight.height <= viewport.height - 7, 'Selection panel escaped the bottom viewport boundary');
  await page.getByRole('button', { name: /Unmute SFX/ }).click();
  const afterRender = await panel.boundingBox();
  assert(Math.abs(afterRender.x - bottomRight.x) < 1 && Math.abs(afterRender.y - bottomRight.y) < 1, 'Selection panel moved during a UI rerender');

  await waitSaved();
  const savedLabels = await clipLabels();
  const projectID = await page.locator('.project-item[aria-current="true"]').getAttribute('data-project-id');
  const origin = page.url().split('/demo/')[0];
  await page.goto(origin + '/demo/story-builder.html?project=' + projectID);
  await page.locator('.timeline-clip').first().waitFor();
  assert(JSON.stringify(savedLabels) === JSON.stringify(await clipLabels()), 'saved millisecond timing drifted after reload');
  assert(await page.getByRole('button', { name: 'Undo' }).isDisabled(), 'undo history incorrectly survived reload');

  return { project: projectID, savedLabels };
}
'@

$audioCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const clipLabels = () => page.locator('.timeline-clip').evaluateAll(nodes => nodes.map(node => node.getAttribute('aria-label')));

  const audioID = '__AUDIO_TRIM_PROJECT_ID__';
  const origin = page.url().split('/demo/')[0];
  await page.goto(origin + '/demo/story-builder.html?project=' + audioID);
  await page.locator('.timeline-clip').click();
  const inspector = page.locator('#storyBuilderSelectionBody');
  const sourceIn = inspector.getByLabel('Source in (ms)');
  await sourceIn.fill('250');
  await sourceIn.press('Tab');
  assert((await clipLabels())[0].includes('from 175 milliseconds for 650 milliseconds'), 'source-in trim did not preserve nondestructive timing');
  const sourceOut = inspector.getByLabel('Source out (ms)');
  await sourceOut.fill('1300');
  await sourceOut.press('Tab');
  assert((await page.locator('#storyBuilderSaveStatus').textContent()).includes('source bounds'), 'out-of-bounds source trim was not rejected');
  assert(await inspector.getByLabel('Source out (ms)').inputValue() === '900', 'rejected source trim changed the clip');

  return { project: audioID, audioTrim: await clipLabels() };
}
'@

$statusCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const waitSaved = async () => {
    await page.waitForFunction(() => document.querySelector('#storyBuilderSaveStatus')?.textContent === 'Saved');
  };

  const origin = page.url().split('/demo/')[0];
  await page.goto(origin + '/demo/story-builder.html?project=__STATUS_PROJECT_ID__');
  await page.locator('.clip-status-badge').first().waitFor();
  const states = await page.locator('.clip-status-badge').allInnerTexts();
  assert(['building', 'failed', 'ready'].every(state => states.map(value => value.toLowerCase()).includes(state)), 'ready, building, and failed states were not all visible');

  let readyRow = page.locator('.track-row[data-track-id="ready_track"]');
  const readyClip = readyRow.locator('.timeline-clip');
  assert(await readyClip.getAttribute('role') === 'group', 'Dialogue Clip did not expose non-nested group semantics');
  assert(await readyClip.getAttribute('tabindex') === null, 'Dialogue Clip wrapper remained a keyboard control around its input');
  await readyRow.locator('.dialogue-text-inline').click();
  let readyInspector = page.locator('#storyBuilderSelectionBody');
  await readyInspector.getByLabel('Starts at (ms)').fill('250');
  await readyInspector.getByLabel('Starts at (ms)').press('Tab');
  readyRow = page.locator('.track-row[data-track-id="ready_track"]');
  await readyRow.locator('input[aria-label="Dialogue track name"]').fill('Ready renamed');
  await readyRow.getByRole('button', { name: /Mute Ready/ }).click();
  await readyRow.getByRole('button', { name: /Move Ready renamed down/ }).click();
  await waitSaved();
  readyRow = page.locator('.track-row[data-track-id="ready_track"]');
  assert((await readyRow.locator('.clip-status-badge').innerText()).toLowerCase() === 'ready', 'arrangement edits made ready dialogue stale in the browser');
  await readyRow.locator('.dialogue-text-inline').fill('Ready dialogue changed in the browser.');
  await readyRow.locator('.dialogue-text-inline').press('Tab');
  await waitSaved();
  assert((await readyRow.locator('.clip-status-badge').innerText()).toLowerCase() === 'stale', 'spoken text did not transition ready dialogue to stale');

  return { project: '__STATUS_PROJECT_ID__', states };
}
'@

$dialogueCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const waitSaved = async () => {
    await page.waitForFunction(() => document.querySelector('#storyBuilderSaveStatus')?.textContent === 'Saved');
  };
  const origin = page.url().split('/demo/')[0];

  await page.locator('#storyBuilderNewName').fill('Dialogue voice browser smoke');
  await page.getByRole('button', { name: 'Create' }).click();
  await page.getByRole('button', { name: '+ Dialogue track' }).click();
  await page.locator('#storyBuilderVoiceRefresh').click();

  const group = page.locator('.actor-voice-group', { hasText: 'Mara' });
  assert(await group.count() === 1, 'Actor Voice parent group was not rendered');
  assert(await group.getByText('Weathered keeper', { exact: true }).count() === 1, 'first Character Voice was not grouped beneath its Actor Voice');
  assert(await group.getByText('Young cartographer', { exact: true }).count() === 1, 'second Character Voice was not grouped beneath its Actor Voice');

  const search = page.locator('#storyBuilderVoiceSearch');
  await search.fill('guarded');
  assert(await page.locator('.character-voice-asset').count() === 1, 'voice search did not match Character Voice direction');
  assert(await page.getByText('Weathered keeper', { exact: true }).count() === 1, 'voice search hid its matching Character Voice');
  await search.fill('');

  const lane = page.locator('.track-row[data-track-type="dialogue"] .timeline-stage');
  const keeper = page.locator('.character-voice-asset', { hasText: 'Weathered keeper' });
  await keeper.dragTo(lane);
  await page.locator('.dialogue-text-inline').waitFor();
  assert((await page.locator('.track-voice-binding').innerText()).includes('Mara / Weathered keeper'), 'compatible drop did not bind the track');
  assert((await page.locator('.clip-status-badge').innerText()).toLowerCase() === 'stale', 'new Dialogue Clip was not visibly stale');

  const inlineText = page.locator('.dialogue-text-inline');
  await inlineText.fill('That path vanished twenty years ago.');
  await inlineText.press('Tab');
  await waitSaved();
  assert((await page.locator('.clip-status-badge').innerText()).toLowerCase() === 'stale', 'spoken-text edit was not visibly stale');

  await page.locator('.timeline-clip').click();
  const inspectorText = page.locator('#storyBuilderSelectionBody').getByLabel('Spoken text');
  await inspectorText.fill('Keep the lamp low until the fog breaks.');
  await inspectorText.press('Tab');
  await waitSaved();

  const projectID = await page.locator('.project-item[aria-current="true"]').getAttribute('data-project-id');
  const beforeRejectedDrop = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  const cartographer = page.locator('.character-voice-asset', { hasText: 'Young cartographer' });
  await cartographer.dragTo(lane);
  await page.getByRole('status').filter({ hasText: /already uses/i }).waitFor();
  const afterRejectedDrop = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  assert(afterRejectedDrop.revision === beforeRejectedDrop.revision, 'incompatible drop changed the durable project revision');
  assert(afterRejectedDrop.tracks[0].character_voice_id === beforeRejectedDrop.tracks[0].character_voice_id, 'incompatible drop replaced the bound Character Voice');

  await page.goto(origin + '/demo/story-builder.html?project=' + projectID);
  await page.locator('.dialogue-text-inline').waitFor();
  assert(await page.locator('.dialogue-text-inline').inputValue() === 'Keep the lamp low until the fog breaks.', 'spoken text did not survive reload');
  assert((await page.locator('.track-voice-binding').innerText()).includes('Mara / Weathered keeper'), 'voice binding did not survive reload');

  return { project: projectID, characterVoice: afterRejectedDrop.tracks[0].character_voice_id };
}
'@

$revoiceCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const waitSaved = async () => {
    await page.waitForFunction(() => document.querySelector('#storyBuilderSaveStatus')?.textContent === 'Saved');
  };
  const projectID = await page.locator('.project-item[aria-current="true"]').getAttribute('data-project-id');
  await page.getByRole('button', { name: /Mute Weathered keeper/ }).click();
  await waitSaved();
  assert(!(await page.getByRole('button', { name: 'Undo' }).isDisabled()), 'arrangement edit did not create Undo history for the revoice race test');
  const beforeRevoice = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  const projectNameBeforeRevoice = await page.locator('#storyBuilderNameInput').inputValue();
  const trackCountBeforeRevoice = await page.locator('.track-row').count();
  const revoiceRoute = '**/v1/story-builder-projects/' + projectID;
  await page.route(revoiceRoute, async (route, request) => {
    const body = request.method() === 'PUT' ? request.postDataJSON() : null;
    if (body?.revoice_track_ids?.length) await page.waitForTimeout(400);
    await route.continue();
  });

  await page.locator('.character-voice-asset', { hasText: 'Young cartographer' }).dragTo(page.locator('.track-row[data-track-type="dialogue"] .timeline-stage'));
  const revoiceButton = page.getByRole('button', { name: 'Revoice as Young cartographer' });
  await revoiceButton.evaluate(button => { button.click(); button.click(); });
  await page.waitForFunction(() => document.querySelector('.app-shell')?.inert === true);
  await page.locator('#storyBuilderNameInput').evaluate(input => {
    input.value = 'Racing project edit';
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await page.locator('#storyBuilderAddDialogue').evaluate(button => button.click());
  await page.keyboard.press('Control+z');
  assert(await page.locator('#storyBuilderNameInput').inputValue() === projectNameBeforeRevoice, 'project name changed during atomic revoice');
  assert(await page.locator('.track-row').count() === trackCountBeforeRevoice, 'timeline changed during atomic revoice');
  assert(await page.getByRole('button', { name: /Unmute Weathered keeper/ }).count() === 1, 'keyboard Undo changed the timeline during atomic revoice');
  await waitSaved();
  await page.unroute(revoiceRoute);

  const revoiced = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  assert(revoiced.tracks[0].character_voice_id !== beforeRevoice.tracks[0].character_voice_id, 'deliberate revoice did not change the Character Voice');
  assert(revoiced.tracks[0].clips[0].status === 'stale', 'deliberate revoice did not stale existing dialogue');
  assert(revoiced.revision === beforeRevoice.revision + 1, 'rapid revoice confirmation produced more than one save');
  assert(await page.getByRole('button', { name: 'Undo' }).isDisabled(), 'revoice left incompatible timeline history available to Undo');

  await page.getByRole('button', { name: '+ Dialogue track' }).click();
  await page.getByRole('button', { name: 'Add Weathered keeper to an empty Dialogue track' }).click();
  assert(await page.locator('.dialogue-text-inline').count() === 2, 'keyboard Add did not create dialogue on an empty compatible track');

  return { project: projectID, characterVoice: revoiced.tracks[0].character_voice_id };
}
'@

$buildDialogueCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const projectID = await page.locator('.project-item[aria-current="true"]').getAttribute('data-project-id');
  await page.getByRole('button', { name: /Build stale/ }).click();
  const buildStatus = page.locator('#storyBuilderBuildStatus');
  await buildStatus.filter({ hasText: /Building/ }).waitFor();
  await buildStatus.filter({ hasText: /Dialogue ready/ }).waitFor({ timeout: 15000 });
  const states = await page.locator('.clip-status-badge').allInnerTexts();
  assert(states.length === 2 && states.every(state => state.toLowerCase() === 'ready'), 'Gateway build state did not refresh every Dialogue Clip to ready');

  const project = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  const dialogue = project.tracks.flatMap(track => track.clips).filter(clip => clip.type === 'dialogue');
  assert(dialogue.length === 2 && dialogue.every(clip => clip.status === 'ready' && clip.source_id), 'built takes were not durably attached to the project');

  await page.locator('.dialogue-text-inline').first().click({ force: true });
  const auditionResponse = page.waitForResponse(response => response.url().includes('/clips/') && response.url().endsWith('/audio'));
  await page.getByRole('button', { name: 'Audition selected clip' }).click();
  const response = await auditionResponse;
  assert(response.ok() && (response.headers()['content-type'] || '').includes('audio/wav'), 'ready Dialogue Clip audition did not return WAV audio');

  return { project: projectID, built: dialogue.map(clip => clip.id) };
}
'@

$buildFailureRecoveryCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const origin = page.url().split('/demo/')[0];
  const voiceID = '__RECOVERY_CHARACTER_VOICE_ID__';
  const createProject = async (name, lines) => {
    const created = await page.evaluate(async name => {
      const response = await fetch('/v1/story-builder-projects', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }),
      });
      return response.json();
    }, name);
    return page.evaluate(async ({ created, lines, voiceID }) => {
      const clips = lines.map((text, index) => ({
        id: `recovery_line_${index + 1}`, type: 'dialogue', label: `Recovery line ${index + 1}`,
        text, start_ms: index * 2000, duration_ms: 1000,
      }));
      const response = await fetch(`/v1/story-builder-projects/${created.id}`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: created.name, revision: created.revision, timeline_duration_ms: 30000,
          tracks: [{ id: 'recovery_dialogue', name: 'Recovery dialogue', type: 'dialogue', order: 0,
            muted: false, character_voice_id: voiceID, clips }],
        }),
      });
      return response.json();
    }, { created, lines, voiceID });
  };
  const projectState = id => page.evaluate(id => fetch(`/v1/story-builder-projects/${id}`).then(response => response.json()), id);
  const replaceClipText = (id, index, text) => page.evaluate(async ({ id, index, text }) => {
    const project = await fetch(`/v1/story-builder-projects/${id}`).then(response => response.json());
    project.tracks[0].clips[index].text = text;
    project.tracks[0].clips[index].label = text;
    const response = await fetch(`/v1/story-builder-projects/${id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: project.name, revision: project.revision,
        timeline_duration_ms: project.timeline_duration_ms, tracks: project.tracks }),
    });
    return response.json();
  }, { id, index, text });

  const failedProject = await createProject('Build failure recovery browser smoke', [
    'First durable line.', '[fixture-fail] Second line fails.', 'Third line must not start.',
  ]);
  await page.goto(`${origin}/demo/story-builder.html?project=${failedProject.id}`);
  await page.getByRole('button', { name: /Build stale/ }).click();
  await page.waitForFunction(async id => {
    const build = await fetch(`/v1/story-builder-projects/${id}/builds`).then(response => response.json());
    return build.status === 'failed';
  }, failedProject.id);
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /failed after 1\/3/i }).waitFor({ timeout: 15000 });
  const failedBuildID = await page.evaluate(id => fetch(`/v1/story-builder-projects/${id}/builds`).then(response => response.json()).then(build => build.id), failedProject.id);
  let failedState = await projectState(failedProject.id);
  let failedClips = failedState.tracks[0].clips;
  assert(failedClips[0].status === 'ready' && failedClips[1].status === 'failed' && failedClips[2].status === 'stale',
    'partial failure did not preserve ready/failed/stale states');
  const failureFirstTake = failedClips[0].source_id;

  await page.reload();
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /failed after 1\/3/i }).waitFor();
  const correctedFailure = await replaceClipText(failedProject.id, 1, 'Recovered second line.');
  assert(correctedFailure.tracks[0].clips[1].status === 'stale', 'corrected failed clip was not retryable');
  await page.reload();
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /failed after 1\/3/i }).waitFor();
  await page.waitForFunction(() => {
    const button = document.querySelector('#storyBuilderBuildButton');
    return button && !button.disabled;
  });
  assert(await page.getByRole('button', { name: /Build stale \(2\)/ }).isEnabled(), 'failed build reload did not offer retry');
  await page.getByRole('button', { name: /Build stale/ }).click();
  await page.waitForFunction(async ({ id, previous }) => {
    const build = await fetch(`/v1/story-builder-projects/${id}/builds`).then(response => response.json());
    return build.id !== previous && build.status === 'complete';
  }, { id: failedProject.id, previous: failedBuildID });
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /Dialogue ready/ }).waitFor({ timeout: 15000 });
  failedState = await projectState(failedProject.id);
  failedClips = failedState.tracks[0].clips;
  assert(failedClips.every(clip => clip.status === 'ready'), 'failure retry did not finish failed and unstarted clips');
  assert(failedClips[0].source_id === failureFirstTake, 'failure retry regenerated the ready first take');

  return { failureProject: failedProject.id };
}
'@

$buildCancellationRecoveryCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const origin = page.url().split('/demo/')[0];
  const voiceID = '__RECOVERY_CHARACTER_VOICE_ID__';
  const created = await page.evaluate(async name => {
    const response = await fetch('/v1/story-builder-projects', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }),
    });
    return response.json();
  }, 'Build cancellation recovery browser smoke');
  const lines = ['First durable line.', '[fixture-wait] Second line waits for cancellation.', 'Third line must not start.'];
  const cancelledProject = await page.evaluate(async ({ created, lines, voiceID }) => {
    const clips = lines.map((text, index) => ({
      id: `recovery_line_${index + 1}`, type: 'dialogue', label: `Recovery line ${index + 1}`,
      text, start_ms: index * 2000, duration_ms: 1000,
    }));
    const response = await fetch(`/v1/story-builder-projects/${created.id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: created.name, revision: created.revision, timeline_duration_ms: 30000,
        tracks: [{ id: 'recovery_dialogue', name: 'Recovery dialogue', type: 'dialogue', order: 0,
          muted: false, character_voice_id: voiceID, clips }],
      }),
    });
    return response.json();
  }, { created, lines, voiceID });
  const projectState = id => page.evaluate(id => fetch(`/v1/story-builder-projects/${id}`).then(response => response.json()), id);
  const replaceClipText = (id, index, text) => page.evaluate(async ({ id, index, text }) => {
    const project = await fetch(`/v1/story-builder-projects/${id}`).then(response => response.json());
    project.tracks[0].clips[index].text = text;
    project.tracks[0].clips[index].label = text;
    const response = await fetch(`/v1/story-builder-projects/${id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: project.name, revision: project.revision,
        timeline_duration_ms: project.timeline_duration_ms, tracks: project.tracks }),
    });
    return response.json();
  }, { id, index, text });

  await page.goto(`${origin}/demo/story-builder.html?project=${cancelledProject.id}`);
  await page.getByRole('button', { name: /Build stale/ }).click();
  await page.waitForFunction(async id => {
    const response = await fetch(`/v1/story-builder-projects/${id}/builds`);
    const build = await response.json();
    return build.completed === 1 && build.active_clip_id === 'recovery_line_2';
  }, cancelledProject.id);
  await page.getByRole('button', { name: 'Cancel build' }).click();
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /cancelled.*1\/3 completed takes kept/i }).waitFor({ timeout: 15000 });
  const cancelledBuildID = await page.evaluate(id => fetch(`/v1/story-builder-projects/${id}/builds`).then(response => response.json()).then(build => build.id), cancelledProject.id);
  let cancelledState = await projectState(cancelledProject.id);
  let cancelledClips = cancelledState.tracks[0].clips;
  assert(cancelledClips[0].status === 'ready' && cancelledClips[1].status === 'stale' && cancelledClips[2].status === 'stale',
    'cancellation did not preserve ready/stale/stale states');
  const cancellationFirstTake = cancelledClips[0].source_id;

  await page.reload();
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /cancelled.*1\/3 completed takes kept/i }).waitFor();
  const correctedCancellation = await replaceClipText(cancelledProject.id, 1, 'Recovered cancelled line.');
  assert(correctedCancellation.tracks[0].clips[1].status === 'stale', 'corrected cancelled clip was not retryable');
  await page.reload();
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /cancelled.*1\/3 completed takes kept/i }).waitFor();
  await page.waitForFunction(() => {
    const button = document.querySelector('#storyBuilderBuildButton');
    return button && !button.disabled;
  });
  assert(await page.getByRole('button', { name: /Build stale \(2\)/ }).isEnabled(), 'cancelled build reload did not offer retry');
  await page.getByRole('button', { name: /Build stale/ }).click();
  await page.waitForFunction(async ({ id, previous }) => {
    const build = await fetch(`/v1/story-builder-projects/${id}/builds`).then(response => response.json());
    return build.id !== previous && build.status === 'complete';
  }, { id: cancelledProject.id, previous: cancelledBuildID });
  await page.locator('#storyBuilderBuildStatus').filter({ hasText: /Dialogue ready/ }).waitFor({ timeout: 15000 });
  cancelledState = await projectState(cancelledProject.id);
  cancelledClips = cancelledState.tracks[0].clips;
  assert(cancelledClips.every(clip => clip.status === 'ready'), 'cancelled build retry did not finish active and unstarted clips');
  assert(cancelledClips[0].source_id === cancellationFirstTake, 'cancelled build retry regenerated the ready first take');

  return { cancelledProject: cancelledProject.id };
}
'@

$playbackSetupCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  await page.addInitScript(() => {
    window.__storyPlayback = { starts: [], stops: 0, decoded: [] };
    class FixtureSource {
      connect() {}
      start(when, offset, duration) {
        window.__storyPlayback.starts.push({ when, offset, duration });
      }
      stop() { window.__storyPlayback.stops += 1; }
    }
    class FixtureAudioContext {
      constructor() {
        this.born = performance.now();
        this.destination = {};
      }
      get currentTime() { return (performance.now() - this.born) / 1000; }
      resume() { return Promise.resolve(); }
      decodeAudioData(bytes) {
        window.__storyPlayback.decoded.push(bytes.byteLength);
        return Promise.resolve({ duration: 1 });
      }
      createBufferSource() { return new FixtureSource(); }
    }
    window.AudioContext = FixtureAudioContext;
  });

  const projectID = '__PLAYBACK_PROJECT_ID__';
  const playbackState = () => page.evaluate(() => structuredClone(window.__storyPlayback));
  await page.goto(`${page.url().split('/demo/')[0]}/demo/story-builder.html?project=${projectID}`);
  const play = page.getByRole('button', { name: 'Play timeline' });
  const pause = page.getByRole('button', { name: 'Pause timeline' });
  const playhead = page.getByRole('slider', { name: 'Playhead' });
  await play.waitFor();
  assert(await play.isEnabled(), 'Play timeline was not available');
  assert(await pause.isDisabled(), 'Pause timeline was enabled before playback');

  await page.keyboard.press('Space');
  await page.locator('#storyBuilderPlaybackStatus').filter({ hasText: /Playing from/ }).waitFor();
  await page.waitForFunction(() => window.__storyPlayback.starts.length === 3);
  let state = await playbackState();
  assert(state.decoded.length === 3 && state.decoded.every(size => size > 44), 'timeline did not decode all fixture WAVs');
  assert(state.starts.length === 3, 'mute or silence scheduled unexpected audio');
  const initialDurations = state.starts.map(item => Number(item.duration.toFixed(2))).sort();
  assert(JSON.stringify(initialDurations) === JSON.stringify([0.5, 1, 1]), 'initial clip durations ignored trim, mute, or silence');
  const futureStarts = state.starts.filter(item => item.when - state.starts[0].when > 0.15);
  assert(futureStarts.length === 2 && Math.abs(futureStarts[0].when - futureStarts[1].when) < 0.01,
    'cross-track overlap was not scheduled simultaneously');

  await page.waitForTimeout(140);
  await page.keyboard.press('Space');
  await page.locator('#storyBuilderPlaybackStatus').filter({ hasText: /Paused at/ }).waitFor();
  const pausedAt = Number(await playhead.inputValue());
  assert(pausedAt >= 50 && pausedAt < 700, 'Pause did not retain the current playhead');

  return { project: projectID, pausedAt };
}
'@

$playbackTimelineCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const projectID = '__PLAYBACK_PROJECT_ID__';
  const projectState = id => page.evaluate(id => fetch(`/v1/story-builder-projects/${id}`).then(response => response.json()), id);
  const playbackState = () => page.evaluate(() => structuredClone(window.__storyPlayback));
  const play = page.getByRole('button', { name: 'Play timeline' });
  const pause = page.getByRole('button', { name: 'Pause timeline' });
  const playhead = page.getByRole('slider', { name: 'Playhead' });
  const revisionBefore = (await projectState(projectID)).revision;

  await playhead.fill('500');
  await page.locator('#storyBuilderPlaybackStatus').filter({ hasText: /00:00\.500/ }).waitFor();
  await play.click();
  await page.waitForFunction(() => window.__storyPlayback.starts.length === 6);
  const state = await playbackState();
  const sought = state.starts.slice(-3);
  const soughtOffsets = sought.map(item => Number(item.offset.toFixed(2))).sort();
  const soughtDurations = sought.map(item => Number(item.duration.toFixed(2))).sort();
  assert(JSON.stringify(soughtOffsets) === JSON.stringify([0.25, 0.5, 0.75]), 'seek did not apply dialogue and source-trim offsets');
  assert(JSON.stringify(soughtDurations) === JSON.stringify([0.25, 0.5, 0.75]), 'seek did not shorten remaining clip durations');
  const playheadGeometry = await page.evaluate(() => {
    const stage = document.querySelector('.timeline-stage').getBoundingClientRect();
    const line = document.querySelector('#storyBuilderPlayheadLine').getBoundingClientRect();
    const playhead = Number(document.querySelector('#storyBuilderPlayhead').value);
    return { actual: line.left, expected: stage.left + stage.width * playhead / 3000 };
  });
  assert(Math.abs(playheadGeometry.actual - playheadGeometry.expected) < 1,
    'visible playhead did not share the rendered clip-lane scale');

  const startsBeforeViewChange = state.starts.length;
  await page.getByRole('slider', { name: 'Zoom' }).fill('160');
  await page.locator('#storyBuilderTimelineViewport').evaluate(node => { node.scrollLeft = 300; });
  await page.waitForTimeout(80);
  assert((await playbackState()).starts.length === startsBeforeViewChange, 'zoom or horizontal scroll changed playback scheduling');
  await pause.click();

  await playhead.fill('0');
  await play.click();
  await page.waitForFunction(count => window.__storyPlayback.starts.length === count + 3, startsBeforeViewChange);
  const beforeRestart = await playbackState();
  await play.click();
  await page.waitForFunction(count => window.__storyPlayback.starts.length === count + 3, beforeRestart.starts.length);
  const restarted = await playbackState();
  assert(restarted.stops >= 3, 'new Play did not stop conflicting browser sources');

  await page.locator('.timeline-clip.clip-sfx').first().click();
  const beforeAudition = await playbackState();
  await page.getByRole('button', { name: 'Audition selected clip' }).click();
  await page.waitForFunction(count => window.__storyPlayback.starts.length === count + 1, beforeAudition.starts.length);
  const auditioned = await playbackState();
  const isolated = auditioned.starts.at(-1);
  assert(Number(isolated.offset.toFixed(2)) === 0.5 && Number(isolated.duration.toFixed(2)) === 0.5,
    'isolated audition ignored selected source trim');
  assert(auditioned.stops > beforeAudition.stops, 'audition did not stop timeline playback');
  assert((await page.locator('#storyBuilderPlaybackStatus').innerText()).includes('only'), 'audition did not report isolation');
  assert((await projectState(projectID)).revision === revisionBefore, 'playback or audition mutated the project revision');

  await page.locator('#storyBuilderTimelineDuration').evaluate(input => {
    input.value = '4';
    input.dispatchEvent(new Event('change', { bubbles: true }));
  });
  assert(!(await page.getByRole('button', { name: 'Undo' }).isDisabled()), 'timeline edit did not create Undo history');
  await playhead.fill('0');
  const beforeUndoPlayback = await playbackState();
  await play.click();
  await page.waitForFunction(count => window.__storyPlayback.starts.length > count, beforeUndoPlayback.starts.length);
  const playingBeforeUndo = await playbackState();
  await page.getByRole('button', { name: 'Undo' }).click();
  assert((await playbackState()).stops > playingBeforeUndo.stops, 'Undo did not stop playback scheduled against the old arrangement');

  return { project: projectID, starts: auditioned.starts.length };
}
'@

$playbackUnavailableCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const projectID = '__PLAYBACK_PROJECT_ID__';

  const stale = await page.evaluate(async id => {
    const project = await fetch(`/v1/story-builder-projects/${id}`).then(response => response.json());
    project.tracks[0].clips[0].text = 'This dialogue is now stale.';
    const response = await fetch(`/v1/story-builder-projects/${id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: project.name, revision: project.revision,
        timeline_duration_ms: project.timeline_duration_ms, tracks: project.tracks }),
    });
    return response.json();
  }, projectID);
  assert(stale.tracks[0].clips[0].status === 'stale', 'playback stale-state fixture was not stale');
  await page.reload();
  await page.getByRole('button', { name: 'Play timeline' }).click();
  await page.locator('#storyBuilderPlaybackStatus').filter({ hasText: /unavailable clips:.*stale/i }).waitFor();

  await page.goto(`${page.url().split('/demo/')[0]}/demo/story-builder.html?project=__BROKEN_PROJECT_ID__`);
  await page.getByRole('button', { name: 'Play timeline' }).click();
  await page.locator('#storyBuilderPlaybackStatus').filter({ hasText: /unavailable clips:.*missing or unreadable/i }).waitFor();

  return { project: projectID, unavailable: true };
}
'@

$renderMasterCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const origin = page.url().split('/demo/')[0];
  const projectID = '__PLAYBACK_PROJECT_ID__';
  const brokenID = '__BROKEN_PROJECT_ID__';
  const projectState = id => page.evaluate(id => fetch(`/v1/story-builder-projects/${id}`).then(response => response.json()), id);
  const digest = url => page.evaluate(async url => {
    const response = await fetch(url);
    if (!response.ok) throw new Error(`render fetch failed ${response.status}`);
    const bytes = await response.arrayBuffer();
    const hash = await crypto.subtle.digest('SHA-256', bytes);
    const view = new DataView(bytes);
    const samples = [0, 250, 800, 1100, 1300].map(ms => view.getInt16(44 + ms * 16 * 2, true));
    return { bytes: bytes.byteLength, hash: Array.from(new Uint8Array(hash), value => value.toString(16).padStart(2, '0')).join(''), samples, url: response.url };
  }, url);

  await page.goto(`${origin}/demo/story-builder.html?project=${projectID}`);
  const renderButton = page.getByRole('button', { name: 'Render master' });
  assert(!(await renderButton.isDisabled()), 'render button was disabled for a saved ready arrangement');
  await renderButton.click();
  await page.waitForFunction(() => document.querySelector('#storyBuilderRenderStatus')?.textContent === 'Rendered revision 1');
  let project = await projectState(projectID);
  assert(project.renders?.length === 1 && project.renders[0].revision === 1, 'first browser render was not recorded');
  assert(await page.locator('#storyBuilderLatestMaster').getAttribute('href') === `/v1/story-builder-projects/${projectID}/master`, 'latest-master action is not stable');
  const first = await digest(project.renders[0].url);
  assert(first.bytes === 96044, `three-second mixed WAV had ${first.bytes} bytes`);
  assert(JSON.stringify(first.samples) === JSON.stringify([1000, 5000, 6000, 3000, 0]),
    `mixed WAV ignored placement, trim, mute, silence, or overlap: ${first.samples}`);
  const latest = await digest(`/v1/story-builder-projects/${projectID}/master`);
  assert(latest.hash === first.hash && latest.url.endsWith('/renders/1'), 'latest-master did not identify revision 1');

  await renderButton.click();
  await page.waitForFunction(() => document.querySelector('#storyBuilderRenderStatus')?.textContent === 'Rendered revision 2');
  project = await projectState(projectID);
  assert(project.renders?.length === 2 && project.renders[1].revision === 2, 'second browser render was not recorded');
  const unchangedFirst = await digest(project.renders[0].url);
  assert(unchangedFirst.hash === first.hash, 'second render rewrote revision 1');
  const latestSecond = await digest(`/v1/story-builder-projects/${projectID}/master`);
  assert(latestSecond.url.endsWith('/renders/2'), 'latest-master did not advance to revision 2');

  await page.goto(`${origin}/demo/story-builder.html?project=${brokenID}`);
  await page.getByRole('button', { name: 'Render master' }).click();
  await page.waitForFunction(() => document.querySelector('#storyBuilderRenderStatus')?.dataset.state === 'failed');
  const broken = await projectState(brokenID);
  assert(!broken.renders?.length, 'missing-media render published a revision');
  await page.goto(`${origin}/demo/story-builder.html?project=${projectID}`);
}
'@

$exportMasterCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const origin = page.url().split('/demo/')[0];
  const projectID = '__PLAYBACK_PROJECT_ID__';
  const projectState = () => page.evaluate(id => fetch(`/v1/story-builder-projects/${id}`).then(response => response.json()), projectID);
  const artifact = url => page.evaluate(async url => {
    const response = await fetch(url);
    const bytes = await response.arrayBuffer();
    const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)))
      .map(value => value.toString(16).padStart(2, '0')).join('');
    const prefix = String.fromCharCode(...new Uint8Array(bytes.slice(0, 4)));
    return { ok: response.ok, contentType: response.headers.get('content-type'), hash, prefix };
  }, url);

  await page.route('**/v1/audio/formats', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ formats: [
      { id: 'mp3', available: true },
      { id: 'flac', available: false },
    ] }),
  }));
  await page.goto(`${origin}/demo/story-builder.html?project=${projectID}`);
  const unavailableRevision = page.locator('[data-render-revision="2"]');
  const unavailableFLAC = unavailableRevision.getByRole('button', { name: 'FLAC unavailable', exact: true });
  await unavailableFLAC.waitFor();
  assert(await unavailableFLAC.isDisabled(), 'unavailable FLAC encoder remained actionable');
  await page.unroute('**/v1/audio/formats');
  await page.reload();
  const revision1 = page.locator('[data-render-revision="1"]');
  const revision2 = page.locator('[data-render-revision="2"]');
  await revision1.getByRole('button', { name: 'Export MP3', exact: true }).waitFor();
  await revision2.getByRole('button', { name: 'Export FLAC', exact: true }).waitFor();
  const before = await projectState();
  assert(await revision1.getByRole('link', { name: 'Download WAV revision 1' }).getAttribute('href') === before.renders[0].url,
    'WAV action did not identify its selected render revision');
  const wav1 = await artifact(before.renders[0].url);
  const wav2 = await artifact(before.renders[1].url);

  await revision1.getByRole('button', { name: 'Export MP3', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('#storyBuilderRenderStatus')?.textContent === 'Revision 1 MP3 is ready');
  const mp3Link = revision1.getByRole('link', { name: 'Download MP3 revision 1' });
  await mp3Link.waitFor();
  const mp3 = await artifact(await mp3Link.getAttribute('href'));
  assert(mp3.ok && mp3.contentType?.startsWith('audio/mpeg') && mp3.prefix.startsWith('ID3'),
    `MP3 download was not a served MP3: ${JSON.stringify(mp3)}`);

  await revision2.getByRole('button', { name: 'Export FLAC', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('#storyBuilderRenderStatus')?.textContent === 'Revision 2 FLAC is ready');
  const flacLink = revision2.getByRole('link', { name: 'Download FLAC revision 2' });
  await flacLink.waitFor();
  const flac = await artifact(await flacLink.getAttribute('href'));
  assert(flac.ok && flac.contentType?.startsWith('audio/flac') && flac.prefix === 'fLaC',
    `FLAC download was not a served FLAC: ${JSON.stringify(flac)}`);

  await revision1.getByRole('button', { name: 'Re-export MP3', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('#storyBuilderRenderStatus')?.textContent === 'Revision 1 MP3 is ready');
  const after = await projectState();
  assert(after.renders[0].exports?.length === 1 && after.renders[0].exports[0].format === 'mp3',
    'MP3 re-export accumulated duplicate derived files');
  assert(after.renders[1].exports?.length === 1 && after.renders[1].exports[0].format === 'flac',
    'exports crossed render revision boundaries');
  assert((await artifact(after.renders[0].url)).hash === wav1.hash && (await artifact(after.renders[1].url)).hash === wav2.hash,
    'delivery export changed an immutable WAV revision');
  return { project: projectID, exports: ['mp3', 'flac'], replacement: true };
}
'@

$storyImportCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const storyID = '__IMPORT_STORY_ID__';
  const origin = page.url().split('/demo/')[0];
  const sourceSnapshot = await page.evaluate(async id => {
    const manifest = await fetch(`/v1/stories/${id}`).then(response => response.json());
    const takeURL = manifest.manifest.script[0].takes[0].url;
    const take = await fetch(takeURL).then(response => response.arrayBuffer());
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', take)))
      .map(value => value.toString(16).padStart(2, '0')).join('');
    return { manifest: JSON.stringify(manifest.manifest), digest };
  }, storyID);

  await page.goto(`${origin}/demo/#story`);
  await page.locator(`.story-library-item[data-story-id="${storyID}"]`).click();
  const open = page.getByRole('button', { name: 'Open in Story Builder' });
  await open.waitFor();
  await open.click();
  const rows = page.locator('.story-builder-import-speaker');
  await rows.nth(1).waitFor();
  assert(await rows.count() === 2, 'speaker mapping did not preserve Story cast order');
  assert(await rows.nth(0).getByLabel('Character Voice for Mara', { exact: true }).inputValue() === '__IMPORT_SUGGESTED_CHARACTER_ID__',
    'unambiguous Story Actor provenance was not preselected');
  assert(await rows.nth(1).getByLabel('Character Voice for Jon', { exact: true }).inputValue() === '',
    'ambiguous Story Actor provenance was silently defaulted');

  await page.getByRole('button', { name: 'Create Story Builder Project' }).click();
  await page.locator('#storyErrorBox').filter({ hasText: /every speaker/i }).waitFor();

  const unresolved = rows.nth(1);
  await unresolved.locator('summary').click();
  await unresolved.getByLabel(/Actor Voice for new/).selectOption('__IMPORT_AMBIGUOUS_ACTOR_ID__');
  await unresolved.getByLabel('Character name').fill('Imported Jon');
  await unresolved.getByLabel('Voice direction').fill('Warm and deliberate');
  await unresolved.getByRole('button', { name: 'Create and select' }).click();
  await page.waitForFunction(() => {
    const selects = document.querySelectorAll('.story-builder-import-speaker select[aria-label^="Character Voice for"]');
    return selects.length === 2 && Boolean(selects[1].value);
  });

  await Promise.all([
    page.waitForURL(/story-builder\.html\?project=/),
    page.getByRole('button', { name: 'Create Story Builder Project' }).click(),
  ]);
  const projectID = decodeURIComponent(page.url().split('project=')[1].split('&')[0]);
  const project = await page.evaluate(id => fetch(`/v1/story-builder-projects/${id}`).then(response => response.json()), projectID);
  assert(project.tracks.length === 2 && project.tracks[0].name === 'Mara' && project.tracks[1].name === 'Jon',
    'imported Dialogue Tracks lost speaker order');
  assert(project.tracks[0].clips[0].status === 'ready' && Boolean(project.tracks[0].clips[0].source_id),
    'compatible retained take was not copied ready');
  assert(project.tracks[1].clips[0].status === 'stale' && !project.tracks[1].clips[0].source_id,
    'edited retained line was not imported stale');
  assert(project.tracks[0].clips[0].source_story_id === storyID && project.tracks[0].clips[0].source_story_line_id,
    'source Story IDs were not retained as provenance');

  const sourceAfter = await page.evaluate(async id => {
    const manifest = await fetch(`/v1/stories/${id}`).then(response => response.json());
    const takeURL = manifest.manifest.script[0].takes[0].url;
    const take = await fetch(takeURL).then(response => response.arrayBuffer());
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', take)))
      .map(value => value.toString(16).padStart(2, '0')).join('');
    return { manifest: JSON.stringify(manifest.manifest), digest };
  }, storyID);
  assert(sourceAfter.manifest === sourceSnapshot.manifest && sourceAfter.digest === sourceSnapshot.digest,
    'browser import mutated the retained Story or its take');
  return { story: storyID, project: projectID };
}
'@

$libraryAudioCode = @'
async page => {
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const waitSaved = async () => {
    await page.waitForFunction(() => document.querySelector('#storyBuilderSaveStatus')?.textContent === 'Saved');
  };
  const origin = page.url().split('/demo/')[0];

  await page.goto(origin + '/demo/story-builder.html');
  await page.locator('#storyBuilderNewName').fill('Reusable media browser smoke');
  await page.getByRole('button', { name: 'Create' }).click();
  await page.getByRole('button', { name: '+ SFX track' }).click();
  await page.getByRole('button', { name: '+ Music track' }).click();
  await waitSaved();
  await page.locator('#storyBuilderVoiceRefresh').click();

  const sfxGroup = page.locator('.reusable-audio-group.role-sfx');
  const musicGroup = page.locator('.reusable-audio-group.role-music');
  const utilityGroup = page.locator('.reusable-audio-group.role-utility');
  assert(await sfxGroup.getByText('Door slam', { exact: true }).count() === 1, 'SFX Library group is missing its asset');
  assert((await sfxGroup.innerText()).includes('1.00s'), 'SFX duration metadata is not displayed');
  assert(await musicGroup.getByText('Low strings', { exact: true }).count() === 1, 'Music Library group is missing its asset');
  assert(await utilityGroup.getByText('Scratch take', { exact: true }).count() === 1, 'utility Library group is missing its asset');
  assert(await utilityGroup.locator('.reusable-audio-add').count() === 0, 'utility audio incorrectly exposes a placement action');

  const search = page.locator('#storyBuilderVoiceSearch');
  await search.fill('ambience');
  assert(await page.locator('.reusable-audio-asset').count() === 1, 'Library search did not filter by media role');
  assert(await page.getByText('Low strings', { exact: true }).count() === 1, 'Library search hid matching Music / ambience');
  await search.fill('');

  await page.getByRole('button', { name: 'Add Door slam to a compatible SFX track' }).click();
  await page.locator('.timeline-clip.clip-sfx').waitFor();
  await waitSaved();
  const projectID = await page.locator('.project-item[aria-current="true"]').getAttribute('data-project-id');
  let project = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  assert(project.tracks[0].clips[0].source_library_item_id === '__SFX_ITEM_ID__', 'keyboard Add did not persist Library provenance');

  const beforeRejected = project.revision;
  await page.locator('.reusable-audio-asset', { hasText: 'Low strings' }).dragTo(page.locator('.track-row[data-track-type="sfx"] .timeline-stage'));
  await page.getByRole('status').filter({ hasText: /cannot be added/i }).waitFor();
  project = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  assert(project.revision === beforeRejected, 'incompatible audio drop mutated the durable project');

  await page.locator('.reusable-audio-asset', { hasText: 'Low strings' }).dragTo(page.locator('.track-row[data-track-type="music"] .timeline-stage'));
  await page.locator('.timeline-clip.clip-music').waitFor();
  await waitSaved();
  project = await page.evaluate(id => fetch('/v1/story-builder-projects/' + id).then(response => response.json()), projectID);
  assert(project.tracks[1].clips[0].source_library_item_id === '__MUSIC_ITEM_ID__', 'compatible Music drop did not persist provenance');

  await page.evaluate(id => fetch('/v1/library/' + id, { method: 'DELETE' }), '__SFX_ITEM_ID__');
  await page.goto(origin + '/demo/story-builder.html?project=' + projectID);
  await page.locator('.timeline-clip.clip-sfx').waitFor();
  assert(await page.locator('.clip-media-error').count() === 0, 'Library source deletion broke project-owned media');
  assert(await page.locator('.timeline-clip').count() === 2, 'placed media did not survive project reload');

  await page.goto(origin + '/demo/story-builder.html?project=__BROKEN_PROJECT_ID__');
  await page.locator('.clip-media-error').waitFor();
  assert((await page.locator('.clip-media-error').innerText()).includes('missing or unreadable'), 'missing project media was not shown on the affected clip');
  await page.locator('.timeline-clip').click();
  assert((await page.locator('#storyBuilderSelectionBody').innerText()).includes('project media is missing or unreadable'), 'Selection panel hid the project media error');

  return { project: projectID, brokenProject: '__BROKEN_PROJECT_ID__' };
}
'@

Push-Location $runtimeDir
try {
  $ready = $false
  for ($attempt = 0; $attempt -lt 60; $attempt++) {
    $server.Refresh()
    if ($server.HasExited) {
      throw "Story Builder browser-smoke Gateway exited before becoming ready"
    }
    try {
      if ((Invoke-WebRequest -UseBasicParsing "$baseURL/health" -TimeoutSec 1).StatusCode -eq 200) {
        $ready = $true
        break
      }
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if (-not $ready) {
    throw "Story Builder browser-smoke Gateway did not become ready"
  }

  $actorVoice = Invoke-RestMethod -Uri "$baseURL/v1/voices" -Method Post -Form @{
    name = "Mara"
    transcript = "A short reference transcript."
    file = Get-Item $voiceWavPath
  }
  $keeperVoice = Invoke-RestMethod -Uri "$baseURL/v1/voices/$($actorVoice.id)/characters" -Method Post -ContentType "application/json" -Body (@{
    name = "Weathered keeper"
    direction = "Older British woman, weathered, low and guarded"
  } | ConvertTo-Json)
  $cartographerVoice = Invoke-RestMethod -Uri "$baseURL/v1/voices/$($actorVoice.id)/characters" -Method Post -ContentType "application/json" -Body (@{
    name = "Young cartographer"
    direction = "Young, precise and curious"
  } | ConvertTo-Json)

  $matchedActorVoice = Invoke-RestMethod -Uri "$baseURL/v1/voices" -Method Post -Form @{
    name = "Lena actor"
    transcript = "A second short reference transcript."
    file = Get-Item $voiceWavPath
  }
  $matchedCharacterVoice = Invoke-RestMethod -Uri "$baseURL/v1/voices/$($matchedActorVoice.id)/characters" -Method Post -ContentType "application/json" -Body (@{
    name = "Lena exact match"
    direction = "Steady and close"
  } | ConvertTo-Json)

  $storyImportStart = Invoke-RestMethod -Uri "$baseURL/v1/stories" -Method Post -ContentType "application/json" -Body (@{
    subject = "Story Builder import browser smoke"
    mode = "sketch"
    premise = "Two speakers test a retained Story import."
    style = "Plain"
    target_seconds = 30
    voice_mode = "fixed"
    cast = @(
      @{ id = "mara"; name = "Mara"; voice_id = $matchedActorVoice.id },
      @{ id = "jon"; name = "Jon"; voice_id = $actorVoice.id }
    )
    title = "Retained Story import browser smoke"
    script = @(
      @{ id = "line-001"; speaker_id = "mara"; text = "This take remains compatible."; fact_ids = @() },
      @{ id = "line-002"; speaker_id = "jon"; text = "This take will become stale."; fact_ids = @() }
    )
  } | ConvertTo-Json -Depth 8)
  $storyImportStatus = $null
  for ($attempt = 0; $attempt -lt 80; $attempt++) {
    $storyImportStatus = Invoke-RestMethod -Uri "$baseURL/v1/stories/$($storyImportStart.id)" -Method Get
    if ($storyImportStatus.status -eq "complete") { break }
    if ($storyImportStatus.status -eq "failed" -or $storyImportStatus.status -eq "cancelled") {
      throw "retained Story import fixture ended as $($storyImportStatus.status)"
    }
    Start-Sleep -Milliseconds 100
  }
  if ($storyImportStatus.status -ne "complete") {
    throw "retained Story import fixture did not complete"
  }
  $storyImportSecondLine = $storyImportStatus.manifest.script[1]
  $storyImportPatched = Invoke-RestMethod -Uri "$baseURL/v1/stories/$($storyImportStart.id)/lines/$($storyImportSecondLine.id)" -Method Patch -ContentType "application/json" -Body (@{
    text = "This line changed after its retained take."
  } | ConvertTo-Json)

  $libraryAudioB64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($libraryWavPath))
  $sfxItem = Invoke-RestMethod -Uri "$baseURL/v1/library" -Method Post -ContentType "application/json" -Body (@{
    kind = "audio"; name = "Door slam"; data_b64 = $libraryAudioB64; meta = @{ media_role = "sfx" }
  } | ConvertTo-Json -Depth 4)
  $musicItem = Invoke-RestMethod -Uri "$baseURL/v1/library" -Method Post -ContentType "application/json" -Body (@{
    kind = "audio"; name = "Low strings"; data_b64 = $libraryAudioB64; meta = @{ media_role = "music" }
  } | ConvertTo-Json -Depth 4)
  $utilityItem = Invoke-RestMethod -Uri "$baseURL/v1/library" -Method Post -ContentType "application/json" -Body (@{
    kind = "audio"; name = "Scratch take"; data_b64 = $libraryAudioB64; meta = @{ media_role = "utility" }
  } | ConvertTo-Json -Depth 4)
  if ($sfxItem.mediaRole -ne "sfx" -or $sfxItem.durationMs -ne 1000 -or $musicItem.mediaRole -ne "music" -or $utilityItem.mediaRole -ne "utility") {
    throw "Library audio role or duration metadata was not canonical"
  }

  $brokenProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects" -Method Post -ContentType "application/json" -Body '{"name":"Missing project media browser smoke"}'
  $brokenProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($brokenProject.id)" -Method Put -ContentType "application/json" -Body (@{
    name = $brokenProject.name
    revision = $brokenProject.revision
    timeline_duration_ms = 30000
    tracks = @([ordered]@{ id = "broken_foley"; name = "Broken foley"; type = "sfx"; order = 0; muted = $false; clips = @() })
  } | ConvertTo-Json -Depth 8)
  $brokenProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($brokenProject.id)/library-audio" -Method Post -ContentType "application/json" -Body (@{
    revision = $brokenProject.revision; track_id = "broken_foley"; library_item_id = $sfxItem.id; start_ms = 0
  } | ConvertTo-Json)
  $brokenSourceID = $brokenProject.tracks[0].clips[0].source_id
  $brokenMediaPath = Join-Path $runtimeDir "out\story-builder-projects\$($brokenProject.id)\media\$brokenSourceID.wav"
  Remove-Item -LiteralPath $brokenMediaPath

  $statusProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects" -Method Post -ContentType "application/json" -Body '{"name":"Dialogue status browser smoke"}'
  $statusTracks = @(
    [ordered]@{ id = "ready_track"; name = "Ready dialogue"; type = "dialogue"; order = 0; muted = $false; character_voice_id = $keeperVoice.id; clips = @(
      [ordered]@{ id = "ready_clip"; type = "dialogue"; label = "Ready line"; text = "Ready line."; status = "stale"; start_ms = 0; duration_ms = 1000 }
    ) },
    [ordered]@{ id = "building_track"; name = "Building dialogue"; type = "dialogue"; order = 1; muted = $false; character_voice_id = $keeperVoice.id; clips = @(
      [ordered]@{ id = "building_clip"; type = "dialogue"; label = "Building line"; text = "Building line."; status = "stale"; start_ms = 0; duration_ms = 1000 }
    ) },
    [ordered]@{ id = "failed_track"; name = "Failed dialogue"; type = "dialogue"; order = 2; muted = $false; character_voice_id = $keeperVoice.id; clips = @(
      [ordered]@{ id = "failed_clip"; type = "dialogue"; label = "Failed line"; text = "Failed line."; status = "stale"; start_ms = 0; duration_ms = 1000 }
    ) }
  )
  $statusSaved = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($statusProject.id)" -Method Put -ContentType "application/json" -Body (@{
    name = $statusProject.name
    revision = $statusProject.revision
    timeline_duration_ms = 30000
    tracks = $statusTracks
  } | ConvertTo-Json -Depth 10)
  $statusManifestPath = Join-Path $runtimeDir "out\story-builder-projects\$($statusProject.id)\project.json"
  $statusManifest = Get-Content -Raw $statusManifestPath | ConvertFrom-Json
  $statusManifest.tracks[0].clips[0].status = "ready"
  $statusManifest.tracks[1].clips[0].status = "building"
  $statusManifest.tracks[2].clips[0].status = "failed"
  $statusManifest.tracks[2].clips[0] | Add-Member -NotePropertyName build_error -NotePropertyValue "fixture failure"
  $statusManifest | ConvertTo-Json -Depth 12 | Set-Content -Encoding UTF8 -Path $statusManifestPath

  $audioTrimProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects" -Method Post -ContentType "application/json" -Body '{"name":"Audio trim browser smoke"}'
  $audioTrimProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($audioTrimProject.id)" -Method Put -ContentType "application/json" -Body (@{
    name = $audioTrimProject.name
    revision = $audioTrimProject.revision
    timeline_duration_ms = 30000
    tracks = @([ordered]@{ id = "mara"; name = "Mara"; type = "dialogue"; order = 0; muted = $false; character_voice_id = $keeperVoice.id; clips = @(
      [ordered]@{ id = "line_1"; type = "dialogue"; label = "Keep the lamp low"; text = "Keep the lamp low"; start_ms = 125; duration_ms = 700 }
    ) })
  } | ConvertTo-Json -Depth 10)
  $audioTrimManifestPath = Join-Path $runtimeDir "out\story-builder-projects\$($audioTrimProject.id)\project.json"
  $audioTrimManifest = Get-Content -Raw $audioTrimManifestPath | ConvertFrom-Json
  $audioTrimClip = $audioTrimManifest.tracks[0].clips[0]
  $audioTrimClip.status = "ready"
  $audioTrimClip | Add-Member -NotePropertyName source_id -NotePropertyValue "take_1"
  $audioTrimClip | Add-Member -NotePropertyName source_duration_ms -NotePropertyValue 1200
  $audioTrimClip | Add-Member -NotePropertyName source_in_ms -NotePropertyValue 200
  $audioTrimClip | Add-Member -NotePropertyName source_out_ms -NotePropertyValue 900
  $audioTrimManifest | ConvertTo-Json -Depth 12 | Set-Content -Encoding UTF8 -Path $audioTrimManifestPath
  $audioTrimTakesDir = Join-Path $runtimeDir "out\story-builder-projects\$($audioTrimProject.id)\takes"
  New-Item -ItemType Directory -Force -Path $audioTrimTakesDir | Out-Null
  Write-FixtureWav -Path (Join-Path $audioTrimTakesDir "take_1.wav") -Samples 19200

  $playbackProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects" -Method Post -ContentType "application/json" -Body '{"name":"Timeline playback browser smoke"}'
  $playbackProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($playbackProject.id)" -Method Put -ContentType "application/json" -Body (@{
    name = $playbackProject.name
    revision = $playbackProject.revision
    timeline_duration_ms = 3000
    tracks = @(
      [ordered]@{ id = "playback_dialogue"; name = "Dialogue"; type = "dialogue"; order = 0; muted = $false; character_voice_id = $keeperVoice.id; clips = @(
        [ordered]@{ id = "playback_line"; type = "dialogue"; label = "Ready line"; text = "Ready line."; status = "stale"; start_ms = 0; duration_ms = 1000 },
        [ordered]@{ id = "playback_silence"; type = "silence"; label = "Pause"; start_ms = 1500; duration_ms = 500 }
      ) },
      [ordered]@{ id = "playback_sfx"; name = "SFX"; type = "sfx"; order = 1; muted = $false; clips = @() },
      [ordered]@{ id = "playback_music"; name = "Music"; type = "music"; order = 2; muted = $false; clips = @() },
      [ordered]@{ id = "playback_muted"; name = "Muted SFX"; type = "sfx"; order = 3; muted = $true; clips = @() }
    )
  } | ConvertTo-Json -Depth 10)
  $playbackProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($playbackProject.id)/library-audio" -Method Post -ContentType "application/json" -Body (@{
    revision = $playbackProject.revision; track_id = "playback_sfx"; library_item_id = $sfxItem.id; start_ms = 250
  } | ConvertTo-Json)
  $playbackProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($playbackProject.id)/library-audio" -Method Post -ContentType "application/json" -Body (@{
    revision = $playbackProject.revision; track_id = "playback_music"; library_item_id = $musicItem.id; start_ms = 250
  } | ConvertTo-Json)
  $playbackProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($playbackProject.id)/library-audio" -Method Post -ContentType "application/json" -Body (@{
    revision = $playbackProject.revision; track_id = "playback_muted"; library_item_id = $sfxItem.id; start_ms = 250
  } | ConvertTo-Json)
  $playbackSFXClip = $playbackProject.tracks | Where-Object { $_.id -eq "playback_sfx" } | Select-Object -ExpandProperty clips | Select-Object -First 1
  $playbackSFXClip | Add-Member -NotePropertyName source_in_ms -NotePropertyValue 500
  $playbackSFXClip.source_out_ms = 1000
  $playbackSFXClip.duration_ms = 500
  $playbackProject = Invoke-RestMethod -Uri "$baseURL/v1/story-builder-projects/$($playbackProject.id)" -Method Put -ContentType "application/json" -Body (@{
    name = $playbackProject.name
    revision = $playbackProject.revision
    timeline_duration_ms = $playbackProject.timeline_duration_ms
    tracks = $playbackProject.tracks
  } | ConvertTo-Json -Depth 12)
  $playbackManifestPath = Join-Path $runtimeDir "out\story-builder-projects\$($playbackProject.id)\project.json"
  $playbackManifest = Get-Content -Raw $playbackManifestPath | ConvertFrom-Json
  $playbackDialogueClip = $playbackManifest.tracks[0].clips[0]
  $playbackDialogueClip.status = "ready"
  $playbackDialogueClip | Add-Member -NotePropertyName source_id -NotePropertyValue "playback_take"
  $playbackDialogueClip | Add-Member -NotePropertyName source_duration_ms -NotePropertyValue 1000
  $playbackDialogueClip | Add-Member -NotePropertyName source_in_ms -NotePropertyValue 0
  $playbackDialogueClip | Add-Member -NotePropertyName source_out_ms -NotePropertyValue 1000
  $playbackManifest | ConvertTo-Json -Depth 12 | Set-Content -Encoding UTF8 -Path $playbackManifestPath
  $playbackTakesDir = Join-Path $runtimeDir "out\story-builder-projects\$($playbackProject.id)\takes"
  New-Item -ItemType Directory -Force -Path $playbackTakesDir | Out-Null
  Write-FixtureWav -Path (Join-Path $playbackTakesDir "playback_take.wav")

  $audioCode = $audioCode.Replace("__AUDIO_TRIM_PROJECT_ID__", $audioTrimProject.id)
  $statusCode = $statusCode.Replace("__STATUS_PROJECT_ID__", $statusProject.id)
  $buildFailureRecoveryCode = $buildFailureRecoveryCode.Replace("__RECOVERY_CHARACTER_VOICE_ID__", $keeperVoice.id)
  $buildCancellationRecoveryCode = $buildCancellationRecoveryCode.Replace("__RECOVERY_CHARACTER_VOICE_ID__", $keeperVoice.id)
  $libraryAudioCode = $libraryAudioCode.Replace("__SFX_ITEM_ID__", $sfxItem.id).Replace("__MUSIC_ITEM_ID__", $musicItem.id).Replace("__BROKEN_PROJECT_ID__", $brokenProject.id)
  $playbackSetupCode = $playbackSetupCode.Replace("__PLAYBACK_PROJECT_ID__", $playbackProject.id)
  $playbackTimelineCode = $playbackTimelineCode.Replace("__PLAYBACK_PROJECT_ID__", $playbackProject.id)
  $playbackUnavailableCode = $playbackUnavailableCode.Replace("__PLAYBACK_PROJECT_ID__", $playbackProject.id).Replace("__BROKEN_PROJECT_ID__", $brokenProject.id)
  $renderMasterCode = $renderMasterCode.Replace("__PLAYBACK_PROJECT_ID__", $playbackProject.id).Replace("__BROKEN_PROJECT_ID__", $brokenProject.id)
  $exportMasterCode = $exportMasterCode.Replace("__PLAYBACK_PROJECT_ID__", $playbackProject.id)
  $storyImportCode = $storyImportCode.Replace("__IMPORT_STORY_ID__", $storyImportStart.id).Replace("__IMPORT_SUGGESTED_CHARACTER_ID__", $matchedCharacterVoice.id).Replace("__IMPORT_AMBIGUOUS_ACTOR_ID__", $actorVoice.id)

  Invoke-BrowserCLI -Arguments @("open", "$baseURL/demo/story-builder.html")
  Invoke-BrowserCode -Code $arrangementCode
  Invoke-BrowserCode -Code $panelCode
  Invoke-BrowserCode -Code $audioCode
  Invoke-BrowserCode -Code $statusCode
  Invoke-BrowserCode -Code $dialogueCode
  Invoke-BrowserCode -Code $revoiceCode
  Invoke-BrowserCode -Code $buildDialogueCode
  Invoke-BrowserCode -Code $libraryAudioCode
  Invoke-BrowserCode -Code $buildFailureRecoveryCode
  Invoke-BrowserCode -Code $buildCancellationRecoveryCode
  Invoke-BrowserCode -Code $playbackSetupCode
  Invoke-BrowserCode -Code $playbackTimelineCode
  Invoke-BrowserCode -Code $renderMasterCode
  Invoke-BrowserCode -Code $exportMasterCode
  Invoke-BrowserCode -Code $playbackUnavailableCode
  Invoke-BrowserCode -Code $storyImportCode
  [ordered]@{ status = "ok"; browser = "playwright"; gateway = $baseURL } | ConvertTo-Json
} finally {
  try {
    & $npx.Source --yes --package "@playwright/cli" playwright-cli "-s=$session" close | Out-Null
  } catch {
    # Best-effort browser cleanup; preserve the original failure.
  }
  if (-not $server.HasExited) {
    Stop-Process -Id $server.Id
  }
  Pop-Location
}
