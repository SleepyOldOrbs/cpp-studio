(() => {
  "use strict";

  const apiRoot = "/v1/story-builder-projects";
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

  let projects = [];
  let currentID = "";
  let autosaveTimer = 0;
  let savePromise = null;
  let saveAgain = false;
  let editVersion = 0;
  let removedTracks = [];
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
  }

  function timelineDurationMS(project) {
    const latestEnd = project.tracks.reduce((projectEnd, track) => track.clips.reduce(
      (trackEnd, clip) => Math.max(trackEnd, clip.start_ms + clip.duration_ms),
      projectEnd,
    ), 0);
    return Math.max(30000, Math.ceil(latestEnd / 30000) * 30000);
  }

  function renderTimelineRuler(durationMS) {
    const ruler = document.querySelector(".timeline-ruler");
    ruler.replaceChildren();
    for (let index = 0; index <= 6; index += 1) {
      const tick = document.createElement("span");
      tick.textContent = `${(durationMS / 6 / 1000) * index}s`;
      ruler.append(tick);
    }
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

  function updateTrack(track, changes, rerender = false) {
    Object.assign(track, changes);
    scheduleAutosave();
    if (rerender) renderTracks();
  }

  function renderSilenceClip(track, clip) {
    const clipElement = document.createElement("fieldset");
    clipElement.className = "silence-clip";
    clipElement.id = `silence-editor-${clip.id}`;
    const legend = document.createElement("legend");
    legend.textContent = "Silence";
    const label = document.createElement("label");
    label.textContent = "Label";
    const labelInput = document.createElement("input");
    labelInput.value = clip.label;
    labelInput.maxLength = 120;
    labelInput.setAttribute("aria-label", "Silence label");
    labelInput.addEventListener("input", () => {
      clip.label = labelInput.value;
      const block = byID(`silence-block-${clip.id}`);
      if (block) block.textContent = clip.label || "Silence";
      positionSilenceBlock(clip, timelineDurationMS(currentProject()));
      scheduleAutosave();
    });
    label.append(labelInput);

    const start = document.createElement("label");
    start.textContent = "Starts at (seconds)";
    const startInput = document.createElement("input");
    startInput.type = "number";
    startInput.min = "0";
    startInput.step = "0.1";
    startInput.value = String(clip.start_ms / 1000);
    startInput.addEventListener("change", () => {
      const seconds = Number(startInput.value);
      if (!Number.isFinite(seconds)) {
        startInput.value = String(clip.start_ms / 1000);
        return;
      }
      clip.start_ms = Math.max(0, Math.round(seconds * 1000));
      startInput.value = String(clip.start_ms / 1000);
      scheduleAutosave();
      renderTracks();
    });
    start.append(startInput);

    const duration = document.createElement("label");
    duration.textContent = "Duration (seconds)";
    const durationInput = document.createElement("input");
    durationInput.type = "number";
    durationInput.min = "0.1";
    durationInput.step = "0.1";
    durationInput.value = String(clip.duration_ms / 1000);
    durationInput.addEventListener("change", () => {
      const seconds = Number(durationInput.value);
      if (!Number.isFinite(seconds)) {
        durationInput.value = String(clip.duration_ms / 1000);
        return;
      }
      clip.duration_ms = Math.max(100, Math.round(seconds * 1000));
      durationInput.value = String(clip.duration_ms / 1000);
      scheduleAutosave();
      renderTracks();
    });
    duration.append(durationInput);

    const remove = actionButton("Remove", `Remove silence ${clip.label}`, () => {
      track.clips = track.clips.filter((item) => item.id !== clip.id);
      scheduleAutosave();
      renderTracks();
    });
    remove.classList.add("danger-button");
    clipElement.append(legend, label, start, duration, remove);
    return clipElement;
  }

  function positionSilenceBlock(clip, durationMS) {
    const block = byID(`silence-block-${clip.id}`);
    if (!block) return;
    block.style.left = `${(clip.start_ms / durationMS) * 100}%`;
    block.style.width = `${Math.max((clip.duration_ms / durationMS) * 100, 0.75)}%`;
    block.setAttribute("aria-label", `${clip.label || "Silence"}, silence from ${clip.start_ms / 1000} seconds for ${clip.duration_ms / 1000} seconds`);
  }

  function renderSilenceBlock(clip, durationMS) {
    const block = document.createElement("button");
    block.type = "button";
    block.id = `silence-block-${clip.id}`;
    block.className = "silence-block";
    block.textContent = clip.label;
    block.setAttribute("aria-label", `${clip.label}, silence from ${clip.start_ms / 1000} seconds for ${clip.duration_ms / 1000} seconds`);
    block.addEventListener("click", () => byID(`silence-editor-${clip.id}`).querySelector("input").focus());
    block.style.left = `${(clip.start_ms / durationMS) * 100}%`;
    block.style.width = `${Math.max((clip.duration_ms / durationMS) * 100, 0.75)}%`;
    return block;
  }

  function renderTracks() {
    tracksElement.replaceChildren();
    const project = currentProject();
    if (!project) return;
    normalizeTracks(project);
    const timelineDuration = timelineDurationMS(project);
    renderTimelineRuler(timelineDuration);
    undoButton.disabled = removedTracks.length === 0;
    if (!project.tracks.length) {
      const empty = document.createElement("div");
      empty.className = "tracks-empty";
      empty.textContent = "Add a Dialogue, SFX, or Music track to begin.";
      tracksElement.append(empty);
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
      });
      identity.append(type, name);

      const controls = document.createElement("div");
      controls.className = "track-controls";
      controls.append(
        actionButton("↑", `Move ${track.name} up`, () => moveTrack(index, -1), index === 0),
        actionButton("↓", `Move ${track.name} down`, () => moveTrack(index, 1), index === project.tracks.length - 1),
        actionButton(track.muted ? "Unmute" : "Mute", `${track.muted ? "Unmute" : "Mute"} ${track.name}`, () => updateTrack(track, { muted: !track.muted }, true)),
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
      track.clips.forEach((clip) => timeline.append(renderSilenceBlock(clip, timelineDuration)));
      const editors = document.createElement("div");
      editors.className = "clip-editors";
      track.clips.forEach((clip) => editors.append(renderSilenceClip(track, clip)));
      const addSilence = actionButton("+ Silence", `Add silence to ${track.name}`, () => addSilenceClip(track));
      addSilence.classList.add("add-silence");
      lane.append(timeline, editors, addSilence);
      row.append(header, lane);
      tracksElement.append(row);
    });
  }

  function showCurrent(project, preserveInput = false) {
    currentID = project ? project.id : "";
    editor.hidden = !project;
    emptyState.hidden = Boolean(project);
    removedTracks = [];
    if (project) {
      normalizeTracks(project);
      if (!preserveInput) nameInput.value = project.name;
      setStatus("saved");
    }
    renderProjects();
    renderTracks();
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
    const number = project.tracks.filter((track) => track.type === type).length + 1;
    const label = type === "sfx" ? "SFX" : type[0].toUpperCase() + type.slice(1);
    project.tracks.push({ id: mintID("track"), name: `${label} ${number}`, type, order: project.tracks.length, muted: false, clips: [] });
    scheduleAutosave();
    renderTracks();
  }

  function moveTrack(index, direction) {
    const project = currentProject();
    const target = index + direction;
    if (!project || target < 0 || target >= project.tracks.length) return;
    [project.tracks[index], project.tracks[target]] = [project.tracks[target], project.tracks[index]];
    normalizeTracks(project);
    scheduleAutosave();
    renderTracks();
  }

  function removeTrack(index) {
    const project = currentProject();
    if (!project) return;
    removedTracks.push({ index, track: clone(project.tracks[index]) });
    project.tracks.splice(index, 1);
    normalizeTracks(project);
    scheduleAutosave();
    renderTracks();
  }

  function addSilenceClip(track) {
    track.clips.push({ id: mintID("clip"), type: "silence", label: "Silence", start_ms: 0, duration_ms: 1000 });
    scheduleAutosave();
    renderTracks();
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
    if (savePromise) {
      saveAgain = true;
      return savePromise;
    }
    normalizeTracks(project);
    const savingID = project.id;
    const savingVersion = editVersion;
    const payload = { name: requestedName, revision: project.revision, tracks: clone(project.tracks) };
    setStatus("saving");
    savePromise = request(`${apiRoot}/${encodeURIComponent(savingID)}`, { method: "PUT", body: JSON.stringify(payload) });
    try {
      const saved = await savePromise;
      const changedDuringSave = editVersion !== savingVersion;
      if (currentID === savingID) {
        project.revision = saved.revision;
        project.updated_at = saved.updated_at;
        project.created_at = saved.created_at;
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
  undoButton.addEventListener("click", () => {
    const project = currentProject();
    const removed = removedTracks.pop();
    if (!project || !removed) return;
    project.tracks.splice(Math.min(removed.index, project.tracks.length), 0, removed.track);
    normalizeTracks(project);
    scheduleAutosave();
    renderTracks();
  });
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

  refreshProjects();
})();
