const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const assert = require('node:assert/strict');

test('pronunciation highlighting uses UTF-8 byte offsets without splitting Unicode', () => {
  const context = load(['pronunciationSegments'], { TextEncoder, TextDecoder });
  const parts = context.pronunciationSegments('é Jaymz!', [{spokenStart:3,spokenEnd:8}], 'spoken');
  assert.deepEqual(JSON.parse(JSON.stringify(parts)), [{text:'é ',matched:false},{text:'Jaymz',matched:true},{text:'!',matched:false}]);
});

test('analysis preparation decodes at 16 kHz and writes mono PCM16 without modifying the source', async () => {
  const original = new File(['original bytes'], 'stereo-48k.wav');
  const context = load(['prepareAnalysisAudio', 'encodeWav', 'writeAscii'], {
    Blob, File, window: { OfflineAudioContext: class {
      constructor(channels, frames, rate) { assert.deepEqual([channels, frames, rate], [1, 1, 16000]); }
      async decodeAudioData(bytes) {
        assert.equal(Buffer.from(bytes).toString(), 'original bytes');
        return { length: 2, sampleRate: 16000, numberOfChannels: 2, getChannelData: c => c ? [0, -1] : [1, 0] };
      }
    } },
  });
  const prepared = await context.prepareAnalysisAudio(original);
  const wav = new DataView(await prepared.arrayBuffer());
  assert.equal(wav.getUint16(22, true), 1);
  assert.equal(wav.getUint32(24, true), 16000);
  assert.equal(wav.getUint16(34, true), 16);
  assert.equal(wav.getInt16(44, true), 16383);
  assert.equal(wav.getInt16(46, true), -16384);
  assert.equal(await original.text(), 'original bytes');
});

test('alignment transcription fills editable text and preserves it on failure or a changed file', async () => {
  for (const outcome of ['success', 'failure', 'empty', 'changed']) {
    const original = {}, prepared = { name: 'analysis-16k.wav' }, fields = [], states = [];
    const context = load(['transcribeAnalysisFile'], {
      running: false, analysisTranscribeButton: {}, analysisFileInput: { files: [original] },
      analysisTranscriptInput: { value: 'Previous text' }, analysisTranscribeStatus: {},
      setRunning(v) { states.push(v); }, setBusy() {}, clearBusy() {}, syncAudioServiceControls() {},
      prepareAnalysisAudio: async file => { assert.equal(file, original); return prepared; },
      FormData: class { append(...args) { fields.push(args); } }, ensureOk: async () => {},
      fetch: async url => {
        assert.equal(url, '/v1/audio/transcriptions');
        if (outcome === 'failure') throw new Error('Unavailable');
        if (outcome === 'changed') context.analysisFileInput.files = [{}];
        return { json: async () => ({ text: outcome === 'empty' ? '' : '  Words from this file  ' }) };
      },
    });
    await context.transcribeAnalysisFile();
    assert.equal(fields[0][1], prepared);
    assert.equal(context.analysisTranscriptInput.value, outcome === 'success' ? 'Words from this file' : 'Previous text');
    assert.deepEqual(states, [true, false]);
    if (outcome === 'failure' || outcome === 'empty') assert.match(context.analysisTranscribeStatus.textContent, /failed/);
  }
});

test('every analysis route receives the normalized audio and alignment receives the corrected transcript', async () => {
  for (const capability of ['vad', 'diarization', 'forced_alignment']) {
    const prepared = { name: 'analysis-16k.wav' }, fields = [], calls = [];
    const context = load(['runAudioAnalysis'], {
      running: false, analysisModelSelect: { selectedIndex: 0, value: 'model1', options: [{ dataset: { capabilities: capability }, textContent: 'Model' }] },
      analysisFileInput: { files: [{}] }, analysisOutput: {}, analysisSubmitButton: {},
      analysisTranscriptInput: { value: 'Corrected words' }, analysisLanguageInput: { value: 'English' },
      prepareAnalysisAudio: async () => prepared, FormData: class { append(...args) { fields.push(args); } },
      setRunning() {}, setBusy() {}, clearBusy() {}, syncAudioServiceControls() {}, log() {}, catalogModelLabel() {}, ensureOk: async () => {},
      fetch: async url => { calls.push(url); return { json: async () => [] }; },
    });
    await context.runAudioAnalysis({ preventDefault() {} });
    assert.equal(fields.find(f => f[0] === 'file')[1], prepared);
    assert.equal(calls[0], '/v1/audio/' + (capability === 'forced_alignment' ? 'alignment' : capability));
    if (capability === 'forced_alignment') assert.deepEqual(fields.find(f => f[0] === 'transcript'), ['transcript', 'Corrected words']);
  }
});

test('converted voice saving transcribes the result and routes each destination without losing failed results', async () => {
  for (const mode of ['new-actor', 'new-character', 'actor', 'character']) {
    for (const failure of ['', 'transcribe', 'empty', 'save']) {
      const calls = [], forms = [], status = [], states = [], result = { size: 100 };
      const context = load(['saveConversionVoice'], {
        running: false, conversionLibraryButton: {}, conversionOutputBlob: result, conversionOutputTranscript: '',
        conversionSaveDestinationSelect: { value: mode }, conversionSaveNameInput: { value: 'New voice' },
        conversionSaveActorSelect: { value: 'actor1', selectedOptions: [{ textContent: 'Actor' }] },
        conversionSaveCharacterSelect: { value: 'character1', selectedOptions: [{ textContent: 'Character' }] },
        conversionSaveError: {}, conversionSaveStatus: {},
        showConversionSaveStatus(name) { status.push(name); }, revealWorkflowResult() {}, log() {},
        setRunning(v) { states.push(v); }, setBusy() {}, clearBusy() {}, refreshVoices: async () => {},
        FormData: class { constructor() { this.fields = []; forms.push(this.fields); } append(...args) { this.fields.push(args); } },
        fetch: async (url, options) => {
          calls.push([url, options.method]);
          if (failure === (calls.length === 1 ? 'transcribe' : 'save')) throw new Error('Unavailable');
          return { json: async () => ({ text: failure === 'empty' ? '' : 'Words from the converted result' }) };
        }, ensureOk: async () => {},
      });
      await context.saveConversionVoice();
      assert.deepEqual(calls[0], ['/v1/audio/transcriptions', 'POST']);
      assert.equal(forms[0][0][1], result);
      assert.equal(context.conversionOutputBlob, result);
      assert.deepEqual(states, [true, false]);
      if (failure === 'transcribe' || failure === 'empty') {
        assert.equal(calls.length, 1, 'never save without a transcript');
      } else {
        assert.deepEqual(calls[1], {
          'new-actor': ['/v1/voices', 'POST'], 'new-character': ['/v1/voices/actor1/characters', 'POST'],
          actor: ['/v1/voices/actor1/reference', 'PUT'], character: ['/v1/character-voices/character1/reference', 'PUT'],
        }[mode]);
        assert.equal(forms[1].find(f => f[0] === 'file')[1], result, 'save the converted WAV');
        assert.deepEqual(forms[1].find(f => f[0] === 'transcript'), ['transcript', 'Words from the converted result']);
        assert.equal(forms[1].some(f => f[0] === 'name'), mode.startsWith('new-'));
      }
      assert.equal(context.conversionSaveError.hidden, !failure);
      if (!failure) assert.equal(status.at(-1), mode.startsWith('new-') ? 'New voice' : mode === 'actor' ? 'Actor' : 'Character');
    }
  }
});

