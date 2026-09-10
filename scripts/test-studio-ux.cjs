const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const assert = require('node:assert/strict');

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
    designSaveStatus: status, designErrorBox: {}, designSaveButton: {},
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
  assert.equal(context.designErrorBox.hidden, false);
  assert.equal(context.designErrorBox.textContent, 'Storage unavailable');
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
  const step = { open: false, parentElement: null };
  const transcript = { closest: () => step }, reply = {};
  let sawOpenAtRequest = false;
  const context = load(['revealWorkflowResult', 'performVoiceTurn'], {
    transcriptOutput: transcript, replyOutput: reply, conversation: [], voiceSelect: { value: '' }, ttsSpeechModelSelect: { value: 'audio' },
    FormData: class { append() {} }, log() {}, ensureOk: async () => {}, readSpeechPerformance: () => undefined,
    fetch: async () => { sawOpenAtRequest = step.open; step.open = false; return { json: async () => ({ transcript: 'Hello', reply: 'Welcome', audio_b64: 'wav' }) }; },
    base64ToBlob: () => ({ size: 10 }), recordExchange() {}, formatBytes: () => '10 B',
    URL: { createObjectURL: () => 'blob:test' }, activeAudioUrl: '', replyAudio: { load() {} }, saveReplyButton: {}, libraryReplyButton: {},
  });
  await context.performVoiceTurn({ message: 'Hello' });
  assert.equal(sawOpenAtRequest, true);
  assert.equal(step.open, true);
  assert.equal(transcript.value, 'Hello');
  assert.equal(reply.value, 'Welcome');
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

test('speech performance rejects conflicting description and blend before sending', () => {
  const panel = { hidden: false, querySelectorAll: () => [
    { value: 'relief', type: 'text', dataset: { performance: 'emotion' } },
    { value: '0.7', type: 'number', dataset: { performance: 'emotion_vector_0' } },
  ] };
  const context = load(['readSpeechPerformance'], { document: { getElementById: () => panel } });
  assert.throws(() => context.readSpeechPerformance('clonePerformanceControls'), /either an emotion direction or an emotion blend/);
});
