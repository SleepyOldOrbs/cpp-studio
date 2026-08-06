(() => {
  "use strict";

  const apiRoot = "/v1/story-builder-projects";
  const newForm = document.getElementById("storyBuilderNewForm");
  const newNameInput = document.getElementById("storyBuilderNewName");
  const projectList = document.getElementById("storyBuilderProjectList");
  const editor = document.getElementById("storyBuilderEditor");
  const emptyState = document.getElementById("storyBuilderEmpty");
  const nameInput = document.getElementById("storyBuilderNameInput");
  const saveStatus = document.getElementById("storyBuilderSaveStatus");
  const saveButton = document.getElementById("storyBuilderSaveButton");
  const deleteButton = document.getElementById("storyBuilderDeleteButton");
  const refreshButton = document.getElementById("storyBuilderRefreshButton");

  let projects = [];
  let currentID = "";
  let autosaveTimer = 0;
  let savePromise = null;
  let saveAgain = false;

  async function request(path, options = {}) {
    const response = await fetch(path, {
      ...options,
      headers: options.body ? { "Content-Type": "application/json" } : undefined,
    });
    if (response.ok) {
      return response.status === 204 ? null : response.json();
    }
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

  function setStatus(state, detail = "") {
    const labels = {
      dirty: "Unsaved changes",
      saving: "Saving…",
      saved: "Saved",
      failed: "Save failed",
    };
    saveStatus.dataset.state = state;
    saveStatus.textContent = detail ? `${labels[state]} — ${detail}` : labels[state];
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

  function showCurrent(project, preserveInput = false) {
    currentID = project ? project.id : "";
    editor.hidden = !project;
    emptyState.hidden = Boolean(project);
    if (project && !preserveInput) nameInput.value = project.name;
    if (project) setStatus("saved");
    renderProjects();
  }

  async function refreshProjects() {
    try {
      const body = await request(apiRoot);
      projects = body.projects || [];
      const selected = currentProject();
      showCurrent(selected);
    } catch (error) {
      projectList.innerHTML = `<p class="project-list-empty">Could not load projects: ${escapeHTML(error.message)}</p>`;
    }
  }

  async function openProject(id) {
    window.clearTimeout(autosaveTimer);
    try {
      const project = await request(`${apiRoot}/${encodeURIComponent(id)}`);
      projects = [project, ...projects.filter((item) => item.id !== id)];
      showCurrent(project);
    } catch (error) {
      setStatus("failed", error.message);
    }
  }

  function scheduleAutosave() {
    setStatus("dirty");
    window.clearTimeout(autosaveTimer);
    autosaveTimer = window.setTimeout(() => saveProject(), 600);
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

    const savingID = project.id;
    const savingRevision = project.revision;
    setStatus("saving");
    savePromise = request(`${apiRoot}/${encodeURIComponent(savingID)}`, {
      method: "PUT",
      body: JSON.stringify({ name: requestedName, revision: savingRevision }),
    });
    try {
      const saved = await savePromise;
      projects = [saved, ...projects.filter((item) => item.id !== saved.id)];
      if (currentID === saved.id) {
        const changedDuringSave = nameInput.value.trim() !== requestedName;
        showCurrent(saved, changedDuringSave);
        if (changedDuringSave) saveAgain = true;
      } else {
        renderProjects();
      }
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

  function escapeHTML(value) {
    return value.replace(/[&<>"']/g, (character) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      "\"": "&quot;",
      "'": "&#39;",
    })[character]);
  }

  newForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const name = newNameInput.value.trim();
    if (!name) return;
    try {
      const project = await request(apiRoot, {
        method: "POST",
        body: JSON.stringify({ name }),
      });
      projects = [project, ...projects];
      newNameInput.value = "";
      showCurrent(project);
      nameInput.focus();
    } catch (error) {
      projectList.innerHTML = `<p class="project-list-empty">Could not create project: ${escapeHTML(error.message)}</p>`;
    }
  });

  nameInput.addEventListener("input", () => {
    const project = currentProject();
    if (!project) return;
    project.name = nameInput.value;
    renderProjects();
    scheduleAutosave();
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