test('converted voice destination controls require a result and valid identity', () => {
  const fields = {}, actor = { id: 'a', character_voices: [{ id: 'c' }] };
  const context = load(['syncConversionAssignment'], {
    running: false, conversionOutputBlob: {}, libraryVoices: [actor], conversionLibraryButton: {},
    conversionSaveDestinationSelect: { value: 'new-actor' }, conversionSaveNameInput: { value: '' },
    conversionSaveActorSelect: { value: '' }, conversionSaveCharacterSelect: { value: '' },
    document: { getElementById(id) { return fields[id] ||= {}; } },
  });
  context.syncConversionAssignment(); assert.equal(context.conversionLibraryButton.disabled, true);
  context.conversionSaveNameInput.value = 'New'; context.syncConversionAssignment();
  assert.equal(context.conversionLibraryButton.disabled, false);
  context.conversionSaveDestinationSelect.value = 'new-character'; context.syncConversionAssignment();
  assert.equal(context.conversionLibraryButton.disabled, true);
  context.conversionSaveActorSelect.value = 'a'; context.syncConversionAssignment();
  assert.equal(context.conversionLibraryButton.disabled, false);
  context.conversionSaveDestinationSelect.value = 'character'; context.syncConversionAssignment();
  assert.equal(context.conversionLibraryButton.disabled, true);
  context.conversionSaveCharacterSelect.value = 'c'; context.syncConversionAssignment();
  assert.equal(context.conversionLibraryButton.disabled, false);
  context.conversionSaveDestinationSelect.value = 'actor'; actor.protected = true; context.syncConversionAssignment();
  assert.equal(context.conversionLibraryButton.disabled, true);
  actor.protected = false; context.conversionOutputBlob = null; context.syncConversionAssignment();
  assert.equal(context.conversionLibraryButton.disabled, true);
});

test('conversion picker scopes characters to their actor and preserves the performance', () => {
  function select() {
    return { options: [], current: '', replaceChildren() { this.options = []; this.current = ''; },
      appendChild(option) { this.options.push(option); },
      get value() { return this.current; },
      set value(value) { this.current = this.options.some(o => o.value === value) ? value : ''; } };
  }
  const actor = select(), voice = select(), field = {};
  const voices = [{ id: 'actor1', name: 'First actor', audio_url: '/actor1.wav', character_voices: [{ id: 'child1', name: 'First character' }] },
    { id: 'actor2', name: 'Second actor', audio_url: '/actor2.wav', character_voices: [] }];
  const upload = { name: 'uploaded.wav' };
  const context = load(['renderConversionTargetVoices', 'selectConversionTargetVoice'], {
    libraryVoices: voices, conversionTargetActorSelect: actor, conversionTargetVoiceSelect: voice,
    conversionSourceFile: upload,
    conversionTargetAudio: { src: '', load() {}, removeAttribute() { this.src = ''; } }, conversionTargetStatus: {},
    document: { getElementById() { return field; } }, createElement: (_, __, textContent) => ({ textContent }),
    clearConversionOutput() {}, clearConversionError() {}, syncConversionControls() {},
  });
  context.renderConversionTargetVoices(voices);
  assert.equal(context.conversionSourceFile, upload);
  assert.equal(context.conversionTargetAudio.src, '');
  assert.equal(field.hidden, true);
  actor.value = 'actor1'; context.renderConversionTargetVoices(voices);
  assert.deepEqual(voice.options.map(o => o.value), ['actor1', 'child1']);
  assert.equal(context.conversionTargetAudio.src, '/actor1.wav');
  assert.equal(context.conversionSourceFile, upload);
  voice.value = 'child1'; context.selectConversionTargetVoice();
  assert.equal(context.conversionTargetAudio.src, '/v1/character-voices/child1/reference');
  context.renderConversionTargetVoices(voices);
  assert.equal(voice.value, 'child1');
  assert.match(context.conversionTargetStatus.textContent, /First actor.*First character/);
  actor.value = 'actor2'; context.renderConversionTargetVoices(voices);
  assert.deepEqual(voice.options.map(o => o.value), ['actor2']);
  assert.equal(voice.value, 'actor2');
  actor.value = ''; context.renderConversionTargetVoices(voices);
  assert.equal(voice.value, '');
  assert.equal(field.hidden, true);
  assert.equal(context.conversionTargetAudio.src, '');
});

test('conversion requires a saved target and sends the selected character with the performance', async () => {
  const fields = [], calls = [], errors = [], source = { name: 'performance.wav' };
  const context = load(['runVoiceConversion'], {
    conversionModelReady: true, conversionSourceFile: source, conversionTargetVoiceSelect: { value: '' },
    conversionModelSelect: { value: 'vevo2', selectedIndex: 0, options: [{ textContent: 'VeVo2' }] },
    conversionRouteSelect: { value: 'style_preserved_vc' }, conversionTargetTextInput: { value: '' },
    conversionSubmitButton: {}, conversionOutputStatus: {}, conversionOutputAudio: { load() {} },
    clearConversionError() {}, setConversionError(e) { errors.push(e.message); },
    setRunning() {}, setBusy() {}, clearBusy() {}, revealWorkflowResult() {}, clearConversionOutput() {}, log() {}, formatBytes() { return '10 KB'; },
    FormData: class { append(...args) { fields.push(args); } }, URL: { createObjectURL() { return 'blob:result'; } },
    ensureOk: async () => {}, fetch: async url => { calls.push(url); return { blob: async () => ({ size: 10240 }) }; },
  });
  await context.runVoiceConversion({ preventDefault() {} });
  assert.equal(calls.length, 0);
  assert.equal(errors.length, 1);
  context.conversionTargetVoiceSelect.value = 'character1';
  await context.runVoiceConversion({ preventDefault() {} });
  assert.deepEqual(calls, ['/v1/audio/conversions']);
  assert.deepEqual(fields.find(f => f[0] === 'target_voice'), ['target_voice', 'character1']);
  assert.equal(fields.find(f => f[0] === 'source')[1], source);
  assert.equal(fields.some(f => f[0] === 'target'), false);
  assert.equal(context.conversionOutputAudio.src, 'blob:result');
});

test('clone destinations reuse the prepared transcript, isolate characters and retain the recording on failure', async () => {
  for (const mode of ['new-actor', 'new-character', 'actor', 'character']) {
    for (const fails of [false, true]) {
      const calls = [], fields = [], states = [], reference = { name: 'recording.wav' };
      const prepared = { file: reference, transcript: 'Already transcribed', source: null };
      const context = load(['assignCloneReference'], {
        revealWorkflowResult() {}, cloneAssignButton: { disabled: false }, cloneWavFile: reference, clonePrepared: prepared,
        cloneDestinationSelect: { value: mode }, cloneNameInput: { value: 'New name' },
        cloneActorSelect: { value: 'actor_one', selectedOptions: [{ textContent: 'Actor' }] },
        cloneCharacterSelect: { value: 'character_one', selectedOptions: [{ textContent: 'Character' }] },
        cloneTryVoiceSelect: {}, updateSpeakVoiceLabel() {}, clearSpeakAudio() {}, cloneAuditionDraft: null, cloneAuditionStatus: {},
        cloneAssignError: {}, cloneAssignStatus: {}, FormData: class { append(...args) { fields.push(args); } },
        appendCloneSource() {}, setRunning(value) { states.push(value); }, setBusy() {}, clearBusy() {}, log() {},
        async fetch(url, options) { calls.push([url, options.method]); return { async json() { return { id: 'new_id' }; } }; },
        async ensureOk() { if (fails) throw new Error('Save failed'); }, async refreshVoices() {},
      });
      await context.assignCloneReference();
      const expected = {
        'new-actor': ['/v1/voices', 'POST'], 'new-character': ['/v1/voices/actor_one/characters', 'POST'],
        actor: ['/v1/voices/actor_one/reference', 'PUT'], character: ['/v1/character-voices/character_one/reference', 'PUT'],
      }[mode];
      assert.deepEqual(calls, [expected]);
      assert.deepEqual(fields.find(x => x[0] === 'transcript'), ['transcript', prepared.transcript]);
      assert.equal(context.cloneWavFile, reference);
      assert.equal(context.clonePrepared, prepared);
      assert.deepEqual(states, [true, false]);
      if (fails) {
        assert.equal(context.cloneAssignError.hidden, false);
        assert.equal(context.cloneAssignStatus.textContent, '');
      } else {
        assert.match(context.cloneAssignStatus.textContent, mode === 'character' ? /Character only/ : /Try it in step 4/);
        assert.equal(context.cloneTryVoiceSelect.value, mode.startsWith('new-') ? 'new_id' : mode === 'character' ? 'character_one' : 'actor_one');
      }
    }
  }
});

