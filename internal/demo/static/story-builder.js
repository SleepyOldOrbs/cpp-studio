(() => {
  "use strict";

  const apiRoot = "/v1/story-builder-projects";
  const snapIntervalMS = 250;
  const trackHeaderWidth = 224;
  const byID = (id) => document.getElementById(id);
  const newForm = byID("storyBuilderNewForm");
  const newNameInput = byID("storyBuilderNewName");
  const projectList = byID("storyBuilderProjectList");
  const editor = byID("storyBuilderEditor");
  const emptyState = byID("storyBuilderEmpty");
  const nameInput = byID("storyBuilderNameInput");
  const saveStatus = byID("storyBuilderSaveStatus");
  const saveButton = byID("storyBuilderSaveButton");
  const deleteButton = byID("storyBuilderDeleteButton");
  const refreshButton = byID("storyBuilderRefreshButton");
  const tracksElement = byID("storyBuilderTracks");
  const undoButton = byID("storyBuilderUndo");
  const redoButton = byID("storyBuilderRedo");
  const snapInput = byID("storyBuilderSnap");
  const timelineDurationInput = byID("storyBuilderTimelineDuration");
  const zoomInput = byID("storyBuilderZoom");
  const zoomValue = byID("storyBuilderZoomValue");
  const timelineViewport = byID("storyBuilderTimelineViewport");
  const timelineContent = byID("storyBuilderTimelineContent");
  const timelineRuler = byID("storyBuilderTimelineRuler");
  const selectionPanel = byID("storyBuilderSelectionPanel");
  const selectionHandle = byID("storyBuilderSelectionHandle");
  const selectionBody = byID("storyBuilderSelectionBody");

  let projects = [];
  let currentID = "";
  let autosaveTimer = 0;
  let savePromise = null;
  let saveAgain = false;
  let editVersion = 0;
  let undoStack = [];
  let redoStack = [];
  let selectedClipIDs = new Set();
  let panelPosition = null;
  let clipPointerEdit = null;
  let panelPointerEdit = null;
  let requestedProjectID = new URLSearchParams(window.location.search).get("project") || "";

  async function request(path, options = {}) {
    const response = await fetch(path, {
      ...options,
      headers: options.body ? { "Content-Type": "application/json" } : undefined,
    });
    if (response.ok) return response.status === 204 ? null : response.json();
    let message = `Request failed (${response.status})`;
    try {
      const body = await response.json();
      if (body.error) message = body.error;
    } catch (_) {
      // Keep the useful HTTP fallback when an upstream returns non-JSON.
    }
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }

  function currentProject() {
    return projects.find((project) => project.id === currentID) || null;
  }

  function mintID(prefix) {
    if (window.crypto && typeof window.crypto.randomUUID === "function") return `${prefix}_${window.crypto.randomUUID()}`;
    return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2)}`;
  }

  function clone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  function normalizeTracks(project) {
    project.tracks = Array.isArray(project.tracks) ? project.tracks : [];
    project.tracks.forEach((track, index) => {
      track.order = index;
      track.clips = Array.isArray(track.clips) ? track.clips : [];
    });
    if (!Number.isInteger(project.timeline_duration_ms) || project.timeline_duration_ms <= 0) {
      const latestEnd = latestClipEndMS(project);
      project.timeline_duration_ms = Math.max(30000, Math.ceil(latestEnd / 30000) * 30000);
    }
  }

  function latestClipEndMS(project) {
    return project.tracks.reduce((projectEnd, track) => track.clips.reduce(
      (trackEnd, clip) => Math.max(trackEnd, clip.start_ms + clip.duration_ms),
      projectEnd,
    ), 0);
  }

  function timelineDurationMS(project) {
    return project.timeline_duration_ms;
  }

  function renderTimelineRuler(durationMS) {
    timelineRuler.replaceChildren();
    for (let index = 0; index <= 6; index += 1) {
      const tick = document.createElement("span");
      const seconds = (durationMS / 6 / 1000) * index;
      tick.textContent = `${Number(seconds.toFixed(2))}s`;
      timelineRuler.append(tick);
    }
  }

  function updateTimelineWidth(durationMS) {
    const pixelsPerSecond = Number(zoomInput.value);
    const requestedWidth = trackHeaderWidth + (durationMS / 1000) * pixelsPerSecond;
    timelineContent.style.width = `${Math.max(timelineViewport.clientWidth, requestedWidth)}px`;
    zoomValue.value = `${pixelsPerSecond} px/s`;
    zoomValue.textContent = `${pixelsPerSecond} px/s`;
  }

  function setStatus(state, detail = "") {
    const labels = { dirty: "Unsaved changes", saving: "Saving…", saved: "Saved", failed: "Save failed" };
    saveStatus.dataset.state = state;
    saveStatus.textContent = detail ? `${labels[state]} — ${detail}` : labels[state];
  }

  function scheduleAutosave() {
    editVersion += 1;
    setStatus("dirty");
    window.clearTimeout(autosaveTimer);
    autosaveTimer = window.setTimeout(() => saveProject(), 600);
  }

  function renderProjects() {
    projectList.replaceChildren();
    if (!projects.length) {
      const message = document.createElement("p");
      message.className = "project-list-empty";
      message.textContent = "No Story Builder Projects yet.";
      projectList.append(message);
      return;
    }
    for (const project of projects) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "project-item";
      button.dataset.projectId = project.id;
      button.setAttribute("aria-current", String(project.id === currentID));
      const name = document.createElement("strong");
      name.textContent = project.name;
      const updated = document.createElement("span");
      updated.textContent = `Updated ${new Date(project.updated_at).toLocaleString()}`;
      button.append(name, updated);
      button.addEventListener("click", () => openProject(project.id));
      projectList.append(button);
    }
  }

  function actionButton(label, title, action, disabled = false) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "track-control";
    button.textContent = label;
    button.title = title;
    button.setAttribute("aria-label", title);
    button.disabled = disabled;
    button.addEventListener("click", action);
    return button;
  }

  function findClip(id) {
    const project = currentProject();
    if (!project) return null;
    for (const track of project.tracks) {
      const clip = track.clips.find((item) => item.id === id);
      if (clip) return { track, clip };
    }
    return null;
  }

  function selectedClips() {
    const project = currentProject();
    if (!project) return [];
    const selected = [];
    for (const track of project.tracks) {
      for (const clip of track.clips) {
        if (selectedClipIDs.has(clip.id)) selected.push({ track, clip });
      }
    }
    return selected;
  }

  function clipStatus(clip) {
    if (clip.status) return String(clip.status);
    if (clip.type === "silence") return "ready";
    return clip.source_id ? "ready" : "needs audio";
  }

  function timelineError(project) {
    if (!Number.isInteger(project.timeline_duration_ms) || project.timeline_duration_ms <= 0) {
      return "The project length must be a positive whole millisecond value.";
    }
    for (const track of project.tracks) {
      const ordered = [...track.clips].sort((left, right) => left.start_ms - right.start_ms);
      for (const clip of ordered) {
        if (!Number.isInteger(clip.start_ms) || !Number.isInteger(clip.duration_ms) || clip.start_ms < 0 || clip.duration_ms <= 0) {
          return "Clip times must be positive whole milliseconds.";
        }
        if (clip.start_ms + clip.duration_ms > project.timeline_duration_ms) {
          return "Clips must remain inside the project length.";
        }
        const hasSource = Boolean(clip.source_id || clip.source_duration_ms || clip.source_in_ms || clip.source_out_ms);
        if (hasSource && (clip.type === "silence" || !clip.source_id || clip.source_duration_ms <= 0 || clip.source_in_ms < 0 ||
          clip.source_out_ms <= clip.source_in_ms || clip.source_out_ms > clip.source_duration_ms ||
          clip.duration_ms !== clip.source_out_ms - clip.source_in_ms)) {
          return "Audio trims must remain inside the source bounds.";
        }
      }
      for (let index = 1; index < ordered.length; index += 1) {
        const previousEnd = ordered[index - 1].start_ms + ordered[index - 1].duration_ms;
        if (ordered[index].start_ms < previousEnd) return `Clips cannot overlap on ${track.name}.`;
      }
    }
    return "";
  }

  function updateHistoryButtons() {
    undoButton.disabled = undoStack.length === 0;
    redoButton.disabled = redoStack.length === 0;
  }

  function pruneSelection() {
    const available = new Set();
    const project = currentProject();
    if (project) project.tracks.forEach((track) => track.clips.forEach((clip) => available.add(clip.id)));
    selectedClipIDs = new Set([...selectedClipIDs].filter((id) => available.has(id)));
  }

  function timelineSnapshot(project) {
    return { tracks: clone(project.tracks), timeline_duration_ms: project.timeline_duration_ms };
  }

  function applyTimelineSnapshot(project, snapshot) {
    project.tracks = clone(snapshot.tracks);
    project.timeline_duration_ms = snapshot.timeline_duration_ms;
    normalizeTracks(project);
  }

  function acceptTimelineEdit(mutator) {
    const project = currentProject();
    if (!project) return false;
    const before = timelineSnapshot(project);
    mutator(project);
    normalizeTracks(project);
    const error = timelineError(project);
    if (error) {
      applyTimelineSnapshot(project, before);
      setStatus("failed", error);
      renderTracks();
      return false;
    }
    if (JSON.stringify(before) === JSON.stringify(timelineSnapshot(project))) {
      renderTracks();
      return false;
    }
    undoStack.push(before);
    redoStack = [];
    pruneSelection();
    scheduleAutosave();
    renderTracks();
    return true;
  }

  function restoreTimeline(source, destination) {
    const project = currentProject();
    if (!project || !source.length) return;
    destination.push(timelineSnapshot(project));
    applyTimelineSnapshot(project, source.pop());
    pruneSelection();
    scheduleAutosave();
    renderTracks();
  }

  function selectClip(id, additive = false) {
    if (additive) {
      if (selectedClipIDs.has(id)) selectedClipIDs.delete(id);
      else selectedClipIDs.add(id);
    } else {
      selectedClipIDs = new Set([id]);
    }
    refreshSelectionUI();
  }

  function refreshSelectionUI() {
    document.querySelectorAll(".timeline-clip").forEach((element) => {
      const selected = selectedClipIDs.has(element.dataset.clipId);
      element.classList.toggle("is-selected", selected);
      element.setAttribute("aria-pressed", String(selected));
    });
    renderSelection();
  }

  function selectionField(labelText, value, onChange = null, options = {}) {
    const label = document.createElement("label");
    label.className = "selection-field";
    const title = document.createElement("span");
    title.textContent = labelText;
    label.append(title);
    if (!onChange) {
      const output = document.createElement("span");
      output.className = "selection-value";
      output.textContent = String(value);
      label.append(output);
      return label;
    }
    const input = document.createElement("input");
    input.type = options.type || "text";
    input.value = String(value);
    if (options.min !== undefined) input.min = String(options.min);
    if (options.step !== undefined) input.step = String(options.step);
    if (options.maxLength) input.maxLength = options.maxLength;
    input.addEventListener("change", () => onChange(input.value));
    label.append(input);
    return label;
  }

  function updateClip(id, changes) {
    acceptTimelineEdit(() => {
      const found = findClip(id);
      if (found) Object.assign(found.clip, changes(found.clip));
    });
  }

  function labelChanges(clip, value) {
    return { label: value.trim() || clip.label };
  }

  function startChanges(_clip, value) {
    return { start_ms: Math.max(0, Math.round(value)) };
  }

  function silenceDurationChanges(_clip, value) {
    return { duration_ms: Math.max(1, Math.round(value)) };
  }

  function sourceInChanges(clip, value) {
    return {
      start_ms: clip.start_ms + (value - clip.source_in_ms),
      source_in_ms: value,
      duration_ms: clip.source_out_ms - value,
    };
  }

  function sourceOutChanges(clip, value) {
    return { source_out_ms: value, duration_ms: value - clip.source_in_ms };
  }

  function renderSelection() {
    selectionBody.replaceChildren();
    const selected = selectedClips();
    if (!selected.length) {
      const message = document.createElement("p");
      message.className = "selection-empty";
      message.textContent = "Select a clip. Hold Shift to select more than one.";
      selectionBody.append(message);
      if (panelPosition) clampPanelPosition();
      return;
    }
    if (selected.length > 1) {
      const count = document.createElement("p");
      count.className = "selection-empty";
      count.textContent = `${selected.length} clips selected`;
      const list = document.createElement("ol");
      list.className = "selection-summary";
      for (const { track, clip } of selected) {
        const item = document.createElement("li");
        const title = document.createElement("strong");
        title.textContent = clip.label;
        const detail = document.createElement("span");
        detail.textContent = `${track.name} · ${clip.start_ms} ms · ${clip.duration_ms} ms · ${clipStatus(clip)}`;
        item.append(title, detail);
        list.append(item);
      }
      selectionBody.append(count, list);
      if (panelPosition) clampPanelPosition();
      return;
    }

    const { track, clip } = selected[0];
    selectionBody.append(
      selectionField("Label", clip.label, (value) => updateClip(clip.id, (current) => labelChanges(current, value)), { maxLength: 120 }),
      selectionField("Track", track.name),
      selectionField("Starts at (ms)", clip.start_ms, (value) => {
        const next = Number(value);
        if (Number.isFinite(next)) updateClip(clip.id, (current) => startChanges(current, next));
      }, { type: "number", min: 0, step: 1 }),
    );
    if (clip.source_id) {
      selectionBody.append(
        selectionField("Duration (ms)", clip.duration_ms),
        selectionField("Source in (ms)", clip.source_in_ms, (value) => {
          const next = Math.round(Number(value));
          if (!Number.isFinite(next)) return;
          updateClip(clip.id, (current) => sourceInChanges(current, next));
        }, { type: "number", min: 0, step: 1 }),
        selectionField("Source out (ms)", clip.source_out_ms, (value) => {
          const next = Math.round(Number(value));
          if (!Number.isFinite(next)) return;
          updateClip(clip.id, (current) => sourceOutChanges(current, next));
        }, { type: "number", min: 1, step: 1 }),
      );
    } else {
      selectionBody.append(selectionField("Duration (ms)", clip.duration_ms, (value) => {
        const next = Number(value);
        if (Number.isFinite(next)) updateClip(clip.id, (current) => silenceDurationChanges(current, next));
      }, { type: "number", min: 1, step: 1 }));
    }
    selectionBody.append(selectionField("Status", clipStatus(clip)));
    if (panelPosition) clampPanelPosition();
  }

  function renderClipBlock(clip, durationMS) {
    const block = document.createElement("div");
    block.id = `timeline-clip-${clip.id}`;
    block.className = `timeline-clip clip-${clip.type}`;
    if (clip.type === "silence") block.classList.add("silence-block");
    block.dataset.clipId = clip.id;
    block.tabIndex = 0;
    block.setAttribute("role", "button");
    block.setAttribute("aria-pressed", String(selectedClipIDs.has(clip.id)));
    const label = document.createElement("span");
    label.className = "clip-label";
    label.textContent = clip.label;
    const startHandle = document.createElement("span");
    startHandle.className = "trim-handle";
    startHandle.dataset.trimEdge = "start";
    startHandle.setAttribute("aria-hidden", "true");
    const endHandle = document.createElement("span");
    endHandle.className = "trim-handle";
    endHandle.dataset.trimEdge = "end";
    endHandle.setAttribute("aria-hidden", "true");
    block.append(startHandle, label, endHandle);
    block.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        selectClip(clip.id, event.shiftKey);
      }
    });
    block.addEventListener("pointerdown", (event) => {
      const edge = event.target instanceof HTMLElement ? event.target.dataset.trimEdge : "";
      beginClipPointerEdit(event, clip.id, edge || "move");
    });
    positionClipBlockOnElement(block, clip, durationMS);
    return block;
  }

  function positionClipBlockOnElement(block, clip, durationMS) {
    block.style.left = `${(clip.start_ms / durationMS) * 100}%`;
    block.style.width = `${Math.max((clip.duration_ms / durationMS) * 100, 0.3)}%`;
    block.setAttribute("aria-label", `${clip.label}, ${clip.type} from ${clip.start_ms} milliseconds for ${clip.duration_ms} milliseconds`);
  }

  function renderClipEditor(track, clip) {
    const editorElement = document.createElement("fieldset");
    editorElement.className = `clip-editor ${clip.source_id ? "audio-clip" : "silence-clip"}`;
    editorElement.id = `clip-editor-${clip.id}`;
    const legend = document.createElement("legend");
    legend.textContent = clip.source_id ? "Audio clip" : "Silence";

    const label = document.createElement("label");
    label.textContent = "Label";
    const labelInput = document.createElement("input");
    labelInput.value = clip.label;
    labelInput.maxLength = 120;
    labelInput.setAttribute("aria-label", `${legend.textContent} label`);
    labelInput.addEventListener("change", () => updateClip(clip.id, (current) => labelChanges(current, labelInput.value)));
    label.append(labelInput);

    const start = document.createElement("label");
    start.textContent = "Starts at (ms)";
    const startInput = document.createElement("input");
    startInput.type = "number";
    startInput.min = "0";
    startInput.step = "1";
    startInput.value = String(clip.start_ms);
    startInput.addEventListener("change", () => {
      const next = Number(startInput.value);
      if (Number.isFinite(next)) updateClip(clip.id, (current) => startChanges(current, next));
    });
    start.append(startInput);

    const duration = document.createElement("label");
    duration.textContent = clip.source_id ? "Source in (ms)" : "Duration (ms)";
    const durationInput = document.createElement("input");
    durationInput.type = "number";
    durationInput.min = "0";
    durationInput.step = "1";
    durationInput.value = String(clip.source_id ? clip.source_in_ms : clip.duration_ms);
    durationInput.addEventListener("change", () => {
      const next = Math.round(Number(durationInput.value));
      if (!Number.isFinite(next)) return;
      if (clip.source_id) {
        updateClip(clip.id, (current) => sourceInChanges(current, next));
      } else {
        updateClip(clip.id, (current) => silenceDurationChanges(current, next));
      }
    });
    duration.append(durationInput);

    const fields = [legend, label, start, duration];
    if (clip.source_id) {
      const sourceOut = document.createElement("label");
      sourceOut.textContent = "Source out (ms)";
      const sourceOutInput = document.createElement("input");
      sourceOutInput.type = "number";
      sourceOutInput.min = "1";
      sourceOutInput.step = "1";
      sourceOutInput.value = String(clip.source_out_ms);
      sourceOutInput.addEventListener("change", () => {
        const next = Math.round(Number(sourceOutInput.value));
        if (Number.isFinite(next)) updateClip(clip.id, (current) => sourceOutChanges(current, next));
      });
      sourceOut.append(sourceOutInput);
      fields.push(sourceOut);
    }

    const remove = actionButton("Remove", `Remove ${clip.label}`, () => {
      acceptTimelineEdit(() => {
        track.clips = track.clips.filter((item) => item.id !== clip.id);
        selectedClipIDs.delete(clip.id);
      });
    });
    remove.classList.add("danger-button");
    fields.push(remove);
    editorElement.append(...fields);
    return editorElement;
  }

  function renderTracks() {
    tracksElement.replaceChildren();
    const project = currentProject();
    if (!project) {
      renderSelection();
      return;
    }
    normalizeTracks(project);
    pruneSelection();
    const timelineDuration = timelineDurationMS(project);
    timelineDurationInput.value = String(timelineDuration / 1000);
    renderTimelineRuler(timelineDuration);
    updateTimelineWidth(timelineDuration);
    updateHistoryButtons();
    if (!project.tracks.length) {
      const empty = document.createElement("div");
      empty.className = "tracks-empty";
      empty.textContent = "Add a Dialogue, SFX, or Music track to begin.";
      tracksElement.append(empty);
      renderSelection();
      return;
    }

    project.tracks.forEach((track, index) => {
      const row = document.createElement("article");
      row.className = `track-row track-${track.type}`;
      row.dataset.trackId = track.id;
      const header = document.createElement("header");
      header.className = "track-header";
      const identity = document.createElement("div");
      identity.className = "track-identity";
      const type = document.createElement("span");
      type.className = "track-type";
      type.textContent = track.type === "sfx" ? "SFX" : track.type[0].toUpperCase() + track.type.slice(1);
      const name = document.createElement("input");
      name.value = track.name;
      name.maxLength = 120;
      name.setAttribute("aria-label", `${type.textContent} track name`);
      name.addEventListener("input", () => {
        track.name = name.value;
        scheduleAutosave();
        renderSelection();
      });
      identity.append(type, name);

      const controls = document.createElement("div");
      controls.className = "track-controls";
      controls.append(
        actionButton("↑", `Move ${track.name} up`, () => moveTrack(index, -1), index === 0),
        actionButton("↓", `Move ${track.name} down`, () => moveTrack(index, 1), index === project.tracks.length - 1),
        actionButton(track.muted ? "Unmute" : "Mute", `${track.muted ? "Unmute" : "Mute"} ${track.name}`, () => acceptTimelineEdit(() => { track.muted = !track.muted; })),
        actionButton("Remove", `Remove ${track.name} track`, () => removeTrack(index)),
      );
      header.append(identity, controls);

      const lane = document.createElement("div");
      lane.className = "track-lane";
      lane.setAttribute("aria-label", `${track.name} timeline`);
      const timeline = document.createElement("div");
      timeline.className = "timeline-stage";
      if (!track.clips.length) {
        const hint = document.createElement("span");
        hint.className = "lane-hint";
        hint.textContent = track.type === "dialogue" ? "Unbound dialogue track" : "Empty track";
        timeline.append(hint);
      }
      track.clips.forEach((clip) => timeline.append(renderClipBlock(clip, timelineDuration)));
      const editors = document.createElement("div");
      editors.className = "clip-editors";
      track.clips.forEach((clip) => editors.append(renderClipEditor(track, clip)));
      const addSilence = actionButton("+ Silence", `Add silence to ${track.name}`, () => addSilenceClip(track));
      addSilence.classList.add("add-silence");
      lane.append(timeline, editors, addSilence);
      row.append(header, lane);
      tracksElement.append(row);
    });
    refreshSelectionUI();
  }

  function snappedTime(value) {
    const rounded = Math.round(value);
    return snapInput.checked ? Math.round(rounded / snapIntervalMS) * snapIntervalMS : rounded;
  }

  function beginClipPointerEdit(event, clipID, mode) {
    if (event.button !== 0) return;
    event.preventDefault();
    const block = event.currentTarget;
    block.focus({ preventScroll: true });
    if (mode === "move") {
      if (event.shiftKey) {
        const wasSelected = selectedClipIDs.has(clipID);
        selectClip(clipID, true);
        if (wasSelected) return;
      } else if (!selectedClipIDs.has(clipID)) {
        selectClip(clipID);
      }
    } else {
      selectClip(clipID);
    }
    const selected = mode === "move" ? selectedClips() : [findClip(clipID)].filter(Boolean);
    const stage = block.closest(".timeline-stage");
    if (!stage || !selected.length) return;
    clipPointerEdit = {
      pointerID: event.pointerId,
      startX: event.clientX,
      lastX: event.clientX,
      mode,
      primaryID: clipID,
      durationMS: timelineDurationMS(currentProject()),
      stageWidth: stage.getBoundingClientRect().width,
      originals: new Map(selected.map(({ clip }) => [clip.id, clone(clip)])),
    };
    window.addEventListener("pointermove", moveClipPointerEdit);
    window.addEventListener("pointerup", finishClipPointerEdit);
    window.addEventListener("pointercancel", cancelClipPointerEdit);
  }

  function moveClipPointerEdit(event) {
    if (!clipPointerEdit || event.pointerId !== clipPointerEdit.pointerID) return;
    clipPointerEdit.lastX = event.clientX;
    if (clipPointerEdit.mode !== "move") return;
    const deltaX = event.clientX - clipPointerEdit.startX;
    clipPointerEdit.originals.forEach((_, id) => {
      const block = byID(`timeline-clip-${id}`);
      if (block) block.style.transform = `translateX(${deltaX}px)`;
    });
  }

  function clearClipPointerListeners() {
    window.removeEventListener("pointermove", moveClipPointerEdit);
    window.removeEventListener("pointerup", finishClipPointerEdit);
    window.removeEventListener("pointercancel", cancelClipPointerEdit);
    document.querySelectorAll(".timeline-clip").forEach((block) => { block.style.transform = ""; });
  }

  function cancelClipPointerEdit(event) {
    if (!clipPointerEdit || event.pointerId !== clipPointerEdit.pointerID) return;
    clearClipPointerListeners();
    clipPointerEdit = null;
  }

  function finishClipPointerEdit(event) {
    if (!clipPointerEdit || event.pointerId !== clipPointerEdit.pointerID) return;
    const edit = clipPointerEdit;
    const deltaX = edit.lastX - edit.startX;
    clearClipPointerListeners();
    clipPointerEdit = null;
    if (Math.abs(deltaX) < 2) return;
    const rawDeltaMS = Math.round((deltaX / edit.stageWidth) * edit.durationMS);
    const primary = edit.originals.get(edit.primaryID);
    if (!primary) return;

    if (edit.mode === "move") {
      let deltaMS = snappedTime(primary.start_ms + rawDeltaMS) - primary.start_ms;
      const earliestStart = Math.min(...[...edit.originals.values()].map((clip) => clip.start_ms));
      deltaMS = Math.max(deltaMS, -earliestStart);
      acceptTimelineEdit(() => {
        edit.originals.forEach((original, id) => {
          const found = findClip(id);
          if (found) found.clip.start_ms = original.start_ms + deltaMS;
        });
      });
      return;
    }

    if (edit.mode === "start") {
      let deltaMS = snappedTime(primary.start_ms + rawDeltaMS) - primary.start_ms;
      let minimumDelta = -primary.start_ms;
      if (primary.source_id) minimumDelta = Math.max(minimumDelta, -primary.source_in_ms);
      deltaMS = Math.max(minimumDelta, Math.min(deltaMS, primary.duration_ms - 1));
      acceptTimelineEdit(() => {
        const found = findClip(edit.primaryID);
        if (!found) return;
        found.clip.start_ms = primary.start_ms + deltaMS;
        found.clip.duration_ms = primary.duration_ms - deltaMS;
        if (primary.source_id) found.clip.source_in_ms = primary.source_in_ms + deltaMS;
      });
      return;
    }

    let durationMS = snappedTime(primary.start_ms + primary.duration_ms + rawDeltaMS) - primary.start_ms;
    durationMS = Math.max(1, durationMS);
    if (primary.source_id) durationMS = Math.min(durationMS, primary.source_duration_ms - primary.source_in_ms);
    acceptTimelineEdit(() => {
      const found = findClip(edit.primaryID);
      if (!found) return;
      found.clip.duration_ms = durationMS;
      if (primary.source_id) found.clip.source_out_ms = primary.source_in_ms + durationMS;
    });
  }

  function showCurrent(project, preserveInput = false) {
    currentID = project ? project.id : "";
    editor.hidden = !project;
    emptyState.hidden = Boolean(project);
    undoStack = [];
    redoStack = [];
    selectedClipIDs = new Set();
    if (project) {
      normalizeTracks(project);
      if (!preserveInput) nameInput.value = project.name;
      setStatus("saved");
    }
    renderProjects();
    renderTracks();
    if (project && !panelPosition) {
      const heading = document.querySelector(".canvas-heading").getBoundingClientRect();
      clampPanelPosition({ x: window.innerWidth - selectionPanel.offsetWidth - 24, y: heading.bottom + 12 });
    }
  }

  async function refreshProjects() {
    try {
      const body = await request(apiRoot);
      projects = body.projects || [];
      if (requestedProjectID) {
        const id = requestedProjectID;
        requestedProjectID = "";
        await openProject(id);
        return;
      }
      showCurrent(currentProject());
    } catch (error) {
      projectList.textContent = `Could not load projects: ${error.message}`;
    }
  }

  async function openProject(id) {
    window.clearTimeout(autosaveTimer);
    try {
      const project = await request(`${apiRoot}/${encodeURIComponent(id)}`);
      projects = [project, ...projects.filter((item) => item.id !== id)];
      editVersion = 0;
      showCurrent(project);
    } catch (error) {
      setStatus("failed", error.message);
    }
  }

  function addTrack(type) {
    const project = currentProject();
    if (!project) return;
    acceptTimelineEdit(() => {
      const number = project.tracks.filter((track) => track.type === type).length + 1;
      const label = type === "sfx" ? "SFX" : type[0].toUpperCase() + type.slice(1);
      project.tracks.push({ id: mintID("track"), name: `${label} ${number}`, type, order: project.tracks.length, muted: false, clips: [] });
    });
  }

  function moveTrack(index, direction) {
    const project = currentProject();
    const target = index + direction;
    if (!project || target < 0 || target >= project.tracks.length) return;
    acceptTimelineEdit(() => {
      [project.tracks[index], project.tracks[target]] = [project.tracks[target], project.tracks[index]];
    });
  }

  function removeTrack(index) {
    const project = currentProject();
    if (!project) return;
    acceptTimelineEdit(() => {
      project.tracks[index].clips.forEach((clip) => selectedClipIDs.delete(clip.id));
      project.tracks.splice(index, 1);
    });
  }

  function addSilenceClip(track) {
    acceptTimelineEdit(() => {
      const latestEnd = track.clips.reduce((end, clip) => Math.max(end, clip.start_ms + clip.duration_ms), 0);
      const clip = { id: mintID("clip"), type: "silence", label: "Silence", start_ms: snappedTime(latestEnd), duration_ms: 1000 };
      track.clips.push(clip);
      selectedClipIDs = new Set([clip.id]);
    });
  }

  function removeSelectedClips() {
    if (!selectedClipIDs.size) return;
    acceptTimelineEdit((project) => {
      project.tracks.forEach((track) => {
        track.clips = track.clips.filter((clip) => !selectedClipIDs.has(clip.id));
      });
      selectedClipIDs = new Set();
    });
  }

  async function saveProject() {
    window.clearTimeout(autosaveTimer);
    const project = currentProject();
    if (!project) return;
    const requestedName = nameInput.value.trim();
    if (!requestedName) {
      setStatus("failed", "A project name is required");
      return;
    }
    const validationError = timelineError(project);
    if (validationError) {
      setStatus("failed", validationError);
      return;
    }
    if (savePromise) {
      saveAgain = true;
      return savePromise;
    }
    normalizeTracks(project);
    const savingID = project.id;
    const savingVersion = editVersion;
    const payload = {
      name: requestedName,
      revision: project.revision,
      timeline_duration_ms: project.timeline_duration_ms,
      tracks: clone(project.tracks),
    };
    setStatus("saving");
    savePromise = request(`${apiRoot}/${encodeURIComponent(savingID)}`, { method: "PUT", body: JSON.stringify(payload) });
    try {
      const saved = await savePromise;
      const changedDuringSave = editVersion !== savingVersion;
      if (currentID === savingID) {
        project.revision = saved.revision;
        project.updated_at = saved.updated_at;
        project.created_at = saved.created_at;
        project.timeline_duration_ms = saved.timeline_duration_ms;
        project.name = changedDuringSave ? project.name : saved.name;
        projects = [project, ...projects.filter((item) => item.id !== project.id)];
        if (changedDuringSave) saveAgain = true;
      } else {
        projects = [saved, ...projects.filter((item) => item.id !== saved.id)];
      }
      if (!changedDuringSave) setStatus("saved");
      renderProjects();
    } catch (error) {
      const detail = error.status === 409 ? "another edit changed this project; reopen it before saving" : error.message;
      setStatus("failed", detail);
    } finally {
      savePromise = null;
      if (saveAgain && currentID === savingID) {
        saveAgain = false;
        await saveProject();
      }
    }
  }

  function clampPanelPosition(position = panelPosition) {
    if (!position) return;
    const width = selectionPanel.offsetWidth;
    const height = selectionPanel.offsetHeight;
    panelPosition = {
      x: Math.max(8, Math.min(position.x, window.innerWidth - width - 8)),
      y: Math.max(8, Math.min(position.y, window.innerHeight - height - 8)),
    };
    selectionPanel.style.left = `${panelPosition.x}px`;
    selectionPanel.style.top = `${panelPosition.y}px`;
    selectionPanel.style.right = "auto";
  }

  function beginPanelPointerEdit(event) {
    if (event.button !== 0) return;
    event.preventDefault();
    const rect = selectionPanel.getBoundingClientRect();
    panelPointerEdit = { pointerID: event.pointerId, startX: event.clientX, startY: event.clientY, x: rect.left, y: rect.top };
    window.addEventListener("pointermove", movePanelPointerEdit);
    window.addEventListener("pointerup", finishPanelPointerEdit);
    window.addEventListener("pointercancel", finishPanelPointerEdit);
  }

  function movePanelPointerEdit(event) {
    if (!panelPointerEdit || event.pointerId !== panelPointerEdit.pointerID) return;
    clampPanelPosition({
      x: panelPointerEdit.x + event.clientX - panelPointerEdit.startX,
      y: panelPointerEdit.y + event.clientY - panelPointerEdit.startY,
    });
  }

  function finishPanelPointerEdit(event) {
    if (!panelPointerEdit || event.pointerId !== panelPointerEdit.pointerID) return;
    window.removeEventListener("pointermove", movePanelPointerEdit);
    window.removeEventListener("pointerup", finishPanelPointerEdit);
    window.removeEventListener("pointercancel", finishPanelPointerEdit);
    panelPointerEdit = null;
  }

  function isEditableTarget(target) {
    return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || target.isContentEditable;
  }

  newForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const name = newNameInput.value.trim();
    if (!name) return;
    try {
      const project = await request(apiRoot, { method: "POST", body: JSON.stringify({ name }) });
      projects = [project, ...projects];
      newNameInput.value = "";
      editVersion = 0;
      showCurrent(project);
      nameInput.focus();
    } catch (error) {
      projectList.textContent = `Could not create project: ${error.message}`;
    }
  });

  nameInput.addEventListener("input", () => {
    const project = currentProject();
    if (!project) return;
    project.name = nameInput.value;
    renderProjects();
    scheduleAutosave();
  });
  byID("storyBuilderAddDialogue").addEventListener("click", () => addTrack("dialogue"));
  byID("storyBuilderAddSFX").addEventListener("click", () => addTrack("sfx"));
  byID("storyBuilderAddMusic").addEventListener("click", () => addTrack("music"));
  undoButton.addEventListener("click", () => restoreTimeline(undoStack, redoStack));
  redoButton.addEventListener("click", () => restoreTimeline(redoStack, undoStack));
  snapInput.addEventListener("change", () => refreshSelectionUI());
  timelineDurationInput.addEventListener("change", () => {
    const seconds = Number(timelineDurationInput.value);
    if (!Number.isFinite(seconds)) {
      renderTracks();
      return;
    }
    acceptTimelineEdit((project) => {
      project.timeline_duration_ms = Math.max(1, Math.round(seconds * 1000));
    });
  });
  zoomInput.addEventListener("input", () => {
    const scrollRatio = timelineViewport.scrollWidth > timelineViewport.clientWidth
      ? timelineViewport.scrollLeft / (timelineViewport.scrollWidth - timelineViewport.clientWidth)
      : 0;
    renderTracks();
    timelineViewport.scrollLeft = scrollRatio * (timelineViewport.scrollWidth - timelineViewport.clientWidth);
  });
  selectionHandle.addEventListener("pointerdown", beginPanelPointerEdit);
  saveButton.addEventListener("click", () => saveProject());
  refreshButton.addEventListener("click", () => refreshProjects());
  deleteButton.addEventListener("click", async () => {
    const project = currentProject();
    if (!project || !window.confirm(`Delete “${project.name}”?`)) return;
    try {
      await request(`${apiRoot}/${encodeURIComponent(project.id)}`, { method: "DELETE" });
      projects = projects.filter((item) => item.id !== project.id);
      showCurrent(null);
    } catch (error) {
      setStatus("failed", error.message);
    }
  });
  document.addEventListener("keydown", (event) => {
    if (isEditableTarget(event.target)) return;
    const command = event.ctrlKey || event.metaKey;
    if (command && event.key.toLowerCase() === "s") {
      event.preventDefault();
      saveProject();
    } else if (command && event.key.toLowerCase() === "z") {
      event.preventDefault();
      restoreTimeline(event.shiftKey ? redoStack : undoStack, event.shiftKey ? undoStack : redoStack);
    } else if (command && event.key.toLowerCase() === "y") {
      event.preventDefault();
      restoreTimeline(redoStack, undoStack);
    } else if (event.key === "Delete" || event.key === "Backspace") {
      event.preventDefault();
      removeSelectedClips();
    }
  });
  window.addEventListener("resize", () => {
    if (panelPosition) clampPanelPosition();
    const project = currentProject();
    if (project) updateTimelineWidth(timelineDurationMS(project));
  });

  renderSelection();
  refreshProjects();
})();
