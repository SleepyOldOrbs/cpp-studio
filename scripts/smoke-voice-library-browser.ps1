param(
  [switch]$Native,
  [int]$GatewayPort=8884,
  [string]$OutDir=(Join-Path $env:TEMP ('voice-library-'+[guid]::NewGuid().ToString('N')))
)
$ErrorActionPreference='Stop'
$npx=Get-Command npx -ErrorAction Stop
if(Get-NetTCPConnection -LocalPort $GatewayPort -State Listen -ErrorAction SilentlyContinue){throw 'Gateway port is in use'}
New-Item -ItemType Directory $OutDir -Force | Out-Null
$runtimeDir=(Resolve-Path $OutDir).Path
$exe=Join-Path $runtimeDir 'cpp-studio.exe';$fixture=Join-Path $runtimeDir 'fixture.exe'
go build -o $exe ./cmd/cpp-studio
if($LASTEXITCODE){throw 'Gateway build failed'}
if($Native){
  $primaryRoot='H:\James_NAS\AI_WEBSITES\llama.cpp-audio.cpp\cpp-studio'
  $configured=Get-Content (Join-Path $primaryRoot 'config.real.json') -Raw | ConvertFrom-Json
  $engines=@{}
  foreach($id in @('voicedesign')){
    $entry=$configured.engines.$id
    if(-not $entry){throw "Native engine $id is not configured"}
    $entry.command=$entry.command.Replace('${root}',$primaryRoot)
    $entry.args=@($entry.args | ForEach-Object { $_.Replace('${root}',$primaryRoot) })
    $engines[$id]=$entry
  }
  if($engines.voicedesign.mode -ne 'subprocess'){throw 'Native check requires the configured subprocess voice designer'}
  foreach($id in @('audio','omnivoice')){$engines[$id]=@{command=(Join-Path $primaryRoot 'engines/audio-0.9.2-drama3-base-small/audiocpp_cli.exe');args=@('--task','tts','--family','omnivoice','--model',(Join-Path $primaryRoot 'models/speech/voice-design/OmniVoice'),'--backend','cuda');mode='subprocess';requestTimeoutSeconds=300;gpu=$true}}
}else{
  go build -o $fixture ./cmd/cpp-studio-fixture
  if($LASTEXITCODE){throw 'Fixture build failed'}
  $engines=@{}
  foreach($id in @('voicedesign','audio','omnivoice','dramabox')){$engines[$id]=@{command=$fixture;args=@('speech');mode='subprocess';requestTimeoutSeconds=60}}
  $engines['voicedesign'].args=@('design')
}
$configPath=Join-Path $runtimeDir 'config.json'
@{gateway=@{host='127.0.0.1';port=$GatewayPort};engines=$engines}|ConvertTo-Json -Depth 12|Set-Content -Encoding utf8 $configPath
$baseURL="http://127.0.0.1:$GatewayPort";$session="voice-library-$PID"
$server=Start-Process -WindowStyle Hidden -PassThru -FilePath $exe -ArgumentList @('--config',$configPath) -WorkingDirectory $runtimeDir -RedirectStandardError (Join-Path $runtimeDir 'gateway.log')
function Invoke-BrowserCode([string]$Code,[string]$Name){
  $file=Join-Path $runtimeDir "$Name.js"
  $Code.Replace('__BASE__',$baseURL).Replace('__NATIVE__',$Native.IsPresent.ToString().ToLower()).Replace('__SCREEN__',(Join-Path $runtimeDir "$Name.png").Replace('\','/'))|Set-Content -Encoding utf8 $file
  $result=& $npx.Source --yes --package '@playwright/cli' playwright-cli "-s=$session" run-code "--filename=$file" 2>&1
  $result|Out-Host
  if($LASTEXITCODE -or ($result -join "`n") -match '### Error|TimeoutError'){throw "Browser check $Name failed"}
}
$start=@'
async page => {
 const assert=(ok,message)=>{if(!ok)throw Error(message);};
 await page.setViewportSize({width:1400,height:1100});
 await page.goto('__BASE__/demo/#voice-design');
 await page.locator('#designModelSelect option[value="qwen3"]').waitFor({state:'attached'});
 await page.locator('#designModelSelect').selectOption('qwen3');
 let requests=0;page.on('request',request=>{if(request.url().endsWith('/v1/voices/design'))requests++;});
 await page.evaluate(()=>window.__designResponses=[]);
 page.on('response',async response=>{if(response.url().endsWith('/v1/voices/design')&&response.ok()){const result=await response.json();await page.evaluate(data=>window.__designResponses.push(data),result);}});
 await page.locator('#designStarterSelect').selectOption('warm-story');
 await page.locator('#designStarterUse').click();
 assert((await page.locator('#designDescriptionInput').inputValue()).includes('warm, conversational'),'starter did not fill editable description');
 assert(requests===0,'starter generated automatically');
 await page.locator('#designGenerateButton').click();
 return {started:true};
}
'@
$finish=@'
async page => {
 const assert=(ok,message)=>{if(!ok)throw Error(message);};
 const native=__NATIVE__;
 const request=async(path,options)=>{const r=await page.request.fetch('__BASE__'+path,options);assert(r.ok(),path+': '+await r.text());return r.status()===204?null:r.json();};
 const hash=async path=>page.evaluate(async url=>Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',await fetch(url).then(r=>r.arrayBuffer())))).map(x=>x.toString(16).padStart(2,'0')).join(''),path);
 await page.locator('input[name="designCandidate"][value="1"]').check();
 await page.locator('#designNameInput').fill('Starter library voice');
 const saving=page.waitForResponse(r=>r.url().endsWith('/v1/voices')&&r.request().method()==='POST');
 await page.locator('#designSaveButton').click();const savedResponse=await saving;assert(savedResponse.ok(),'candidate save failed');const actor=await savedResponse.json();
 const expectedHash=await page.evaluate(async()=>{const data=window.__designResponses[1].reference_b64;const bytes=Uint8Array.from(atob(data),c=>c.charCodeAt(0));return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))).map(x=>x.toString(16).padStart(2,'0')).join('');});
 assert(await hash('/v1/voices/'+actor.id+'/audio')===expectedHash,'saved voice differs from chosen candidate');
 const initialReference=await page.request.get('__BASE__/v1/voices/'+actor.id+'/audio');
 if(!native){const frames=192000,buffer=Buffer.alloc(44+frames*2);buffer.write('RIFF',0);buffer.writeUInt32LE(36+frames*2,4);buffer.write('WAVEfmt ',8);buffer.writeUInt32LE(16,16);buffer.writeUInt16LE(1,20);buffer.writeUInt16LE(1,22);buffer.writeUInt32LE(16000,24);buffer.writeUInt32LE(32000,28);buffer.writeUInt16LE(2,32);buffer.writeUInt16LE(16,34);buffer.write('data',36);buffer.writeUInt32LE(frames*2,40);for(let i=0;i<frames;i++)buffer.writeInt16LE(i%2?1000:-1000,44+i*2);const assigned=await page.request.put('__BASE__/v1/voices/'+actor.id+'/reference',{multipart:{file:{name:'long.wav',mimeType:'audio/wav',buffer},transcript:'Reference words.'}});assert(assigned.ok(),'long fixture reference failed');}
 const oldCharacter=await request('/v1/voices/'+actor.id+'/characters',{method:'POST',data:{name:'Quiet character',direction:'whisper'}});
 const newCharacter=await request('/v1/voices/'+actor.id+'/characters',{method:'POST',data:{name:'Low character',direction:'low pitch'}});
 const second=await page.request.post('__BASE__/v1/voices',{multipart:{name:'Second library voice',transcript:'Reference words.',file:{name:'ref.wav',mimeType:'audio/wav',buffer:await initialReference.body()}}});assert(second.ok(),'second voice fixture failed');
 await page.locator('a[data-parent-link="stories-audiobooks"]').click();await page.locator('a.subnav-link[data-page-link="audiobook"]').click();
 await page.locator('#audiobookFileInput').setInputFiles({name:'Draft.md',mimeType:'text/markdown',buffer:Buffer.from('# Opening\nThe old clock chimed.')});
 await page.locator('#audiobookTitleInput').fill('Reusable book');
 await page.locator('a.utility-link[data-page-link="library"]').click();
 await page.locator('#libraryFilterInput').fill('library voice');
 await page.locator('.library-item[data-library-kind="actor_voice"] audio').first().waitFor({state:'attached'});
 const selected=await page.evaluate(()=>localStorage.getItem('cpp-studio-voice'));
 await page.locator('.library-item[data-library-kind="actor_voice"] audio').evaluateAll(async audios=>{await audios[0].play();await audios[1].play();if(!audios[0].paused||audios[1].paused)throw Error('two auditions playing');});
 await page.locator('#libraryVoiceFilter').selectOption('character_voice');
 assert(await page.locator('.library-item[data-library-kind="actor_voice"] audio').evaluateAll(audios=>audios.every(a=>a.paused)),'filter left an audition playing');
 assert(await page.evaluate(()=>localStorage.getItem('cpp-studio-voice'))===selected,'filter changed selected voice');
 const child=page.locator('.library-child[data-library-id="'+newCharacter.id+'"]');await child.waitFor({state:'visible'});assert((await child.innerText()).includes('Inherited from Starter library voice'),'inherited relationship missing');
 await page.locator('#libraryVoiceFilter').selectOption('actor_voice');assert(await child.isHidden(),'Actor filter still showed Character Voices');
 const actorCard=page.locator('.library-item[data-library-id="'+actor.id+'"]');
 await actorCard.locator(':scope > audio').evaluate(audio=>audio.play());
 await actorCard.locator(':scope > .library-item-actions').getByRole('button',{name:'Use for audiobook',exact:true}).click();
 await page.waitForFunction(id=>document.querySelector('#audiobookVoiceSelect').value===id,actor.id);
 await page.locator('section[data-page="audiobook"]').waitFor({state:'visible'});
 assert(await page.locator('#audiobookFileInput').evaluate(input=>input.files[0].name==='Draft.md'),'reuse discarded uploaded document');assert(await page.locator('#audiobookEngineSelect').inputValue()==='audio','reuse changed engine');
 assert(await actorCard.locator(':scope > audio').evaluate(audio=>audio.paused),'leaving Library left its hidden audition playing');
 await page.locator('#audiobookPreviewButton').click();await page.locator('#audiobookChapterPlan li').waitFor();
 await page.locator('#audiobookNarrateButton').click();await page.waitForFunction(()=>document.querySelector('#audiobookStatus').textContent.includes('Complete'),undefined,{timeout:180000});
 const books=await request('/v1/audiobooks');const book=books.audiobooks.find(b=>b.title==='Reusable book');assert(book?.voiceId===actor.id,'saved book used wrong voice');
 await page.locator('#audiobookTitleInput').fill('Draft preserved');await page.locator('#audiobookFileInput').setInputFiles({name:'Unsubmitted.md',mimeType:'text/markdown',buffer:Buffer.from('Keep this draft.')});
 await page.locator('a.utility-link[data-page-link="library"]').click();await page.locator('#libraryVoiceFilter').selectOption('');await page.locator('#libraryFilterInput').fill('Reusable book');
 await page.locator('.library-item[data-library-id="'+book.id+'"]').getByRole('link',{name:'Open Audiobook',exact:true}).click();
 assert(await page.locator('#audiobookTitleInput').inputValue()==='Draft preserved'&&await page.locator('#audiobookFileInput').evaluate(input=>input.files[0].name==='Unsubmitted.md'),'owner launch discarded a draft');
 await page.locator('#audiobookShelf [data-book-id="'+book.id+'"] .audiobook-book-details').waitFor({state:'visible'});
 if(!native){
  await page.locator('#audiobookEngineSelect').selectOption('dramabox');const settings=page.locator('details.workflow-step').filter({has:page.locator('#audiobookSpeakerPhrase')});if(!await settings.evaluate(details=>details.open))await settings.locator(':scope > summary').click();await page.locator('#audiobookSpeakerPhrase').selectOption('A woman');await page.locator('a.utility-link[data-page-link="library"]').click();await page.locator('#libraryFilterInput').fill('Starter library voice');await page.locator('#libraryVoiceFilter').selectOption('character_voice');await child.getByRole('button',{name:'Use for audiobook',exact:true}).click();await page.waitForFunction(id=>document.querySelector('#audiobookVoiceSelect').value===id,newCharacter.id);assert(await page.locator('#audiobookDirectionInput').inputValue()==='low pitch','Character direction was not applied');assert(await page.locator('#audiobookSpeakerPhrase').inputValue()==='A woman','Character reuse reset the speaker setting');await page.locator('#audiobookPreviewButton').click();await page.locator('#audiobookChapterPlan li').waitFor();
  const handoff=await page.context().newPage();await handoff.goto('__BASE__/demo/?voice='+encodeURIComponent(newCharacter.id)+'#audiobook');
  await handoff.locator('#audiobookFileInput').setInputFiles({name:'Pending.md',mimeType:'text/markdown',buffer:Buffer.from('Keep the pending manuscript.')});
  const applyRequested=handoff.getByRole('button',{name:'Apply requested voice',exact:true});await applyRequested.click();await handoff.locator('#audiobookReuseStatus').filter({hasText:/expressive/i}).waitFor();assert(await applyRequested.isVisible(),'failed handoff removed the retry button');
  await handoff.locator('#audiobookEngineSelect').selectOption('dramabox');await handoff.locator('details.workflow-step').filter({has:handoff.locator('#audiobookSpeakerPhrase')}).locator(':scope > summary').click();await handoff.locator('#audiobookSpeakerPhrase').selectOption('A woman');await applyRequested.click();await handoff.waitForFunction(id=>document.querySelector('#audiobookVoiceSelect').value===id,newCharacter.id);
  assert(await handoff.locator('#audiobookFileInput').evaluate(input=>input.files[0].name==='Pending.md'),'retry discarded the pending manuscript');assert(await handoff.locator('#audiobookSpeakerPhrase').inputValue()==='A woman','retry reset the speaker setting');await handoff.close();
 }
 let project=await request('/v1/story-builder-projects',{method:'POST',data:{name:'Reuse selection fixture'}});
 project=await request('/v1/story-builder-projects/'+project.id,{method:'PUT',data:{name:project.name,revision:project.revision,tracks:[{id:'dialogue_track',name:'Original reader',type:'dialogue',order:0,character_voice_id:oldCharacter.id,clips:[{id:'line_one',label:'First',type:'dialogue',text:'Original line.',start_ms:0,duration_ms:4000},{id:'line_two',label:'Second',type:'dialogue',text:'Unchanged line.',start_ms:5000,duration_ms:4000}]}]}});
 if(!native){const build=await request('/v1/story-builder-projects/'+project.id+'/builds',{method:'POST',data:{revision:project.revision}});for(let i=0;i<100;i++){const status=await request(build.status_url);if(status.status==='complete')break;assert(status.status!=='failed',status.error);await page.waitForTimeout(100);}project=await request('/v1/story-builder-projects/'+project.id);}
 const retained=project.tracks[0].clips[1].source_id;
 await page.locator('a.utility-link[data-page-link="library"]').click();await page.locator('#libraryVoiceFilter').selectOption('character_voice');await page.locator('#libraryFilterInput').fill('Starter library voice');
 const popupPromise=page.waitForEvent('popup');await child.getByRole('link',{name:'Assign in Story Builder',exact:true}).click();const story=await popupPromise;await story.waitForLoadState();
 await story.locator('#storyBuilderProjectList button').filter({hasText:'Reuse selection fixture'}).click();await story.locator('#storyBuilderPendingStatus').filter({hasText:'reference checked'}).waitFor();
 await story.locator('#storyBuilderScriptView').click();const row=story.locator('.script-row[data-clip-id="line_one"]');await row.locator('textarea').first().fill('Draft wording preserved.');
 await story.locator('#storyBuilderPendingTarget').selectOption('clip:line_one');await story.locator('#storyBuilderPendingApply').click();await story.locator('#storyBuilderPendingStatus').filter({hasText:'Assigned'}).waitFor();
 await story.waitForFunction(()=>document.querySelector('#storyBuilderSaveStatus').textContent==='Saved');
 const changed=await request('/v1/story-builder-projects/'+project.id);const target=changed.tracks.find(t=>t.clips.some(c=>c.id==='line_one'));assert(target.character_voice_id===newCharacter.id&&target.clips.find(c=>c.id==='line_one').text==='Draft wording preserved.','selected line did not retain source and new voice');assert(changed.tracks.flatMap(t=>t.clips).find(c=>c.id==='line_two').source_id===retained,'unrelated take changed');
 await row.getByRole('button',{name:'Generate line Draft wording preserved.',exact:true}).click();await story.waitForFunction(()=>document.querySelector('.script-row[data-clip-id="line_one"] .script-line-status').textContent==='ready', undefined,{timeout:180000});
 await story.waitForFunction(()=>!document.querySelector('#storyBuilderPendingTarget').disabled,undefined,{timeout:10000});
 await story.setViewportSize({width:800,height:1100});await story.screenshot({path:'__SCREEN__',fullPage:true});const storyWidth=await story.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,wide:Array.from(document.querySelectorAll('body *')).filter(e=>e.getBoundingClientRect().right>innerWidth+2).slice(0,12).map(e=>({tag:e.tagName,id:e.id,class:e.className,width:e.getBoundingClientRect().width,right:e.getBoundingClientRect().right}))}));assert(storyWidth.scroll<=storyWidth.width,'Story reuse overflows narrow window '+JSON.stringify(storyWidth));await story.close();
 await page.setViewportSize({width:800,height:1100});await page.locator('#libraryVoiceFilter').focus();assert(await page.locator('#libraryVoiceFilter').evaluate(e=>e===document.activeElement),'voice filter lost keyboard focus');assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Library overflows narrow window');
 return {status:'ok',native,actor:actor.id,book:book.id,project:project.id,chosenCandidateSHA256:expectedHash};
}
'@
Push-Location $runtimeDir
try{
  for($i=0;$i -lt 60;$i++){try{Invoke-WebRequest "$baseURL/health"|Out-Null;break}catch{Start-Sleep -Milliseconds 200}}
  & $npx.Source --yes --package '@playwright/cli' playwright-cli "-s=$session" open "$baseURL/demo/#voice-design"
  if($LASTEXITCODE){throw 'Browser did not open'}
  Invoke-BrowserCode $start 'start'
  foreach($index in 0..2){Invoke-BrowserCode ("async page => { await page.waitForFunction(i => { const text=document.querySelectorAll('[data-design-candidate-status]')[i].textContent;return text.includes('Ready')||text.includes('Failed'); }, $index, {timeout:180000}); const text=await page.locator('[data-design-candidate-status]').nth($index).innerText();if(!text.includes('Ready'))throw Error(text);return {candidate:$index,status:text}; }") "candidate-$index"}
  Invoke-BrowserCode $finish 'reuse'
  [ordered]@{status='ok';native=$Native.IsPresent;output=$runtimeDir}|ConvertTo-Json
}finally{
  & $npx.Source --yes --package '@playwright/cli' playwright-cli "-s=$session" close | Out-Null
  if($server -and -not $server.HasExited){Stop-Process -Id $server.Id}
  Pop-Location
}
