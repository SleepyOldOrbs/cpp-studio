const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const assert = require('node:assert/strict');

const source = fs.readFileSync(path.join(__dirname, '../internal/demo/static/story-builder.js'), 'utf8');
function functionSource(name) {
  const start = source.search(new RegExp(`  (?:async )?function ${name}\\(`));
  assert.ok(start >= 0, `missing ${name}`);
  const rest = source.slice(start + 3);
  const next = rest.search(/\n  (?:async )?function /);
  assert.ok(next >= 0, `missing function boundary after ${name}`);
  return source.slice(start, start + 3 + next);
}

test('save reconciliation preserves edits made while a save is in flight', () => {
  const context = vm.createContext({
    clone: structuredClone, normalizeTracks() {}, timelineError: () => '',
    window: { clearTimeout() {}, setTimeout: () => 1 },
  });
  vm.runInContext(functionSource('createEditSession') + '\nthis.session = createEditSession();', context);
  const session = context.session;
  const project = { id: 'project_1', name: 'Test', revision: 1, scenes: [], tracks: [], timeline_duration_ms: 30000 };
  session.upsert(project); session.open(project);
  const saved = structuredClone(project); saved.revision = 2;
  const version = session.saveVersion();
  session.edit(project => { project.timeline_duration_ms = 60000; });
  session.scheduleAutosave(() => {});
  assert.equal(session.reconcileSaved(saved, version), true);
  assert.equal(session.current().timeline_duration_ms, 60000);
  assert.equal(session.current().revision, 2);
  const next = structuredClone(session.current()); next.revision = 3;
  assert.equal(session.reconcileSaved(next, session.saveVersion()), false);
  assert.equal(session.current().timeline_duration_ms, 60000);
});

for (const terminal of ['complete', 'cancelled', 'failed']) {
  test(`build polling avoids unchanged project reloads and refreshes on ${terminal}`, async () => {
    const initial = { project_id: 'project_1', status: 'running', completed: 0, total: 2, active_clip_id: 'line_1', status_url: '/build/status' };
    const progressed = { ...initial, completed: 1, active_clip_id: 'line_2' };
    const replies = [initial, initial, initial, progressed, progressed, { ...progressed, status: terminal }];
    let refreshes = 0;
    const waits = [], statuses = [], mutations = new Map();
    const context = vm.createContext({
      activeDialogueBuild: null, dialogueCancelPending: false,
      editSession: {
        mutation: key => mutations.get(key),
        beginMutation: (key, promise) => mutations.set(key, promise),
        endMutation: key => mutations.delete(key), invalidateHistory() {},
      },
      updateBuildControls() {},
      updateRenderControls() {},
      setBuildStatus: (message, status) => statuses.push(status),
      refreshBuiltProject: async () => { refreshes++; return {}; },
      dialogueBuildableCount: () => 0,
      wait: async milliseconds => { waits.push(milliseconds); },
      request: async url => { assert.equal(url, initial.status_url); assert.ok(replies.length); return replies.shift(); },
    });
    vm.runInContext(functionSource('monitorDialogueBuild'), context);
    await context.monitorDialogueBuild(initial);
    assert.equal(refreshes, 3, 'only initial state, completed clip and terminal state reload the project');
    assert.equal(replies.length, 0);
    assert.ok(waits.every(ms => ms >= 250 && ms <= 2000));
    assert.equal(mutations.size, 0);
    assert.equal(statuses.at(-1), terminal === 'complete' ? 'ready' : terminal);
  });
}

test('empty-project render gate preserves previous delivery and permits silence clips', () => {
  let project = { id: 'project_1', tracks: [], renders: [{ revision: 2 }] };
  let pending = false;
  let status = '';
  const renderButton = {};
  const latestMaster = { removeAttribute(name) { delete this[name]; } };
  const context = vm.createContext({
    apiRoot: '/v1/story-builder-projects',
    currentProject: () => project,
    serverMutationPending: () => pending,
    editSession: { mutation: () => false },
    renderButton, latestMaster,
    setRenderStatus: message => { status = message; },
    renderRenderHistory() {},
  });
  vm.runInContext(functionSource('updateRenderControls'), context);
  context.updateRenderControls();
  assert.equal(renderButton.disabled, true);
  assert.match(status, /Add a clip/);
  assert.equal(latestMaster.hidden, false);
  assert.equal(latestMaster.href, '/v1/story-builder-projects/project_1/master');
  assert.equal(latestMaster.textContent, 'Latest master (r2)');

  project.tracks.push({ type: 'dialogue', clips: [] });
  context.updateRenderControls();
  assert.equal(renderButton.disabled, true, 'an empty track still cannot render');

  project.tracks[0].clips.push({ type: 'silence', duration_ms: 1000 });
  context.updateRenderControls();
  assert.equal(renderButton.disabled, false, 'silence is valid timeline content');
  assert.equal(status, 'Rendered revision 2');

  pending = true;
  context.updateRenderControls();
  assert.equal(renderButton.disabled, true, 'existing mutation exclusion remains intact');
});

