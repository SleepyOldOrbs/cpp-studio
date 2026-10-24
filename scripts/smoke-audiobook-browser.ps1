param(
  [int]$GatewayPort = 8881,
  [string]$OutDir = (Join-Path $env:TEMP ("audiobook-browser-" + [guid]::NewGuid().ToString("N")))
)

$ErrorActionPreference = "Stop"
$npx = Get-Command npx -ErrorAction Stop
if (Get-NetTCPConnection -LocalPort $GatewayPort -State Listen -ErrorAction SilentlyContinue) { throw "Port $GatewayPort is in use" }
New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
$runtimeDir = (Resolve-Path $OutDir).Path
$gatewayExe = Join-Path $runtimeDir 'cpp-studio.exe'
$fixtureExe = Join-Path $runtimeDir 'fixture.exe'
go build -o $gatewayExe ./cmd/cpp-studio
if ($LASTEXITCODE) { throw 'Gateway build failed' }
go build -o $fixtureExe ./cmd/cpp-studio-fixture
if ($LASTEXITCODE) { throw 'Fixture build failed' }
$configPath = Join-Path $runtimeDir 'config.json'
@{ gateway = @{ host = '127.0.0.1'; port = $GatewayPort }; engines = @{
  audio = @{ command = $fixtureExe; args = @('speech'); mode = 'subprocess'; requestTimeoutSeconds = 45 }
  dramabox = @{ command = $fixtureExe; args = @('speech'); mode = 'subprocess'; requestTimeoutSeconds = 45 }
  omnivoice = @{ command = $fixtureExe; args = @('speech'); mode = 'subprocess'; requestTimeoutSeconds = 45 }
}} | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 $configPath
$baseURL = "http://127.0.0.1:$GatewayPort"
$session = "audiobook-smoke-$PID"
$server = Start-Process -WindowStyle Hidden -PassThru -FilePath $gatewayExe -ArgumentList @('--config', $configPath) -WorkingDirectory $runtimeDir -RedirectStandardError (Join-Path $runtimeDir 'gateway.log')
$code = @'
async page => {
  const assert = (ok, why) => { if (!ok) throw new Error(why); };
  await page.setViewportSize({width: 1400, height: 1200});
  await page.request.put('__BASE__/v1/pronunciation',{data:{version:1,rules:[{term:'James',replacement:'Jaymz',language:'en'}]}});
  let releaseLoad;const loading=new Promise(resolve=>{releaseLoad=resolve;});
  await page.route('**/v1/pronunciation',async route=>{if(route.request().method()==='GET')await loading;await route.continue();});
  await page.goto('__BASE__/demo/#pronunciation');
  assert(await page.locator('#pronunciationAdd').isDisabled()&&await page.locator('#pronunciationSave').isDisabled(),'dictionary allowed replacement before existing rules loaded');
  releaseLoad();
  await page.locator('#pronunciationStatus').filter({hasText:'saved rules'}).waitFor();
  await page.unroute('**/v1/pronunciation');
  assert(await page.locator('[data-rule-field="term"]').inputValue()==='James','existing baseline was lost');
  await page.locator('#pronunciationAdd').click();
  await page.locator('[data-rule-field="term"]').last().fill('York');
  await page.locator('[data-rule-field="replacement"]').last().fill('Yawk');
  await page.locator('[data-rule-field="language"]').last().fill('en');
  await page.locator('#pronunciationLanguage').fill('en');
  await page.locator('#pronunciationPreview').click();
  await page.locator('#pronunciationPreviewResult mark').filter({hasText:'Jaymz'}).waitFor();
  assert((await page.locator('#pronunciationPreviewResult').innerText()).includes('James'), 'original text missing from preview');
  await page.locator('#pronunciationSave').click();
  await page.locator('#pronunciationStatus').filter({hasText:'Rules saved'}).waitFor();
  await page.goto('__BASE__/demo/#audiobook');
  await page.locator('#audiobookEngineSelect option[value="audio"]:not([disabled])').waitFor({state:'attached'});
  const source = '# Opening\nThe old clock chimed.\n# Ending\nThe town fell quiet.'.replace(/\\n/g, '\n');
  const upload = async text => page.locator('#audiobookFileInput').setInputFiles({name:'Two chapters.md', mimeType:'text/markdown', buffer:Buffer.from(text)});
  await upload(source);
  await page.locator('#audiobookPreviewButton').click();
  await page.waitForFunction(() => document.querySelectorAll('#audiobookChapterPlan li').length === 2);
  assert((await page.locator('#audiobookChapterPlan').innerText()).includes('The town fell quiet.'), 'missing actual excerpt');
  await page.locator('[data-audiobook-audition="chapter_002"]').click();
  await page.locator('#audiobookAuditionAudio').waitFor({state:'visible'});
  await page.waitForFunction(() => document.querySelector('#audiobookAuditionAudio').readyState >= 1);
  const audio = await page.locator('#audiobookAuditionAudio').evaluate(a => ({duration:a.duration, paused:a.paused}));
  assert(audio.duration > 0 && audio.paused, 'audition must be playable and await user play');
  let shelf = await page.request.get('__BASE__/v1/audiobooks').then(r => r.json());
  assert(shelf.audiobooks.length === 0, 'audition created a phantom book');
  await page.locator('#audiobookTitleInput').fill('Chapter browser fixture');
  await page.locator('#audiobookNarrateButton').click();
  await page.waitForFunction(() => document.querySelector('#audiobookStatus').textContent.includes('Complete'));
  shelf = await page.request.get('__BASE__/v1/audiobooks').then(r => r.json());
  const book = shelf.audiobooks[0];
  assert(book.documentPlan.chapters.length === 2 && book.chapterBoundaries.length === 2, 'saved chapter identity/timing missing');
  const master = await page.request.get('__BASE__' + book.artifactUrl);
  assert(master.ok(), 'master unavailable');
  let bookCard = page.locator('#audiobookShelf .story-library-item').filter({has:page.getByText('Chapter browser fixture',{exact:true})});
  await bookCard.locator('.audiobook-book-details > summary').click();
  await bookCard.locator('[data-book-detail="author"]').fill('Fixture Writer');
  await bookCard.locator('[data-book-detail="narrator"]').fill('Fixture Narrator');
  const coverPNG = await page.evaluate(() => { const c=document.createElement('canvas'); c.width=16;c.height=24;const ctx=c.getContext('2d');ctx.fillStyle='#f0a141';ctx.fillRect(0,0,16,24);return c.toDataURL('image/png').split(',')[1]; });
  await bookCard.locator('[data-book-cover]').setInputFiles({name:'cover.png',mimeType:'image/png',buffer:Buffer.from(coverPNG,'base64')});
  await page.locator('#audiobookTitleInput').fill('Concurrent fixture');
  await page.locator('#audiobookNarrateButton').click();
  await bookCard.locator('[data-book-detail="author"]').focus();
  await page.waitForFunction(() => document.querySelector('#audiobookStatus').textContent.includes('Complete'));
  await page.locator('#audiobookShelf').getByText('Concurrent fixture',{exact:true}).waitFor();
  assert(await bookCard.locator('[data-book-detail="author"]').inputValue()==='Fixture Writer','background completion lost the detail draft');
  assert(await bookCard.locator('[data-book-cover]').evaluate(input=>input.files.length===1&&input.files[0].name==='cover.png'),'background completion lost the selected cover');
  assert(await bookCard.locator('[data-book-detail="author"]').evaluate(input=>input===document.activeElement),'background completion lost editor focus');
  const coverSaved = page.waitForResponse(r => r.url().endsWith('/cover') && r.request().method() === 'PUT');
  await bookCard.getByRole('button',{name:'Save book details',exact:true}).click();
  assert((await coverSaved).status() === 204, 'cover was not saved');
  await page.waitForFunction(async id => { const b=await fetch('/v1/audiobooks').then(r=>r.json()); const saved=b.audiobooks.find(book=>book.id===id);return saved?.metadata?.author==='Fixture Writer' && Boolean(saved.cover); },book.id);
  await page.reload();
  await page.locator('section[data-page="audiobook"] details.result-panel > summary').click();
  await page.locator('#audiobookShelf').getByText('Chapter browser fixture', {exact:true}).waitFor();
  const card = page.locator('#audiobookShelf .story-library-item').filter({has:page.getByText('Chapter browser fixture',{exact:true})});
  await card.locator('.audiobook-book-details > summary').click();
  assert(await card.locator('[data-book-detail="author"]').inputValue() === 'Fixture Writer', 'book details did not reopen');
  await card.locator('img.audiobook-cover').waitFor();
  await page.waitForFunction(() => document.querySelector('img.audiobook-cover').naturalWidth === 16);
  await card.getByText('2 chapters', {exact:true}).click();
  assert((await card.innerText()).includes('The town fell quiet.'), 'saved plan not reopened');
  await upload('# Wait\n[fixture-wait] This passage can be cancelled.');
  await page.locator('#audiobookPreviewButton').click();
  await page.waitForFunction(() => document.querySelectorAll('#audiobookChapterPlan li').length === 1);
  await page.locator('[data-audiobook-audition]').click();
  await page.locator('#audiobookAuditionCancel').waitFor({state:'visible'});
  await page.waitForFunction(async () => { const p = await fetch('/v1/jobs').then(r=>r.json()); return p.jobs.some(j=>j.kind==='audiobook-audition' && j.status==='running'); });
  await page.locator('#audiobookAuditionCancel').click();
  await page.waitForFunction(() => document.querySelector('#audiobookAuditionStatus').textContent.includes('cancelled'));
  await page.locator('#audiobookEngineSelect').selectOption('dramabox');
  await page.getByText('Choose the narrator and settings', {exact:true}).click();
  await page.locator('#audiobookVerificationSelect').selectOption('off');
  assert(await page.locator('#audiobookChapterPlan').isHidden(), 'engine change left an approved plan');
  await upload(source);
  await page.locator('#audiobookPreviewButton').click();
  await page.waitForFunction(() => document.querySelectorAll('#audiobookChapterPlan li').length === 2);
  await page.locator('#audiobookTitleInput').fill('Drama chapter fixture');
  await page.locator('#audiobookNarrateButton').click();
  await page.waitForFunction(() => document.querySelector('#audiobookStatus').textContent.includes('Complete'));
  shelf = await page.request.get('__BASE__/v1/audiobooks').then(r => r.json());
  const drama = shelf.audiobooks.find(b => b.title === 'Drama chapter fixture');
  assert(drama && drama.sections.length === 2 && drama.chapterBoundaries.length === 2, 'Drama chapter plan missing');
  await page.locator('#audiobookEngineSelect').selectOption('audio');
  await page.locator('#audiobookApplyPronunciation').evaluate(button => button.closest('details').open=true);
  await page.locator('#audiobookPronunciationLanguage').fill('en');
  await page.locator('#audiobookApplyPronunciation').click();
  await page.locator('#audiobookPronunciationStatus').filter({hasText:'2 saved rules applied'}).waitFor();
  await upload('# Name\nJames opens the door.');
  await page.locator('#audiobookPreviewButton').click();
  await page.locator('#audiobookChapterPlan mark').filter({hasText:'Jaymz'}).waitFor();
  await page.locator('[data-audiobook-audition]').click();
  await page.locator('#audiobookAuditionAudio').waitFor({state:'visible'});
  await page.locator('#audiobookTitleInput').fill('Corrected name');
  await page.locator('#audiobookNarrateButton').click();
  await page.waitForFunction(() => document.querySelector('#audiobookStatus').textContent.includes('Complete'));
  shelf = await page.request.get('__BASE__/v1/audiobooks').then(r=>r.json());
  const corrected=shelf.audiobooks.find(b=>b.title==='Corrected name');
  assert(corrected?.pronunciation?.rules[0].replacement==='Jaymz' && corrected.language==='en','book snapshot was not saved');

  const actorResponse=await page.request.post('__BASE__/v1/voices',{multipart:{name:'Pronunciation actor',transcript:'A short reference.',file:{name:'ref.wav',mimeType:'audio/wav',buffer:await master.body()}}});
  assert(actorResponse.ok(),'voice fixture failed: '+await actorResponse.text());const actor=await actorResponse.json();
  const character=await page.request.post('__BASE__/v1/voices/'+actor.id+'/characters',{data:{name:'Name reader',direction:'Calm and clear'}}).then(r=>r.json());
  let project=await page.request.post('__BASE__/v1/story-builder-projects',{data:{name:'Pronunciation project'}}).then(r=>r.json());
  const save=await page.request.put('__BASE__/v1/story-builder-projects/'+project.id,{data:{name:project.name,revision:project.revision,tracks:[{id:'names_track',name:'Reader',type:'dialogue',order:0,character_voice_id:character.id,clips:[{id:'name_line',label:'Name line',type:'dialogue',text:'James opens the door.',start_ms:0,duration_ms:8000}]}]}});
  assert(save.ok(),'project fixture failed: '+await save.text());project=await save.json();
  await page.goto('__BASE__/demo/story-builder.html?project='+project.id);
  await page.locator('#storyBuilderNameInput').waitFor({state:'visible'});
  await page.locator('.project-pronunciation > summary').click();
  await page.locator('#storyBuilderPronunciationLanguage').fill('en');
  await page.locator('#storyBuilderPronunciationPreview').click();
  await page.locator('#storyBuilderPronunciationResult mark').filter({hasText:'Jaymz'}).waitFor();
  await page.locator('#storyBuilderPronunciationApply').click();
  await page.waitForFunction(()=>document.querySelector('#storyBuilderSaveStatus').textContent==='Saved');
  let saved=await page.request.get('__BASE__/v1/story-builder-projects/'+project.id).then(r=>r.json());
  assert(saved.pronunciation?.rules[0].replacement==='Jaymz'&&saved.tracks[0].clips[0].text==='James opens the door.','project lost source or snapshot');
  await page.request.put('__BASE__/v1/pronunciation',{data:{version:1,rules:[{term:'James',replacement:'Different',language:'en'}]}});
  saved=await page.request.get('__BASE__/v1/story-builder-projects/'+project.id).then(r=>r.json());
  assert(saved.pronunciation.rules[0].replacement==='Jaymz','global dictionary edit changed project');
  await page.locator('#storyBuilderBuildButton').click();
  await page.waitForFunction(()=>document.querySelector('#storyBuilderBuildStatus').dataset.state==='ready');
  saved=await page.request.get('__BASE__/v1/story-builder-projects/'+project.id).then(r=>r.json());
  assert(saved.tracks[0].clips[0].status==='ready','corrected dialogue did not build');
  await page.setViewportSize({width:800, height:1100});
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'pronunciation project overflows at 800px');
  await page.screenshot({path:'__SCREEN__', fullPage:true});
  console.log(JSON.stringify({status:'ok', book:book.id, drama:drama.id, auditionSeconds:audio.duration}));
}
'@
$codePath = Join-Path $runtimeDir 'browser-check.js'
$code.Replace('__BASE__', $baseURL).Replace('__SCREEN__', (Join-Path $runtimeDir 'audiobook.png').Replace('\','/')) | Set-Content -Encoding utf8 $codePath
Push-Location $runtimeDir
try {
  $ready = $false
  for ($i=0; $i -lt 60; $i++) {
    try { if ((Invoke-WebRequest "$baseURL/health").StatusCode -eq 200) { $ready = $true; break } } catch {}
    Start-Sleep -Milliseconds 250
  }
  if (-not $ready) { throw 'Gateway did not become ready' }
  & $npx.Source --yes --package '@playwright/cli' playwright-cli "-s=$session" open "$baseURL/demo/#audiobook"
  if ($LASTEXITCODE) { throw 'Browser did not open' }
  & $npx.Source --yes --package '@playwright/cli' playwright-cli "-s=$session" run-code "--filename=$codePath"
  if ($LASTEXITCODE) { throw 'Audiobook browser acceptance failed' }
  [ordered]@{status='ok'; output=$runtimeDir} | ConvertTo-Json
} finally {
  & $npx.Source --yes --package '@playwright/cli' playwright-cli "-s=$session" snapshot
  & $npx.Source --yes --package '@playwright/cli' playwright-cli "-s=$session" close | Out-Null
  if (-not $server.HasExited) { Stop-Process -Id $server.Id }
  Pop-Location
}