test('Clone Voice prepares a transcript without saving an actor and ignores replaced recordings', async () => {
  for (const outcome of ['success', 'replaced', 'empty', 'failure']) {
    const calls = [], reference = { name: 'reference.wav' }, errors = [], field = {};
    const context = load(['saveClone'], {
      revealWorkflowResult() {}, running: false, cloneRecording: false, cloneSourceRevision: 1,
      cloneWavFile: reference, cloneWavSource: null, clonePrepared: null,
      cloneTranscript: {}, cloneAssignStatus: {}, cloneProcessStatus: {}, cloneSaveButton: {},
      document: { getElementById() { return field; } }, FormData: class { append() {} },
      clearCloneError() {}, setCloneError(e) { errors.push(e.message); }, setRunning() {}, setBusy() {}, clearBusy() {},
      async fetch(url) {
        calls.push(url);
        if (outcome === 'replaced') context.cloneSourceRevision++;
        if (outcome === 'failure') throw new Error('Unavailable');
        return { async json() { return { text: outcome === 'empty' ? '' : '  Spoken words  ' }; } };
      }, async ensureOk() {},
    });
    await context.saveClone();
    assert.deepEqual(calls, ['/v1/audio/transcriptions']);
    assert.equal(context.cloneWavFile, reference);
    if (outcome === 'success') {
      assert.equal(context.clonePrepared.file, reference);
      assert.equal(context.clonePrepared.transcript, 'Spoken words');
      assert.equal(field.hidden, false);
    } else {
      assert.equal(context.clonePrepared, null);
      assert.equal(errors.length, outcome === 'replaced' ? 0 : 1);
    }
  }
});

test('changing a reference invalidates the prepared clone and its old playback', () => {
  const revoked = [], field = {};
  const context = load(['resetPreparedClone'], {
    cloneSourceRevision: 4, clonePrepared: {}, cloneTranscript: { value: 'old words' },
    cloneAssignStatus: {}, cloneAssignError: {}, cloneReferenceURL: 'blob:old', clearCloneError() {},
    document: { getElementById() { return field; } },
    clonePreviewAudio: { pause() {}, removeAttribute() {}, load() {} },
    URL: { revokeObjectURL(url) { revoked.push(url); } },
  });
  context.resetPreparedClone();
  assert.equal(context.clonePrepared, null);
  assert.equal(context.cloneSourceRevision, 5);
  assert.equal(context.cloneTranscript.value, '');
  assert.equal(field.hidden, true);
  assert.deepEqual(revoked, ['blob:old']);
});

test('getting started TTS sends literal text to speech, labels output and retains replay when autoplay fails', async () => {
  const calls = [];
  const revoked = [];
  const resultLabel = {};
  const context = load(['playQuickSpeech'], {
    running: false, quickSpeechController: null, AbortController,
    quickSpeechText: { value: '  [whisper] Keep this. <|sfx:sigh|>Uh  ' },
    quickSpeechModel: { value: 'higgs-audio', selectedIndex: 0, options: [{ textContent: 'Higgs' }] },
    quickSpeechPlay: { disabled: false }, quickSpeechCancel: {}, quickSpeechStatus: {}, quickSpeechError: {},
    quickSpeechResult: { hidden: true }, quickSpeechDownload: {}, quickSpeechURL: 'blob:old',
    quickSpeechAudio: { pause() {}, load() {}, async play() { throw new Error('autoplay blocked'); } },
    document: { getElementById() { return resultLabel; } },
    URL: { revokeObjectURL(url) { revoked.push(url); }, createObjectURL() { return 'blob:new'; } },
    setRunning() {}, setBusy() {}, clearBusy() {},
    readSpeechPerformance() { return { emotion: 'relief' }; },
    async ensureOk() {},
    async fetch(url, options) {
      calls.push({ url, body: JSON.parse(options.body) });
      return { async blob() { return {}; } };
    },
  });
  await context.playQuickSpeech({ preventDefault() {} });
  assert.deepEqual(calls, [{ url: '/v1/audio/speech', body: {
    input: '[whisper] Keep this. <|sfx:sigh|>Uh', model: 'higgs-audio', performance: { emotion: 'relief' }, format: 'wav',
  } }]);
  assert.equal(context.quickSpeechAudio.src, 'blob:new');
  assert.equal(context.quickSpeechDownload.href, 'blob:new');
  assert.deepEqual(revoked, ['blob:old']);
  assert.equal(resultLabel.textContent, 'Generated with Higgs');
  assert.equal(context.quickSpeechResult.hidden, false);
  assert.match(context.quickSpeechStatus.textContent, /Press play on the audio player/);
  assert.equal(context.quickSpeechController, null);
  assert.equal(context.quickSpeechCancel.hidden, true);
});

test('getting started TTS reports errors and cancellation while preserving the previous result', async () => {
  for (const cancel of [false, true]) {
    const runningStates = [];
    const context = load(['playQuickSpeech'], {
      running: false, quickSpeechController: null, AbortController,
      quickSpeechText: { value: 'Try again' },
      quickSpeechModel: { value: 'audio', selectedIndex: 0, options: [{ textContent: 'Qwen' }] },
      quickSpeechPlay: { disabled: false }, quickSpeechCancel: {}, quickSpeechStatus: {}, quickSpeechError: {},
      quickSpeechResult: { hidden: false }, quickSpeechURL: 'blob:previous',
      quickSpeechAudio: { src: 'blob:previous', pause() {} },
      setRunning(value) { runningStates.push(value); }, setBusy() {}, clearBusy() {},
      readSpeechPerformance() {}, async ensureOk() {},
      async fetch() {
        if (cancel) context.quickSpeechController.abort();
        throw new Error('Model is unavailable');
      },
    });
    await context.playQuickSpeech();
    assert.equal(context.quickSpeechAudio.src, 'blob:previous');
    assert.equal(context.quickSpeechResult.hidden, false);
    assert.equal(context.quickSpeechController, null);
    assert.equal(context.quickSpeechCancel.hidden, true);
    assert.deepEqual(runningStates, [true, false]);
    if (cancel) assert.equal(context.quickSpeechStatus.textContent, 'Generation cancelled.');
    else assert.equal(context.quickSpeechError.textContent, 'Model is unavailable');
  }
});

const source = fs.readFileSync(path.join(__dirname, '../internal/demo/static/app.js'), 'utf8');
function load(names, values = {}) {
  const context = vm.createContext(values);
  for (const name of names) {
    const start = source.search(new RegExp(`^  (?:async )?function ${name}\\(`, 'm'));
    assert.ok(start >= 0, `missing ${name}`);
    const end = source.indexOf('\n  }', start);
    assert.ok(end > start, `missing end of ${name}`);
    vm.runInContext(source.slice(start, end + 4), context);
  }
  return context;
}

test('a delayed chapter preview cannot approve a changed document or narrator', async () => {
  for (const changed of [false, true]) {
    let currentKey = 'first', drawn;
    const plan = { digest: 'digest', chapters: [] };
    const context = load(['previewAudiobookDocument'], {
      audiobookRequestKey: () => currentKey, audiobookErrorBox: {}, audiobookPreviewButton: {}, audiobookRequestPreview: {},
      audiobookEngineSelect: { value: 'audio' }, audiobookVoiceSelect: { value: 'voice', selectedOptions: [{ textContent: 'Narrator' }] },
      audiobookAcceptPromptWarnings: { checked: false }, audiobookFileInput: { files: [{ name: 'book.md' }] },
      audiobookOptionsText: () => '', audiobookPromptWarningAcceptance: {}, audiobookChapterPlan: {},
      audiobookResolvedKey: '', audiobookDocumentPlan: null, audiobookPronunciation: null, audiobookPronunciationLanguage: '', FormData: class { append() {} },
      renderAudiobookChapterPlan(_, value) { drawn = value; }, syncAudiobookEngineControls() {},
      fetch: async () => ({ ok: true, json: async () => { if (changed) currentKey = 'second'; return { plan }; } }),
    });
    await context.previewAudiobookDocument();
    assert.equal(context.audiobookResolvedKey, changed ? '' : 'first');
    assert.equal(context.audiobookDocumentPlan, changed ? null : plan);
    assert.equal(drawn, changed ? undefined : plan);
  }
});

test('changing audiobook inputs clears the displayed chapter plan and its approval', () => {
  const context = load(['invalidateAudiobookPreview'], {
    audiobookResolvedKey: 'approved', audiobookResolvedDirection: 'old', audiobookDocumentPlan: { digest: 'old' },
    audiobookChapterPlan: { hidden: false, textContent: 'Old chapters' }, audiobookRequestPreview: {},
    showMatchingAudiobookBenchmark() {}, syncAudiobookEngineControls() {}, clearAudiobookAudition() {},
  });
  context.invalidateAudiobookPreview();
  assert.equal(context.audiobookDocumentPlan, null);
  assert.equal(context.audiobookResolvedKey, '');
  assert.equal(context.audiobookChapterPlan.hidden, true);
  assert.equal(context.audiobookChapterPlan.textContent, '');
});