function transitionFixture({ failSave = false, gateFirstSave = null } = {}) {
  const original = { id: 'first', name: 'Original', revision: 1, scenes: [], tracks: [], timeline_duration_ms: 30000 };
  const durable = new Map([[original.id, structuredClone(original)], ['second', { ...structuredClone(original), id: 'second', name: 'Second' }]]);
  const calls = [];
  let puts = 0;
  const context = vm.createContext({
    clone: structuredClone, normalizeTracks() {}, timelineError: () => '',
    window: { clearTimeout() {}, setTimeout: () => 1 },
    projectTransitionPending: false, requestedProjectID: '', apiRoot: '/projects',
    appShell: { inert: false }, nameInput: { value: original.name }, saveStatus: { dataset: { state: 'saved' } },
    projectList: {}, serverMutationPending: () => false,
    renderProjects() {}, renderTracks() {},
    request: async (url, options = {}) => {
      calls.push(`${options.method || 'GET'} ${url}`);
      if (options.method === 'PUT') {
        if (++puts === 1 && gateFirstSave) await gateFirstSave;
        if (failSave) throw new Error('offline');
        const id = url.split('/').at(-1);
        const saved = { ...JSON.parse(options.body), id, revision: durable.get(id).revision + 1 };
        durable.set(id, structuredClone(saved));
        return saved;
      }
      return url === '/projects' ? { projects: [...durable.values()].map(structuredClone) } : structuredClone(durable.get(url.split('/').at(-1)));
    },
  });
  for (const name of ['createEditSession', 'hasUnsavedProject', 'runProjectTransition', 'saveProject', 'openProject', 'refreshProjects']) {
    vm.runInContext(functionSource(name), context);
  }
  context.editSession = vm.runInContext('editSession', context);
  context.editSession.upsert(original); context.editSession.open(original);
  context.currentProject = () => context.editSession.current();
  context.setStatus = state => { context.saveStatus.dataset.state = state; };
  context.showCurrent = project => {
    context.editSession.open(project); context.nameInput.value = project.name; context.setStatus('saved');
  };
  const edit = name => {
    context.currentProject().name = name; context.nameInput.value = name;
    context.editSession.scheduleAutosave(() => {}); context.setStatus('dirty');
  };
  return { context, calls, durable, edit };
}

for (const transition of ['open', 'refresh']) {
  test(`${transition} saves a pending edit before replacing project state`, async () => {
    const { context, calls, durable, edit } = transitionFixture();
    edit('Must survive');
    if (transition === 'open') await context.openProject('second');
    else await context.refreshProjects();
    assert.equal(durable.get('first').name, 'Must survive');
    assert.deepEqual(calls, ['PUT /projects/first', transition === 'open' ? 'GET /projects/second' : 'GET /projects']);
    assert.equal(context.appShell.inert, false);
  });
}

test('failed save blocks a project transition and retains the editable draft', async () => {
  const { context, calls, edit } = transitionFixture({ failSave: true });
  edit('Keep this draft');
  await context.openProject('second');
  assert.deepEqual(calls, ['PUT /projects/first']);
  assert.equal(context.currentProject().id, 'first');
  assert.equal(context.currentProject().name, 'Keep this draft');
  assert.equal(context.saveStatus.dataset.state, 'failed');
  assert.equal(context.appShell.inert, false);
  assert.equal(context.hasUnsavedProject(), true);
});

test('transition waits for an in-flight save and its newer queued edit', async () => {
  let release;
  const gateFirstSave = new Promise(resolve => { release = resolve; });
  const { context, calls, durable, edit } = transitionFixture({ gateFirstSave });
  edit('First edit');
  const save = context.saveProject();
  edit('Newer edit');
  const transition = context.openProject('second');
  assert.equal(context.appShell.inert, true);
  release();
  await Promise.all([save, transition]);
  assert.deepEqual(calls, ['PUT /projects/first', 'PUT /projects/first', 'GET /projects/second']);
  assert.equal(durable.get('first').name, 'Newer edit');
  assert.equal(context.currentProject().id, 'second');
  assert.equal(context.hasUnsavedProject(), false);
});

