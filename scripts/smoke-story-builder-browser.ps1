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
    $writer.Write([byte[]]::new($pcmBytes))
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

  $audioCode = $audioCode.Replace("__AUDIO_TRIM_PROJECT_ID__", $audioTrimProject.id)
  $statusCode = $statusCode.Replace("__STATUS_PROJECT_ID__", $statusProject.id)
  $libraryAudioCode = $libraryAudioCode.Replace("__SFX_ITEM_ID__", $sfxItem.id).Replace("__MUSIC_ITEM_ID__", $musicItem.id).Replace("__BROKEN_PROJECT_ID__", $brokenProject.id)

  Invoke-BrowserCLI -Arguments @("open", "$baseURL/demo/story-builder.html")
  Invoke-BrowserCode -Code $arrangementCode
  Invoke-BrowserCode -Code $panelCode
  Invoke-BrowserCode -Code $audioCode
  Invoke-BrowserCode -Code $statusCode
  Invoke-BrowserCode -Code $dialogueCode
  Invoke-BrowserCode -Code $revoiceCode
  Invoke-BrowserCode -Code $buildDialogueCode
  Invoke-BrowserCode -Code $libraryAudioCode
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