test('passage audition uses the approved plan and discards changed or immediately cancelled requests', async () => {
  for (const scenario of ['complete', 'changed', 'cancelled']) {
    const changed = scenario === 'changed', cancelled = scenario === 'cancelled';
    let currentKey = 'approved', pauses = 0;
    const fields = [], requests = [];
    const context = load(['cancelAudiobookAudition', 'auditionAudiobookChapter'], {
      audiobookRequestKey: () => currentKey, audiobookResolvedKey: 'approved', audiobookDocumentPlan: { digest: 'digest' },
      audiobookFileInput: { files: [{ name: 'book.md' }] }, audiobookAuditionPending: false, audiobookAuditionJobId: '',
      audiobookAuditionStatus: {}, audiobookAuditionAudio: { hidden: true, load() {} }, audiobookErrorBox: {},
      clearAudiobookAudition() { pauses++; }, syncAudiobookEngineControls() {},
      audiobookDocumentForm: () => ({ append(...args) { fields.push(args); } }),
      fetch: async (url, options) => {
        requests.push([url, options && options.method]);
        if (url === '/v1/audiobooks/audition') {
          if (changed) currentKey = 'changed';
          if (cancelled) context.audiobookAuditionCancelled = true;
          return { ok: true, json: async () => ({ id: 'audition-1' }) };
        }
        return { ok: true, json: async () => ({ status: 'complete', result: { audioUrl: '/temporary.wav' } }) };
      },
    });
    await context.auditionAudiobookChapter('chapter_002');
    assert.deepEqual(fields, [['chapterId', 'chapter_002']]);
    assert.equal(context.audiobookAuditionAudio.hidden, changed || cancelled);
    assert.equal(context.audiobookAuditionAudio.src, changed || cancelled ? undefined : '/temporary.wav');
    assert.equal(context.audiobookAuditionPending, false);
    if (changed || cancelled) assert.ok(requests.some(r => r[0] === '/v1/jobs/audition-1/cancel'));
    assert.equal(pauses, 1);
  }
});

test('a failed audition poll requests cancellation and keeps a handle when cancellation is unavailable', async () => {
  for (const cancellationOK of [false, true]) {
    const requests = [];
    const context = load(['cancelAudiobookAudition', 'auditionAudiobookChapter'], {
      audiobookRequestKey: () => 'approved', audiobookResolvedKey: 'approved', audiobookDocumentPlan: { digest: 'digest' },
      audiobookFileInput: { files: [{ name: 'book.md' }] }, audiobookAuditionPending: false, audiobookAuditionJobId: '',
      audiobookAuditionStatus: {}, audiobookAuditionAudio: { hidden: true }, clearAudiobookAudition() {}, syncAudiobookEngineControls() {},
      audiobookDocumentForm: () => ({ append() {} }), readErrorBody: async () => 'Status unavailable',
      fetch: async (url, options) => {
        requests.push([url, options && options.method]);
        if (url === '/v1/audiobooks/audition') return { ok: true, json: async () => ({ id: 'audition-1' }) };
        if (url.endsWith('/cancel')) return { ok: cancellationOK };
        return { ok: false };
      },
    });
    await context.auditionAudiobookChapter('chapter_001');
    assert.ok(requests.some(r => r[0] === '/v1/jobs/audition-1/cancel' && r[1] === 'POST'));
    assert.equal(context.audiobookAuditionJobId, cancellationOK ? '' : 'audition-1');
    assert.equal(context.audiobookAuditionAudio.hidden, true);
  }
});

test('reopened chapter plans identify the saved narrator with an engine-appropriate fallback', () => {
  const context = load(['audiobookSavedNarrator'], { audiobookVoiceSelect: { options: [{value:'voice_1',textContent:'Ada'}] } });
  assert.equal(context.audiobookSavedNarrator({ voiceId:'voice_1' }), 'Ada');
  assert.equal(context.audiobookSavedNarrator({ voiceId:'missing_voice' }), 'missing_voice (saved voice)');
  assert.equal(context.audiobookSavedNarrator({ synthesisIdentity:{voice:{id:'voice_1'}} }), 'Ada');
  assert.equal(context.audiobookSavedNarrator({ engine:'dramabox', synthesisIdentity:{voice:{id:'default'}} }), 'DramaBox text-only voice');
  assert.equal(context.audiobookSavedNarrator({}), 'Studio default');
});

test('book details save through the audiobook owner and clearly report a rejected cover', async () => {
  for (const coverOK of [false,true]) {
    const requests = [], status = {};
    const context = load(['saveAudiobookDetails'], {
      FormData: class { append() {} }, audiobookStatus: {}, refreshAudiobooks: async () => {}, log() {},
      readErrorBody: async () => 'Malformed cover',
      fetch: async (url, options) => { requests.push([url, options]); return {ok: !url.endsWith('/cover') || coverOK}; },
    });
    await context.saveAudiobookDetails('book-1', {title:'Café', author:'Writer'}, {name:'cover.png'}, status);
    assert.deepEqual(requests.map(r=>[r[0],r[1].method]), [['/v1/audiobooks/book-1/metadata','PUT'],['/v1/audiobooks/book-1/cover','PUT']]);
    assert.equal(JSON.parse(requests[0][1].body).title, 'Café');
    assert.match(status.textContent, coverOK ? /saved/i : /Details saved.*cover.*Malformed cover/i);
  }
});

test('M4B export submits the reviewed render and master identity and reports cancellation', async () => {
  for (const cancelled of [false,true]) {
    let request;
    const status={};
    const plan={renderId:'render-0002',masterSha256:'reviewed-hash'};
    const signal={};
    const context=load(['exportAudiobookM4B'],{
      fetch:async(url,options)=>{request=[url,options];if(cancelled){const e=new Error('cancelled');e.name='AbortError';throw e;}return{ok:true,json:async()=>({url:'/delivery.m4b',renderId:'render-0002'})};},
      readErrorBody:async()=>'',
    });
    const record=await context.exportAudiobookM4B('book_1',plan,status,signal);
    assert.equal(request[0],'/v1/audiobooks/book_1/exports/m4b');
    assert.deepEqual(JSON.parse(request[1].body),plan);
    assert.equal(request[1].signal,signal);
    if(cancelled){assert.equal(record,null);assert.match(status.textContent,/cancelled/i);}else{assert.equal(record.renderId,'render-0002');assert.match(status.textContent,/ready/i);}
  }
});

test('shelf refresh cannot remove an active M4B export and its cancel control', async () => {
  for (const startedBeforeRefresh of [true,false]) {
    let requests=0;
    const shelf={textContent:'Export and cancel controls'};
    const context=load(['refreshAudiobooks'],{
      audiobookM4BBusy:startedBeforeRefresh,audiobookShelf:shelf,
      fetch:async()=>{requests++;return{ok:true,json:async()=>{context.audiobookM4BBusy=true;return{audiobooks:[],interrupted:[]};}};},
    });
    await context.refreshAudiobooks(true);
    assert.equal(shelf.textContent,'Export and cancel controls');
    assert.equal(requests,startedBeforeRefresh?0:1);
  }
});

test('audiobook shelf retains the exact dirty card and selected file while adding completed work', () => {
  const file={name:'cover.png'};
  const dirty={dataset:{bookId:'book_1',bookDetailsDirty:'true'},input:{value:'Draft author'},files:[file]};
  const stale={dataset:{bookId:'old'}};
  const children=[dirty,stale];
  stale.remove=()=>children.splice(children.indexOf(stale),1);
  const shelf={children,childNodes:children,insertBefore(node,anchor){const i=anchor?children.indexOf(anchor):children.length;children.splice(i,0,node);},appendChild(node){children.push(node);}};
  const context=load(['renderAudiobookShelf'],{audiobookShelf:shelf,renderAudiobookCard(book){assert.notEqual(book.id,'book_1');return{dataset:{bookId:book.id}};}});
  context.renderAudiobookShelf({audiobooks:[{id:'book_2'},{id:'book_1'}],interrupted:[]});
  assert.deepEqual(children.map(node=>node.dataset.bookId),['book_2','book_1']);
  assert.equal(children[1],dirty);
  assert.equal(dirty.input.value,'Draft author');
  assert.equal(dirty.files[0],file);
});