test('document shortcuts cannot edit the old project while its replacement is loading', async () => {
  const { context, durable, edit } = transitionFixture();
  let release, entered;
  const gate = new Promise(resolve => { release = resolve; });
  const loading = new Promise(resolve => { entered = resolve; });
  const request = context.request;
  context.request = async (...args) => {
    if (args[0] === '/projects/second') { entered(); await gate; }
    return request(...args);
  };
  context.editSession.edit(project => { project.timeline_duration_ms = 60000; });
  edit('Saved before switching');
  context.stopBrowserPlayback = () => {};
  context.scheduleAutosave = () => context.setStatus('dirty');
  vm.runInContext(functionSource('restoreTimeline'), context);
  let keydown;
  context.document = { addEventListener: (name, handler) => { assert.equal(name, 'keydown'); keydown = handler; } };
  context.isEditableTarget = () => false;
  context.removeSelectedClips = () => { throw new Error('delete reached old project'); };
  const start = source.indexOf('  document.addEventListener("keydown",');
  const end = source.indexOf('\n  });', start);
  vm.runInContext(source.slice(start, end + 6), context);
  const transition = context.openProject('second');
  await loading;
  for (const key of ['z', 'y', 's', 'Delete']) keydown({ key, ctrlKey: key.length === 1, target: {}, preventDefault() {} });
  assert.equal(context.currentProject().timeline_duration_ms, 60000);
  assert.equal(context.saveStatus.dataset.state, 'saved');
  release(); await transition;
  assert.equal(durable.get('first').timeline_duration_ms, 60000);
});

for (const terminal of ['complete', 'cancelled', 'failed']) {
  test(`render control unlocks after dialogue build ${terminal}`, async () => {
    const project = { id: 'project_1', tracks: [{ clips: [{ type: 'dialogue', status: 'ready' }] }], renders: [] };
    const mutations = new Map();
    const renderButton = {};
    const context = vm.createContext({
      apiRoot: '/projects', currentProject: () => project,
      serverMutationPending: () => mutations.size > 0,
      editSession: { mutation: key => mutations.get(key), beginMutation: (key, value) => mutations.set(key, value), endMutation: key => mutations.delete(key), invalidateHistory() {} },
      renderButton, latestMaster: { removeAttribute() {} }, renderRenderHistory() {}, setRenderStatus() {},
      activeDialogueBuild: null, dialogueCancelPending: false, updateBuildControls() {}, setBuildStatus() {}, dialogueBuildableCount: () => 0,
    });
    vm.runInContext(functionSource('updateRenderControls') + functionSource('monitorDialogueBuild'), context);
    context.refreshBuiltProject = async () => {
      context.updateRenderControls();
      assert.equal(renderButton.disabled, true, 'render stays disabled while the build owns the mutation');
      return project;
    };
    await context.monitorDialogueBuild({ project_id: project.id, status: terminal, completed: 1, total: 1 });
    assert.equal(mutations.size, 0);
    assert.equal(renderButton.disabled, false, 'terminal completion refreshes render availability without another edit');
  });
}

for (const operation of ['revoiceCharacterVoice', 'placeLibraryAudio']) {
  test(`${operation} refreshes render availability after releasing its mutation`, async () => {
    const track = { id: 'track_1', name: 'Track', type: operation === 'placeLibraryAudio' ? 'sfx' : 'dialogue', clips: [{ id: 'clip_1', type: 'dialogue', status: 'ready' }] };
    const project = { id: 'project_1', name: 'Project', revision: 1, tracks: [track], renders: [] };
    const mutations = new Map();
    const renderButton = {};
    const context = vm.createContext({
      apiRoot: '/projects', currentProject: () => project, serverMutationPending: () => mutations.size > 0,
      editSession: { mutation: key => mutations.get(key), savePromise: () => null, beginMutation: (key, value) => mutations.set(key, value), endMutation: key => mutations.delete(key), applySaved: () => true },
      saveStatus: { dataset: { state: 'saved' } }, stopBrowserPlayback() {}, setStatus() {}, setVoiceLibraryStatus() {},
      appShell: { inert: false }, clone: structuredClone, request: async () => project, renderProjects() {},
      trackTypeForMediaRole: () => 'sfx', renderButton, latestMaster: { removeAttribute() {} }, renderRenderHistory() {}, setRenderStatus() {},
    });
    vm.runInContext(functionSource('updateRenderControls') + functionSource(operation), context);
    context.renderTracks = () => {
      context.updateRenderControls();
      assert.equal(renderButton.disabled, true, 'the successful project refresh still occurs inside the mutation');
    };
    if (operation === 'revoiceCharacterVoice') await context.revoiceCharacterVoice(track, { id: 'actor_1', name: 'Actor' }, { id: 'character_2', name: 'Character' });
    else await context.placeLibraryAudio(track, { id: 'audio_1', name: 'Effect', mediaRole: 'sfx' }, 0);
    assert.equal(mutations.size, 0);
    assert.equal(context.appShell.inert, false);
    assert.equal(renderButton.disabled, false);
  });
}