test('re-export replaces the same-render download label and filename with the returned snapshot', () => {
  const nodes=[];
  const container={querySelectorAll(){return nodes;},appendChild(node){nodes.push(node);}};
  const context=load(['renderAudiobookM4BDownload'],{createElement(tag,cls,text){const node={dataset:{},textContent:text,remove(){nodes.splice(nodes.indexOf(node),1);}};return node;}});
  context.renderAudiobookM4BDownload(container,{renderId:'render-0001',url:'/same',metadata:{title:'Old title'}},'Book');
  context.renderAudiobookM4BDownload(container,{renderId:'render-0002',url:'/other',metadata:{title:'Other render'}},'Book');
  context.renderAudiobookM4BDownload(container,{renderId:'render-0001',url:'/same',metadata:{title:'New title'}},'Book');
  assert.equal(nodes.length,2);
  const link=nodes.find(node=>node.dataset.m4bRender==='render-0001');
  assert.match(link.textContent,/New title/);
  assert.equal(link.download,'New title.m4b');
  assert.ok(!nodes.some(node=>node.textContent.includes('Old title')));
});

test('Library dates prefer real updates and fall back from missing, zero or invalid updates', () => {
  const context = load(['libraryTimestamp'], { Date });
  const created = '2026-09-10T08:28:42.799Z';
  const updated = '2026-09-11T09:00:00Z';
  for (const value of [undefined, '', '0001-01-01T00:00:00Z', '0001-01-01T00:00:00.000Z', 'bad date']) {
    assert.equal(context.libraryTimestamp({ created_at: created, updated_at: value }), new Date(created).toLocaleString());
  }
  assert.equal(context.libraryTimestamp({ created_at: created, updated_at: updated }), new Date(updated).toLocaleString());
  assert.equal(context.libraryTimestamp({ created_at: 'bad', updated_at: '0001-01-01T00:00:00Z' }), '');
});

test('session reset cancels without reloading and states the durable boundary before confirmation', () => {
  let approved = false, reloads = 0, message = '';
  const context = load(['clearEverything'], {
    window: { confirm(value) { message = value; return approved; }, location: { reload() { reloads++; } } },
  });
  context.clearEverything();
  assert.equal(reloads, 0);
  assert.match(message, /Unsaved inputs, results and browser-local clips/);
  assert.match(message, /Saved Library items and running jobs will remain/);
  approved = true;
  context.clearEverything();
  assert.equal(reloads, 1);
});

test('result disclosure opens only its enclosing workflow steps without scrolling', () => {
  const outer = { open: false, parentElement: null };
  const inner = { open: false, parentElement: { closest: () => outer } };
  const unrelated = { open: false };
  const context = load(['revealWorkflowResult'], { window: { scrollTo() { assert.fail('result updates must not scroll'); } } });
  context.revealWorkflowResult({ closest: () => inner });
  assert.equal(inner.open, true);
  assert.equal(outer.open, true);
  assert.equal(unrelated.open, false);
  context.revealWorkflowResult({ closest: () => null });
});

test('voice save displays its returned name and link, and clears old confirmation on failure', async () => {
  const status = { children: [], replaceChildren() { this.children = []; }, appendChild(child) { this.children.push(child); } };
  let fail = false;
  const context = load(['showDesignSaveStatus', 'setDesignError', 'saveDesign'], {
    running: false, designSaveStatus: status, designErrorBox: {}, designSaveError: {}, designSaveButton: {},
    designDestinationSelect: { value: 'new-actor' }, designActorSelect: { value: '' }, designCharacterSelect: { value: '' }, revealWorkflowResult() {},
    designCandidate: { reference: 'wav', description: 'Narrator', transcript: 'Hello' },
    designNameInput: { value: 'Lantern narrator' }, selectedVoiceId: '',
    document: { createTextNode: text => ({ textContent: text }) },
    createElement: (tag, className, text) => ({ tag, className, textContent: text }),
    clearDesignError() {}, setRunning() {}, setBusy() {}, clearBusy() {}, log() {}, persistSelectedVoice() {}, refreshVoices: async () => {},
    FormData: class { append() {} }, File: class {},
    fetch: async () => ({ json: async () => ({ id: 'voice_1', name: 'Lantern narrator' }) }),
    ensureOk: async () => { if (fail) throw new Error('Storage unavailable'); },
  });
  await context.saveDesign();
  assert.equal(status.hidden, false);
  assert.match(status.children[0].textContent, /Saved “Lantern narrator”/);
  assert.equal(status.children[1].href, '#library');
  assert.equal(context.selectedVoiceId, 'voice_1');
  fail = true;
  await context.saveDesign();
  assert.equal(status.hidden, true);
  assert.equal(context.designSaveError.hidden, false);
  assert.equal(context.designSaveError.textContent, 'Storage unavailable');
});

test('tool navigation resets scroll only on a real change while preserving shared audio data', () => {
  const scrolls = [], modes = [];
  const ex = { samples: new Float32Array([0.1, 0.2]), transcript: 'Keep this edit' };
  const context = load(['applyPage'], {
    displayedPage: '', PAGE_PARENT: {}, transcribeRecorder: null, pageModules: [], parentLinks: [], parentNavs: [], pageLinks: [],
    document: { documentElement: { setAttribute() {} } },
    applyAudioWorkspaceMode: value => modes.push(value), ex,
    refreshModels() {}, refreshGPU() {}, refreshLibrary() {}, refreshAudiobooks() {}, refreshAudiobookBenchmarks() {}, drawExtractWave() {},
    window: { scrollTo: value => scrolls.push(value), setTimeout() {} },
  });
  context.applyPage('story');
  assert.equal(scrolls.length, 0, 'initial route keeps browser scroll restoration');
  context.applyPage('audiobook');
  assert.equal(scrolls.length, 1);
  assert.equal(scrolls[0].top, 0);
  context.applyPage('audiobook');
  assert.equal(scrolls.length, 1, 'same-tool refresh does not move viewport');
  context.applyPage('transcription');
  context.applyPage('extract');
  assert.deepEqual(modes, ['transcribe', 'extract']);
  assert.equal(ex.transcript, 'Keep this edit');
  assert.equal(ex.samples.length, 2);
});

test('voice request reveals output at start and again on completion after the user collapses it', async () => {
  for (const switchWhilePending of [false, true]) {
    const inputStep = { open: false, parentElement: null }, replyStep = { open: false, parentElement: null };
    const transcript = { closest: () => inputStep }, reply = { closest: () => replyStep };
    let sawOpenAtRequest = false;
    const context = load(['revealWorkflowResult', 'performVoiceTurn', 'setVoiceTranscript'], {
      voiceInputMode: 'hands-free', voiceInputSources: {},
      transcriptOutput: transcript, replyOutput: reply, conversation: [], voiceSelect: { value: '' }, ttsSpeechModelSelect: { value: 'audio' },
      FormData: class { append() {} }, log() {}, ensureOk: async () => {}, readSpeechPerformance: () => undefined,
      fetch: async () => {
        sawOpenAtRequest = inputStep.open && replyStep.open;
        inputStep.open = replyStep.open = false;
        if (switchWhilePending) {
          context.voiceInputMode = 'wav';
          transcript.value = 'Existing WAV transcript';
        }
        return { json: async () => ({ transcript: 'Hello', reply: 'Welcome', audio_b64: 'wav' }) };
      },
      base64ToBlob: () => ({ size: 10 }), recordExchange() {}, formatBytes: () => '10 B',
      URL: { createObjectURL: () => 'blob:test' }, activeAudioUrl: '', replyAudio: { load() {} }, saveReplyButton: {}, libraryReplyButton: {},
    });
    await context.performVoiceTurn({ message: 'Hello' });
    assert.equal(sawOpenAtRequest, true);
    assert.equal(replyStep.open, true);
    assert.equal(inputStep.open, !switchWhilePending);
    assert.equal(transcript.value, switchWhilePending ? 'Existing WAV transcript' : 'Hello');
    if (switchWhilePending) assert.equal(context.voiceInputSources['hands-free'].transcript, 'Hello');
    assert.equal(reply.value, 'Welcome');
  }
});

test('speech performance keeps zero strengths and ignores controls for other models', () => {
  const panel = { hidden: false, querySelectorAll: () => [
    { value: '0', type: 'number', dataset: { performance: 'intensity' }, checkValidity: () => true },
    { value: '0.7', type: 'number', dataset: { performance: 'emotion_vector_2' }, checkValidity: () => true },
    { value: '', type: 'text', dataset: { performance: 'emotion' } },
  ] };
  const context = load(['readSpeechPerformance'], { document: { getElementById: () => panel } });
  const p = context.readSpeechPerformance('clonePerformanceControls');
  assert.equal(p.intensity, 0);
  assert.deepEqual(Array.from(p.emotion_vector), [0, 0, 0.7, 0, 0, 0, 0, 0]);
  panel.hidden = true;
  assert.equal(context.readSpeechPerformance('clonePerformanceControls'), undefined);
});

test('voice input tabs preserve independent sources and transcripts and do not switch while disabled', () => {
  const elements = {};
  const tabs = ['record', 'live', 'hands-free', 'wav'].map(mode => ({
    dataset: { voiceInput: mode }, disabled: false,
    setAttribute(name, value) { this[name] = value; },
    getAttribute() { return mode + '-panel'; },
  }));
  const recording = { name: 'recording.wav' }, upload = { name: 'upload.wav' };
  const context = load(['selectVoiceInput', 'clearActiveWav', 'setVoiceTranscript'], {
    voiceInputTabs: tabs, voiceInputMode: 'record', voiceInputSources: {},
    transcriptOutput: { value: 'Recorded words' }, voiceTranscriptField: {},
    activeWavFile: recording, wavStatus: { textContent: 'recording: recording.wav' }, wavSaveButton: {}, wavInput: {},
    document: { getElementById(id) { return elements[id] ||= { appendChild(child) { child.panelId = id; } }; } }, clearError() {},
  });
  context.selectVoiceInput(tabs[3]);
  assert.equal(context.activeWavFile, null, 'upload tab must not use the recording');
  assert.equal(context.transcriptOutput.value, '');
  assert.equal(context.voiceTranscriptField.panelId, 'wav-panel');
  assert.equal(elements.voiceInputMeter.hidden, true);
  context.activeWavFile = upload;
  context.wavStatus.textContent = 'upload: upload.wav';
  context.setVoiceTranscript('Uploaded words', 'wav');
  context.selectVoiceInput(tabs[0]);
  assert.equal(context.activeWavFile, recording);
  assert.equal(context.transcriptOutput.value, 'Recorded words');
  assert.equal(context.voiceTranscriptField.panelId, 'record-panel');
  assert.equal(tabs[0]['aria-selected'], 'true');
  context.clearActiveWav();
  tabs[2].disabled = true;
  context.selectVoiceInput(tabs[2]);
  assert.equal(context.voiceInputMode, 'record');
  tabs[2].disabled = false;
  context.selectVoiceInput(tabs[2]);
  assert.equal(elements.voiceSendStep.hidden, true);
  assert.equal(elements.voiceWavSource.hidden, true);
  assert.equal(context.voiceTranscriptField.panelId, 'hands-free-panel');
  context.setVoiceTranscript('Hands-free words', 'hands-free');
  context.selectVoiceInput(tabs[3]);
  assert.equal(context.activeWavFile, upload, 'switching modes retains the uploaded source');
  assert.equal(context.transcriptOutput.value, 'Uploaded words');
  context.selectVoiceInput(tabs[1]);
  context.setVoiceTranscript('Live words', 'live');
  assert.equal(context.voiceTranscriptField.panelId, 'live-panel');
  context.selectVoiceInput(tabs[2]);
  assert.equal(context.transcriptOutput.value, 'Hands-free words');
  context.selectVoiceInput(tabs[1]);
  assert.equal(context.transcriptOutput.value, 'Live words');
  context.selectVoiceInput(tabs[0]);
  assert.equal(context.activeWavFile, null, 'a cleared recording must not reappear');
  assert.equal(context.transcriptOutput.value, '', 'a cleared recording transcript must not reappear');
});

test('manual voice input tabs require audio before sending', async () => {
  for (const mode of ['record', 'live', 'wav', 'hands-free']) {
    const sent = [], errors = [];
    const context = load(['runVoiceLoop'], {
      voiceInputMode: mode, activeWavFile: null,
      transcriptOutput: {}, runButton: {}, clearError() {}, revealWorkflowResult() {},
      setRunning() {}, setBusy() {}, resetOutputs() {}, clearBusy() {}, log() {},
      clearActiveWav() {}, replyAudio: { async play() {} },
      setError(error) { errors.push(error.message); },
      async performVoiceTurn(request) { sent.push(request.file); },
    });
    await context.runVoiceLoop();
    assert.equal(sent.length, 0);
    if (mode === 'record') assert.match(errors[0], /Record audio first/);
    if (mode === 'wav') assert.match(errors[0], /Choose a WAV/);
    if (mode === 'live') assert.match(errors[0], /Start live transcription/);
    const file = { name: 'recording.wav' };
    context.activeWavFile = file;
    await context.runVoiceLoop();
    assert.deepEqual(sent, mode === 'hands-free' ? [] : [file]);
    assert.equal(context.activeWavFile, file, 'sending retains the audio source');
  }
});

test('voice source can be reused with a different reply voice until replaced or removed', async () => {
  const requests = [];
  const context = load(['runVoiceLoop', 'setActiveWav', 'clearActiveWav'], {
    voiceInputMode: 'record', activeWavFile: null, voiceSelect: { value: 'first-voice' },
    wavInput: {}, wavStatus: {}, wavSaveButton: {}, formatBytes: size => size + ' B',
    transcriptOutput: {}, runButton: {}, clearError() {}, revealWorkflowResult() {},
    setRunning() {}, setBusy() {}, resetOutputs() {}, clearBusy() {}, log() {},
    replyAudio: { async play() {} }, setError(error) { throw error; },
    async performVoiceTurn(request) {
      requests.push({ file: request.file, voice: context.voiceSelect.value });
      if (request.onAccepted) request.onAccepted();
    },
  });
  const first = { name: 'recording.wav', size: 100 }, replacement = { name: 'new.wav', size: 200 };
  context.setActiveWav(first, 'recording');
  await context.runVoiceLoop();
  context.voiceSelect.value = 'second-voice';
  await context.runVoiceLoop();
  assert.deepEqual(requests, [{ file: first, voice: 'first-voice' }, { file: first, voice: 'second-voice' }]);
  assert.equal(context.activeWavFile, first);
  assert.equal(context.wavStatus.textContent, 'recording: recording.wav (100 B)');
  assert.equal(context.wavSaveButton.disabled, false);
  context.transcriptOutput.value = 'Old recording transcript';
  context.setActiveWav(replacement, 'recording');
  assert.equal(context.activeWavFile, replacement);
  assert.equal(context.transcriptOutput.value, '', 'replacement clears the old transcript');
  context.transcriptOutput.value = 'Live transcript so far';
  context.setActiveWav(replacement, 'live');
  assert.equal(context.transcriptOutput.value, 'Live transcript so far', 'accepting a live take retains its streamed transcript');
  context.clearActiveWav();
  assert.equal(context.activeWavFile, null);
  assert.equal(context.wavSaveButton.disabled, true);
  assert.equal(context.transcriptOutput.value, '');
});

test('stopped hands-free setup cannot open a hidden microphone after switching tabs', async () => {
  let grantMic, cleaned = 0, connected = 0;
  const context = load(['startHandsFree'], {
    handsFree: false, handsFreeSetupPending: false, running: false, recording: false, live: false, cloneRecording: false,
    voiceRecorder: { isPending: () => false }, cloneRecorder: { isPending: () => false },
    hfSessionToken: 0, canRecord: () => true, clearError() {}, syncControls() {},
    navigator: { mediaDevices: { getUserMedia: () => new Promise(resolve => { grantMic = resolve; }) } },
    window: { AudioContext: class {
      createMediaStreamSource() { return { connect() { connected++; } }; }
      createScriptProcessor() { return {}; }
    } },
    async cleanupRecorderResources() { cleaned++; }, setError(error) { throw error; },
  });
  const pending = context.startHandsFree();
  context.hfSessionToken++;
  context.handsFreeSetupPending = false;
  grantMic({});
  await pending;
  assert.equal(cleaned, 1);
  assert.equal(connected, 0);
  assert.equal(context.handsFree, false);
});

test('speech performance rejects conflicting description and blend before sending', () => {
  const panel = { hidden: false, querySelectorAll: () => [
    { value: 'relief', type: 'text', dataset: { performance: 'emotion' } },
    { value: '0.7', type: 'number', dataset: { performance: 'emotion_vector_0' } },
  ] };
  const context = load(['readSpeechPerformance'], { document: { getElementById: () => panel } });
  assert.throws(() => context.readSpeechPerformance('clonePerformanceControls'), /either an emotion direction or an emotion blend/);
});

test('speaker choices include declared and detected speakers and keep renamed colours until a new source', () => {
  const count = { value: '4' }, errors = [];
  const context = load(['createAudioWorkspaceModule', 'taggedSpeakers', 'workspaceSpeakers', 'speakerTagNames',
    'speakerColor', 'renameTranscriptSpeaker', 'renameWorkspaceSpeaker'], {
    document: { getElementById: () => count }, extractTranscribeStatus: {},
    renderExtractFilter() {}, refreshTranscribeSearch() {}, drawExtractWave() {},
    extractError(message) { errors.push(message); },
  });
  context.ex = context.createAudioWorkspaceModule();
  context.ex.segments = [{ speaker: 'A', start: 0, end: 2 }, { speaker: 'B', start: 2, end: 4 }];
  assert.deepEqual(Array.from(context.workspaceSpeakers()), ['A', 'B', 'C', 'D']);
  const originalColour = context.speakerColor('A');
  context.renameWorkspaceSpeaker('A', 'Narrator');
  assert.equal(context.ex.segments[0].speaker, 'Narrator');
  assert.equal(context.speakerColor('Narrator'), originalColour);
  assert.deepEqual(Array.from(context.speakerTagNames()), ['Narrator', 'B', 'C', 'D']);
  context.renameWorkspaceSpeaker('D', 'Guest');
  assert.deepEqual(Array.from(context.workspaceSpeakers()), ['Narrator', 'B', 'C', 'Guest'], 'unassigned declared speakers can be renamed');
  context.renameWorkspaceSpeaker('B', 'Narrator');
  assert.equal(errors.length, 1);
  assert.equal(context.ex.segments[1].speaker, 'B');
  count.value = '';
  context.ex.detectedSpeakers = ['Narrator', 'B', 'E'];
  assert.deepEqual(Array.from(context.speakerTagNames()), ['Narrator', 'B', 'E'], 'all detected speakers appear without extra defaults');
  context.ex.loadSource(new Float32Array(10), 10, 1, 'new.wav');
  assert.equal(context.workspaceSpeakers().length, 0);
  assert.equal(context.speakerColor('A'), originalColour);
  count.value = '26';
  assert.equal(context.workspaceSpeakers().length, 26);
});

test('speaker waveform overlays follow labelled segments and clip to the zoomed view', () => {
  const rectangles = [], labels = [];
  const context = load(['drawSpeakerRegions', 'speakerColor'], {
    ex: { view: { start: 2, end: 6 }, speakerLabels: [], segments: [
      { start: 0, end: 1, speaker: 'A' },
      { start: 1, end: 3, speaker: 'A' },
      { start: 3, end: 4, speaker: '' },
      { start: 4, end: 7, speaker: 'B' },
    ] },
  });
  const g = { save() {}, restore() {},
    fillRect(x, y, width, height) { rectangles.push({ x, y, width, height, colour: this.fillStyle, alpha: this.globalAlpha }); },
    fillText(name) { labels.push(name); },
  };
  context.drawSpeakerRegions(g, 400, 88);
  assert.deepEqual(rectangles.filter(r => r.alpha === 0.18).map(r => [r.x, r.width]), [[0, 100], [200, 200]]);
  assert.deepEqual(labels, ['A', 'B']);
  assert.notEqual(rectangles[0].colour, rectangles[2].colour);
});


test('clone saving stays gated by processing, destination and required name', () => {
  const fields = {}, reference = {};
  const context = load(['syncCloneAssignment'], {
    cloneDestinationSelect: { value: 'new-actor' }, clonePrepared: null, cloneWavFile: reference,
    running: false, cloneRecording: false,
    libraryVoices: [{ id: 'actor', protected: true, character_voices: [{ id: 'child' }] }],
    cloneActorSelect: { value: '' }, cloneCharacterSelect: { value: '' }, cloneNameInput: { value: '' },
    cloneRegenerateButton: {}, librarySpeakButton: {}, cloneTryVoiceSelect: { value: '' }, speakTextInput: { value: '' }, speakButton: {}, cloneSaveButton: {},
    cloneAssignButton: { dataset: {} }, document: { getElementById(id) { return fields[id] ||= {}; } },
  });
  context.syncCloneAssignment();
  assert.equal(context.cloneAssignButton.disabled, true);
  assert.equal(context.cloneDestinationSelect.disabled, true);
  context.clonePrepared = { file: reference };
  context.syncCloneAssignment();
  assert.equal(context.cloneDestinationSelect.disabled, false);
  assert.equal(context.cloneAssignButton.disabled, true);
  context.cloneNameInput.value = 'New';
  context.syncCloneAssignment();
  assert.equal(context.cloneAssignButton.disabled, false);
  context.cloneDestinationSelect.value = 'character';
  context.cloneActorSelect.value = 'actor';
  context.syncCloneAssignment();
  assert.equal(context.cloneAssignButton.disabled, true);
  context.cloneCharacterSelect.value = 'child';
  context.syncCloneAssignment();
  assert.equal(context.cloneAssignButton.disabled, false);
  assert.equal(fields.cloneNameField.hidden, true);
  context.cloneDestinationSelect.value = 'actor';
  context.syncCloneAssignment();
  assert.equal(context.cloneAssignButton.disabled, true); // Protected actor, but its child was assignable.
  context.cloneDestinationSelect.value = 'new-character';
  context.syncCloneAssignment(true);
  assert.equal(context.cloneAssignButton.disabled, true);
});

test('saved voice audition uses its own selected character and reports errors in step 4', async () => {
  for (const fails of [false, true]) {
    const calls = [];
    const context = load(['requestCloneSpeech', 'presentCloneSpeech', 'speakText'], {
      running: false, cloneAuditionDraft: null, selectedVoiceId: 'unrelated_voice_chat_actor', cloneTryVoiceSelect: { value: 'character_one' },
      speakTextInput: { value: '  Literal preview  ' }, cloneModelSelect: { value: 'omnivoice' },
      revealWorkflowResult() {}, cloneSpeakError: {}, speakButton: {}, saveSpeakButton: {}, librarySpeakButton: {},
      speakAudio: { load() {}, async play() {} }, speakAudioUrl: '',
      setRunning() {}, setBusy() {}, clearBusy() {}, clearSpeakAudio() {}, log() {}, formatBytes() { return ''; },
      readSpeechPerformance() {}, URL: { createObjectURL() { return 'blob:preview'; } },
      async fetch(url, options) { calls.push(JSON.parse(options.body)); return { async blob() { return { size: 50 }; } }; },
      async ensureOk() { if (fails) throw new Error('Speech unavailable'); },
    });
    await context.speakText();
    assert.equal(calls[0].voice, 'character_one');
    assert.equal(calls[0].input, 'Literal preview');
    assert.equal(context.selectedVoiceId, 'unrelated_voice_chat_actor');
    if (fails) {
      assert.equal(context.cloneSpeakError.hidden, false);
      assert.equal(context.cloneSpeakError.textContent, 'Speech unavailable');
    } else assert.equal(context.speakAudio.src, 'blob:preview');
  }
});


test('regeneration transcribes and auditions a draft without writing a saved voice, preserving the last candidate on failure', async () => {
  for (const stage of ['success', 'transcribe', 'speech']) {
    const calls = [], previous = { voiceID: 'character_one', file: { name: 'original.wav' }, transcript: 'old', pending: false };
    const context = load(['regenerateClone'], {
      running: false, cloneRegenerateButton: { disabled: false }, cloneTryVoiceSelect: { value: 'character_one' },
      speakTextInput: { value: 'new preview text' }, cloneAuditionDraft: previous, revealWorkflowResult() {}, cloneSpeakError: {}, cloneAuditionStatus: {},
      setRunning() {}, setBusy() {}, clearBusy() {}, FormData: class { append() {} }, async ensureOk() {},
      async fetch(url, options) { calls.push([url, options.method]); if (stage === 'transcribe') throw Error('Transcribe failed'); return { async json() { return { text: 'fresh transcript' }; } }; },
      async requestCloneSpeech(text, id, draft) { calls.push(['speech', text, id, draft.transcript]); if (stage === 'speech') throw Error('Speech failed'); return {}; },
      async presentCloneSpeech() { calls.push(['preview']); },
    });
    await context.regenerateClone();
    assert.deepEqual(calls[0], ['/v1/audio/transcriptions', 'POST']);
    assert.ok(calls.every(call => !['PUT', 'DELETE'].includes(call[1])));
    if (stage === 'success') {
      assert.equal(context.cloneAuditionDraft.voiceID, 'character_one');
      assert.equal(context.cloneAuditionDraft.file, previous.file);
      assert.equal(context.cloneAuditionDraft.transcript, 'fresh transcript');
      assert.equal(context.cloneAuditionDraft.pending, true);
    } else {
      assert.equal(context.cloneAuditionDraft, previous);
      assert.equal(context.cloneSpeakError.hidden, false);
      assert.ok(!calls.some(call => call[0] === 'preview'));
    }
  }
});

test('saving a regenerated clone updates only its bound identity, never uploads the preview as a library asset', async () => {
  for (const id of ['actor_one', 'character_one']) {
    for (const fails of [false, true]) {
      const calls = [], fields = [], draft = { voiceID: id, file: { name: 'original.wav' }, transcript: 'fresh', pending: true };
      const context = load(['saveAuditionClone'], {
        running: false, librarySpeakButton: { disabled: false }, cloneAuditionDraft: draft, cloneTryVoiceSelect: { value: id },
        revealWorkflowResult() {}, cloneSpeakError: {}, cloneAuditionStatus: {}, FormData: class { append(...args) { fields.push(args); } },
        setRunning() {}, setBusy() {}, clearBusy() {}, appendCloneSource() {}, async refreshVoices() {},
        async fetch(url, options) { calls.push([url, options.method]); return {}; }, async ensureOk() { if (fails) throw Error('Disk full'); },
      });
      await context.saveAuditionClone();
      assert.deepEqual(calls, [[(id.startsWith('character_') ? '/v1/character-voices/' : '/v1/voices/') + id + '/reference', 'PUT']]);
      assert.equal(fields.find(x => x[0] === 'file')[1], draft.file);
      assert.equal(draft.pending, fails);
      if (fails) assert.equal(context.cloneSpeakError.hidden, false);
      calls.length = 0;
      context.cloneTryVoiceSelect.value = 'different_actor';
      await context.saveAuditionClone();
      assert.equal(calls.length, 0);
    }
  }
});

test('draft speech uses its reference and transcript, while another selected voice uses its saved identity', async () => {
  const calls = [], draft = { voiceID: 'character_one', file: { name: 'original.wav' }, transcript: 'reprocessed words' };
  class Form { constructor() { this.fields = []; } append(...args) { this.fields.push(args); } }
  const context = load(['requestCloneSpeech'], {
    cloneModelSelect: { value: 'audio' }, readSpeechPerformance() {}, FormData: Form, async ensureOk() {},
    async fetch(url, options) { calls.push(options); return { async blob() { return {}; } }; },
  });
  await context.requestCloneSpeech('literal words', 'character_one', draft);
  assert.equal(calls[0].body.fields.find(x => x[0] === 'file')[1], draft.file);
  assert.deepEqual(calls[0].body.fields.find(x => x[0] === 'transcript'), ['transcript', draft.transcript]);
  await context.requestCloneSpeech('different words', 'other_actor', draft);
  assert.equal(JSON.parse(calls[1].body).voice, 'other_actor');
});


test('voice design generates three sequential candidates and retains successes when one fails', async () => {
  const requests = [], refs = [], playing = [], states = [];
  let active = 0, seed = 0;
  const context = load(['generateDesign'], {
    running: false, designDescriptionInput: { value: 'Warm narrator' }, designModelSelect: { value: 'voxcpm2' },
    designGenerateButton: {}, designStatus: {}, designGenerationStatus: {}, designCandidate: null, designCandidates: [],
    designCandidateStatuses: [{}, {}, {}], designCandidateRadios: [{}, {}, {}],
    designCandidateAudios: [0, 1, 2].map(i => ({ load() {}, play() { playing.push(i); } })),
    clearDesignError() {}, clearDesignCandidate() {}, setDesignError(e) { assert.fail(e.message); },
    setRunning(v) { states.push(v); }, setBusy() {}, clearBusy() {}, revealWorkflowResult() {}, log() {},
    crypto: { getRandomValues(array) { array[0] = ++seed; return array; } },
    base64ToBlob: data => data, URL: { createObjectURL: data => 'blob:' + data },
    selectDesignCandidate(i) { context.designCandidate = context.designCandidates[i]; },
    async fetch(url, options) {
      assert.equal(active++, 0, 'only one candidate in flight');
      const body = JSON.parse(options.body); requests.push(body);
      await Promise.resolve(); active--;
      if (requests.length === 2) throw new Error('Engine failed');
      return { async json() { return { reference_b64: 'reference' + requests.length, preview_b64: 'preview' + requests.length, transcript: 'Sample' }; } };
    }, ensureOk: async () => {},
  });
  await context.generateDesign();
  assert.equal(requests.length, 3);
  assert.equal(new Set(requests.map(r => r.seed)).size, 3);
  assert.ok(requests.every(r => r.description === 'Warm narrator' && r.model === 'voxcpm2'));
  assert.equal(context.designCandidates[0].reference, 'reference1');
  assert.equal(context.designCandidates[1], undefined);
  assert.equal(context.designCandidates[2].reference, 'reference3');
  assert.equal(context.designCandidate, context.designCandidates[0]);
  assert.match(context.designCandidateStatuses[1].textContent, /Engine failed/);
  assert.match(context.designGenerationStatus.textContent, /2 of 3/);
  assert.deepEqual(playing, [], 'candidate auditions wait for the user');
  assert.deepEqual(states, [true, false]);
});

test('saving a designed candidate routes to the chosen identity and keeps the candidate on failure', async () => {
  for (const mode of ['new-actor', 'new-character', 'actor', 'character']) {
    for (const fail of [false, true]) {
      const calls = [], fields = [], errors = [];
      const candidate = { reference: 'selected reference', transcript: 'Selected transcript', preview: 'audition' };
      const context = load(['saveDesign'], {
        running: false, designSaveButton: {}, designCandidate: candidate, designNameInput: { value: 'New voice' },
        designDestinationSelect: { value: mode }, designActorSelect: { value: 'actor_1', selectedOptions: [{ textContent: 'Actor' }] },
        designCharacterSelect: { value: 'character_1', selectedOptions: [{ textContent: 'Character' }] }, designSaveStatus: {},
        clearDesignError() {}, showDesignSaveStatus() {}, setDesignError(e) { errors.push(e.message); },
        setRunning() {}, setBusy() {}, clearBusy() {}, log() {}, revealWorkflowResult() {}, persistSelectedVoice() {}, refreshVoices: async () => {},
        FormData: class { append(...args) { fields.push(args); } }, File: class { constructor(parts) { this.parts = parts; } },
        fetch: async (url, options) => { calls.push([url, options.method]); return { json: async () => ({ id: 'created' }) }; },
        ensureOk: async () => { if (fail) throw new Error('Save failed'); },
      });
      await context.saveDesign();
      assert.deepEqual(calls, [{ 'new-actor': ['/v1/voices', 'POST'], 'new-character': ['/v1/voices/actor_1/characters', 'POST'], actor: ['/v1/voices/actor_1/reference', 'PUT'], character: ['/v1/character-voices/character_1/reference', 'PUT'] }[mode]]);
      assert.deepEqual(Array.from(fields.find(f => f[0] === 'file')[1].parts), ['selected reference']);
      assert.deepEqual(fields.find(f => f[0] === 'transcript'), ['transcript', 'Selected transcript']);
      assert.equal(context.designCandidate, candidate);
      assert.equal(errors.length, fail ? 1 : 0);
    }
  }
});
