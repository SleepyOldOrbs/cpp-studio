(function () {
  "use strict";

  var shell = document.querySelector(".app-shell");
  var healthButton = document.getElementById("healthButton");
  var gatewayStatus = document.getElementById("gatewayStatus");
  var healthUpdated = document.getElementById("healthUpdated");
  var healthBody = document.getElementById("healthBody");
  var engineRack = document.getElementById("engineRack");
  var voiceForm = document.getElementById("voiceForm");
  var messageInput = document.getElementById("messageInput");
  var recordButton = document.getElementById("recordButton");
  var liveButton = document.getElementById("liveButton");
  var wavInput = document.getElementById("wavInput");
  var wavStatus = document.getElementById("wavStatus");
  var wavSaveButton = document.getElementById("wavSaveButton");
  var wavClearButton = document.getElementById("wavClearButton");
  var clearAllButton = document.getElementById("clearAllButton");
  var runButton = document.getElementById("runButton");
  var clearButton = document.getElementById("clearButton");
  var clearLogButton = document.getElementById("clearLogButton");
  var transcriptOutput = document.getElementById("transcriptOutput");
  var replyOutput = document.getElementById("replyOutput");
  var replyAudio = document.getElementById("replyAudio");
  var saveReplyButton = document.getElementById("saveReplyButton");
  var saveImageButton = document.getElementById("saveImageButton");
  var saveStoryButton = document.getElementById("saveStoryButton");
  var vuLevel = document.getElementById("vuLevel");
  var convoStatus = document.getElementById("convoStatus");
  var convoList = document.getElementById("convoList");
  var newConvoButton = document.getElementById("newConvoButton");
  var errorBox = document.getElementById("errorBox");
  var logOutput = document.getElementById("logOutput");
  var imageForm = document.getElementById("imageForm");
  var imagePromptInput = document.getElementById("imagePromptInput");
  var imageSizeInput = document.getElementById("imageSizeInput");
  var sizePresets = Array.prototype.slice.call(document.querySelectorAll(".preset"));
  var generateImageButton = document.getElementById("generateImageButton");
  var clearImageButton = document.getElementById("clearImageButton");
  var imagePreview = document.getElementById("imagePreview");
  var imagePlaceholder = document.getElementById("imagePlaceholder");
  var imageStatus = document.getElementById("imageStatus");
  var imageErrorBox = document.getElementById("imageErrorBox");
  var imageFileInput = document.getElementById("imageFileInput");
  var describeImageButton = document.getElementById("describeImageButton");
  var describeVoiceLabel = document.getElementById("describeVoiceLabel");
  var imageDescriptionOutput = document.getElementById("imageDescriptionOutput");
  var saveDescriptionButton = document.getElementById("saveDescriptionButton");
  var describeAudio = document.getElementById("describeAudio");
  var storyForm = document.getElementById("storyForm");
  var storySubjectInput = document.getElementById("storySubjectInput");
  var storySecondsInput = document.getElementById("storySecondsInput");
  var storyVoiceSelect = document.getElementById("storyVoiceSelect");
  var castList = document.getElementById("castList");
  var castAddButton = document.getElementById("castAddButton");
  var sourceStack = document.getElementById("sourceStack");
  var sourceAddButton = document.getElementById("sourceAddButton");
  var storyDraftButton = document.getElementById("storyDraftButton");
  var scriptEditor = document.getElementById("scriptEditor");
  var scriptDraftMeta = document.getElementById("scriptDraftMeta");
  var scriptDiscardButton = document.getElementById("scriptDiscardButton");
  var storyTitleInput = document.getElementById("storyTitleInput");
  var scriptLines = document.getElementById("scriptLines");
  var storyGenerateButton = document.getElementById("storyGenerateButton");
  var storyCancelButton = document.getElementById("storyCancelButton");
  var storyErrorBox = document.getElementById("storyErrorBox");
  var storyStatus = document.getElementById("storyStatus");
  var storyProgress = document.getElementById("storyProgress");
  var storyAudio = document.getElementById("storyAudio");
  var storyLibraryButton = document.getElementById("storyLibraryButton");
  var storyLibrary = document.getElementById("storyLibrary");
  var storyFacts = document.getElementById("storyFacts");
  var voiceSelect = document.getElementById("voiceSelect");
  var cloneForm = document.getElementById("cloneForm");
  var cloneNameInput = document.getElementById("cloneNameInput");
  var cloneRecordButton = document.getElementById("cloneRecordButton");
  var cloneWavInput = document.getElementById("cloneWavInput");
  var cloneWavStatus = document.getElementById("cloneWavStatus");
  var cloneWavSaveButton = document.getElementById("cloneWavSaveButton");
  var cloneWavClearButton = document.getElementById("cloneWavClearButton");
  var cloneSaveButton = document.getElementById("cloneSaveButton");
  var cloneErrorBox = document.getElementById("cloneErrorBox");
  var cloneVuLevel = document.getElementById("cloneVuLevel");
  var voiceLibraryButton = document.getElementById("voiceLibraryButton");
  var voiceLibrary = document.getElementById("voiceLibrary");
  var clonePreviewAudio = document.getElementById("clonePreviewAudio");
  var designForm = document.getElementById("designForm");
  var designDescriptionInput = document.getElementById("designDescriptionInput");
  var designModelSelect = document.getElementById("designModelSelect");
  var designGenerateButton = document.getElementById("designGenerateButton");
  var designErrorBox = document.getElementById("designErrorBox");
  var designStatus = document.getElementById("designStatus");
  var designEngineInput = document.getElementById("designEngineInput");
  var saveDesignWavButton = document.getElementById("saveDesignWavButton");
  var designAudio = document.getElementById("designAudio");
  var designNameInput = document.getElementById("designNameInput");
  var designSaveButton = document.getElementById("designSaveButton");
  var cloneSpeakForm = document.getElementById("cloneSpeakForm");
  var speakVoiceLabel = document.getElementById("speakVoiceLabel");
  var speakTextInput = document.getElementById("speakTextInput");
  var speakButton = document.getElementById("speakButton");
  var saveSpeakButton = document.getElementById("saveSpeakButton");
  var speakAudio = document.getElementById("speakAudio");

  var ENGINE_NAMES = ["llama", "whisper", "audio", "sd", "vision", "voicedesign"];
  var HEALTH_POLL_MS = 20000;
  var LIVE_TICK_MS = 2500;
  var LIVE_MAX_SECONDS = 90;
  var LIVE_TARGET_RATE = 16000;
  var LIVE_MAX_FAILURES = 3;
  var CONVO_MAX_TURNS = 40;

  var conversation = [];

  var running = false;
  var recording = false;
  var live = false;
  var liveCapture = null;
  var livePassBusy = false;
  var liveLabelTimer = 0;
  var livePassTimer = 0;
  var liveStartedAt = 0;
  var liveFailures = 0;
  var activeStoryID = "";
  var activeStoryPoll = 0;
  var activeWavFile = null;
  var activeAudioUrl = "";
  var recorder = null;
  var recordSetupPending = false;
  var recordStopRequested = false;
  var recordStartedAt = 0;
  var recordTimer = 0;
  var lastHealthStatus = "";
  var cloneWavFile = null;
  var cloneRecorder = null;
  var cloneRecording = false;
  var cloneSetupPending = false;
  var cloneStopRequested = false;
  var cloneStartedAt = 0;
  var cloneTimer = 0;
  var speakAudioUrl = "";
  var describeAudioUrl = "";
  var designAudioUrl = "";
  var designCandidate = null;
  var libraryVoices = [];
  var storyDraft = null;
  var selectedVoiceId = "";
  try {
    selectedVoiceId = window.localStorage.getItem("cpp-studio-voice") || "";
  } catch (error) {
    selectedVoiceId = "";
  }

  var apiControls = [
    wavInput,
    runButton,
    clearButton,
    messageInput,
    imagePromptInput,
    imageSizeInput,
    generateImageButton,
    clearImageButton,
    storySubjectInput,
    storySecondsInput,
    storyVoiceSelect,
    storyGenerateButton,
    newConvoButton,
    voiceSelect,
    cloneNameInput,
    cloneWavInput,
    cloneSaveButton,
    speakTextInput,
    speakButton,
    clearAllButton,
    imageFileInput,
    designDescriptionInput,
    designModelSelect,
    designGenerateButton,
    designNameInput
  ].concat(sizePresets);

  function log(message, level) {
    var stamp = new Date().toLocaleTimeString();
    var line = document.createElement("span");
    if (level === "error") {
      line.className = "log-error";
    }
    line.textContent = "[" + stamp + "] " + message + "\n";
    logOutput.appendChild(line);
    logOutput.scrollTop = logOutput.scrollHeight;
  }

  function setError(error) {
    var message = error && error.message ? error.message : String(error);
    errorBox.textContent = message;
    errorBox.hidden = false;
    log("Error: " + message, "error");
  }

  function setImageError(error) {
    var message = error && error.message ? error.message : String(error);
    imageErrorBox.textContent = message;
    imageErrorBox.hidden = false;
    log("Error: " + message, "error");
  }

  function setStoryError(error) {
    var message = error && error.message ? error.message : String(error);
    storyErrorBox.textContent = message;
    storyErrorBox.hidden = false;
    log("Error: " + message, "error");
  }

  function clearError() {
    errorBox.textContent = "";
    errorBox.hidden = true;
  }

  function clearImageError() {
    imageErrorBox.textContent = "";
    imageErrorBox.hidden = true;
  }

  function clearStoryError() {
    storyErrorBox.textContent = "";
    storyErrorBox.hidden = true;
  }

  function setBusy(button, busyText) {
    if (!button.dataset.idleText) {
      button.dataset.idleText = button.textContent;
    }
    button.textContent = busyText;
  }

  function clearBusy(button) {
    if (button.dataset.idleText) {
      button.textContent = button.dataset.idleText;
      delete button.dataset.idleText;
    }
  }

  function syncControls() {
    var busy = running || recording || recordSetupPending || live || cloneRecording || cloneSetupPending;
    apiControls.forEach(function (control) {
      control.disabled = busy;
    });
    if (activeStoryID) {
      storyGenerateButton.disabled = true;
    }
    recordButton.disabled = running || live || cloneRecording || cloneSetupPending || (!recording && !recordSetupPending && !canRecord());
    liveButton.disabled = running || recording || recordSetupPending || cloneRecording || cloneSetupPending || (!live && !canRecord());
    cloneRecordButton.disabled = running || live || recording || recordSetupPending || (!cloneRecording && !cloneSetupPending && !canRecord());
    describeImageButton.disabled = busy || imagePreview.hidden || !imagePreview.src;
    designSaveButton.disabled = busy || !designCandidate;
    storyDraftButton.disabled = busy || Boolean(activeStoryID);
  }

  function setRunning(value) {
    running = value;
    shell.setAttribute("aria-busy", value ? "true" : "false");
    syncControls();
  }

  function recordLabel() {
    var elapsed = Math.max(0, (Date.now() - recordStartedAt) / 1000);
    var minutes = Math.floor(elapsed / 60);
    var seconds = Math.floor(elapsed % 60);
    return "Recording " + minutes + ":" + (seconds < 10 ? "0" : "") + seconds + " · release to stop";
  }

  function stopRecordTimer() {
    if (recordTimer) {
      window.clearInterval(recordTimer);
      recordTimer = 0;
    }
  }

  function setRecording(value) {
    recording = value;
    recordButton.classList.toggle("recording", value);
    recordButton.setAttribute("aria-pressed", value ? "true" : "false");
    if (value) {
      recordStartedAt = Date.now();
      recordButton.textContent = recordLabel();
      stopRecordTimer();
      recordTimer = window.setInterval(function () {
        recordButton.textContent = recordLabel();
      }, 250);
    } else {
      stopRecordTimer();
      recordButton.textContent = "Push to record";
    }
    syncControls();
  }

  function setRecordSetupPending(value) {
    recordSetupPending = value;
    if (value) {
      recordButton.textContent = "Preparing...";
    } else if (recording) {
      recordButton.textContent = recordLabel();
    } else {
      recordButton.textContent = "Push to record";
    }
    syncControls();
  }

  function canRecord() {
    return Boolean(navigator.mediaDevices && navigator.mediaDevices.getUserMedia && window.AudioContext);
  }

  function formatBytes(bytes) {
    if (!Number.isFinite(bytes)) {
      return "";
    }
    if (bytes < 1024) {
      return bytes + " B";
    }
    if (bytes < 1024 * 1024) {
      return (bytes / 1024).toFixed(1) + " KB";
    }
    return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  }

  function estimateBase64Bytes(value) {
    if (!value) {
      return 0;
    }
    var padding = value.endsWith("==") ? 2 : value.endsWith("=") ? 1 : 0;
    return Math.max(0, Math.floor(value.length * 3 / 4) - padding);
  }

  function setActiveWav(file, source) {
    activeWavFile = file;
    wavStatus.textContent = source + ": " + file.name + " (" + formatBytes(file.size) + ")";
    wavSaveButton.disabled = false;
    log("WAV ready from " + source + ": " + file.name + ", " + formatBytes(file.size));
  }

  function clearActiveWav() {
    activeWavFile = null;
    wavInput.value = "";
    wavStatus.textContent = "None";
    wavSaveButton.disabled = true;
  }

  // downloadFile saves an in-memory File (a recording or live take) through
  // a temporary object URL.
  function downloadFile(file, fallbackName) {
    var url = URL.createObjectURL(file);
    downloadURL(url, file.name || fallbackName);
    window.setTimeout(function () {
      URL.revokeObjectURL(url);
    }, 10000);
  }

  function clearAudioUrl() {
    if (activeAudioUrl) {
      URL.revokeObjectURL(activeAudioUrl);
      activeAudioUrl = "";
    }
    replyAudio.removeAttribute("src");
    replyAudio.load();
    saveReplyButton.disabled = true;
    libraryReplyButton.disabled = true;
  }

  function resetOutputs() {
    transcriptOutput.value = "";
    replyOutput.value = "";
    clearAudioUrl();
    clearError();
  }

  function clearDescribeAudio() {
    if (describeAudioUrl) {
      URL.revokeObjectURL(describeAudioUrl);
      describeAudioUrl = "";
    }
    describeAudio.removeAttribute("src");
    describeAudio.load();
    saveDescriptionButton.disabled = true;
  }

  function clearImageOutput() {
    imagePreview.removeAttribute("src");
    imagePreview.hidden = true;
    imagePlaceholder.hidden = false;
    imageStatus.textContent = "Idle";
    saveImageButton.disabled = true;
    libraryImageButton.disabled = true;
    imageFileInput.value = "";
    imageDescriptionOutput.value = "";
    clearDescribeAudio();
    clearImageError();
    syncControls();
  }

  function createElement(tag, className, text) {
    var element = document.createElement(tag);
    if (className) {
      element.className = className;
    }
    if (text !== undefined) {
      element.textContent = text;
    }
    return element;
  }

  function renderStatus(status) {
    var normalized = status || "unknown";
    gatewayStatus.textContent = normalized;
    gatewayStatus.className = "status-pill " + normalized;
  }

  function renderEngineRack(engines) {
    engineRack.textContent = "";
    var names = Object.keys(engines || {}).sort();
    if (names.length === 0) {
      names = ENGINE_NAMES;
    }
    names.forEach(function (name) {
      var engine = (engines || {})[name] || {};
      var chip = createElement("span", "led-chip");
      var state = engine.status || "unknown";
      if (engine.ready) {
        state = "ready";
      }
      chip.appendChild(createElement("i", "led " + state));
      chip.appendChild(document.createTextNode(name));
      chip.title = name + ": " + state;
      engineRack.appendChild(chip);
    });
  }

  // DESIGN_MODELS maps designer model choices to the engine that must be
  // configured for them to work; first entry is the preferred default.
  var DESIGN_MODELS = [
    { value: "voxcpm2", engine: "voxcpm2", label: "VoxCPM2 (realistic, 48 kHz)" },
    { value: "omnivoice", engine: "omnivoice", label: "OmniVoice (precision accents)" },
    { value: "qwen3", engine: "voicedesign", label: "Qwen3 (characterful)" }
  ];

  function updateDesignModels(engines) {
    var available = DESIGN_MODELS.filter(function (model) {
      return engines && Object.prototype.hasOwnProperty.call(engines, model.engine);
    });
    if (available.length === 0) {
      available = [DESIGN_MODELS[0]];
    }
    var previous = designModelSelect.value;
    designModelSelect.textContent = "";
    available.forEach(function (model) {
      var option = createElement("option", "", model.label);
      option.value = model.value;
      designModelSelect.appendChild(option);
    });
    designModelSelect.value = previous;
    if (designModelSelect.value !== previous) {
      designModelSelect.value = available[0].value;
    }
  }

  function renderHealth(data) {
    renderStatus(data.status);
    renderEngineRack(data.engines);
    updateDesignModels(data.engines);
    // The Detect speakers button only exists when a diarize engine is
    // configured; without one the Extractor stays manual-tagging only.
    var hasDiarize = Boolean(data.engines && data.engines.diarize);
    extractDiarizeButton.hidden = !hasDiarize;
    document.getElementById("extractSpeakersInput").hidden = !hasDiarize;
    healthUpdated.textContent = data.updatedAt ? "Updated " + new Date(data.updatedAt).toLocaleString() : "Updated now";
    healthBody.textContent = "";

    var engines = data.engines || {};
    var names = Object.keys(engines).sort();
    if (names.length === 0) {
      healthBody.textContent = "No engines reported";
      return;
    }

    names.forEach(function (name) {
      var engine = engines[name] || {};
      var row = createElement("div", "engine-row");
      row.appendChild(createElement("span", "engine-name", name));
      row.appendChild(createElement("span", "engine-status", engine.status || "unknown"));

      var detail = engine.ready ? "ready" : "not ready";
      if (engine.pid) {
        detail += ", pid " + engine.pid;
      }
      if (engine.lastSuccessAt) {
        detail += ", last success " + new Date(engine.lastSuccessAt).toLocaleString();
      }
      if (engine.lastError) {
        detail += ", " + engine.lastError;
      }
      row.appendChild(createElement("span", "engine-detail", detail));
      row.appendChild(buildEngineControls(name, engine));
      healthBody.appendChild(row);
    });
  }

  function buildEngineControls(name, engine) {
    var controls = createElement("span", "engine-controls");
    if (engine.mode !== "server") {
      controls.appendChild(createElement("span", "engine-mode-tag", "per request"));
      return controls;
    }
    var running = engine.status === "ready" || engine.status === "running" || engine.status === "starting";
    var action = running ? "stop" : "start";
    var button = createElement("button", "plain compact-button", running ? "Stop" : "Start");
    button.type = "button";
    button.addEventListener("click", function () {
      controlEngine(name, action, button);
    });
    controls.appendChild(button);
    if (running) {
      var reload = createElement("button", "plain compact-button", "Reload");
      reload.type = "button";
      reload.addEventListener("click", function () {
        controlEngine(name, "reload", reload);
      });
      controls.appendChild(reload);
    }
    return controls;
  }

  var enginesErrorBox = document.getElementById("enginesErrorBox");
  var profilesRow = document.getElementById("profilesRow");
  var profilesButtons = document.getElementById("profilesButtons");
  var gpuMeter = document.getElementById("gpuMeter");

  async function controlEngine(name, action, button) {
    enginesErrorBox.hidden = true;
    button.disabled = true;
    button.textContent = action === "stop" ? "Stopping…" : action === "reload" ? "Reloading…" : "Starting…";
    try {
      var response = await fetch("/v1/engines/" + encodeURIComponent(name) + "/" + action, { method: "POST" });
      if (!response.ok) {
        throw new Error(await readErrorBody(response));
      }
      renderHealth(await response.json());
      log("Engine " + name + ": " + action + " ok");
    } catch (err) {
      enginesErrorBox.textContent = name + " " + action + " failed: " + err.message;
      enginesErrorBox.hidden = false;
      log("Engine " + name + " " + action + " failed: " + err.message, "error");
      refreshHealth(true);
    }
    refreshGPU();
  }

  async function applyProfile(name, button) {
    enginesErrorBox.hidden = true;
    button.disabled = true;
    var original = button.textContent;
    button.textContent = "Applying…";
    try {
      var response = await fetch("/v1/engines/profiles/" + encodeURIComponent(name), { method: "POST" });
      var payload = await response.json();
      if (payload.health) {
        renderHealth(payload.health);
      }
      if (payload.failures && payload.failures.length) {
        enginesErrorBox.textContent = "Profile " + name + ": " + payload.failures.join("; ");
        enginesErrorBox.hidden = false;
        log("Profile " + name + " applied with failures: " + payload.failures.join("; "), "error");
      } else if (!response.ok) {
        throw new Error(payload.error || response.status);
      } else {
        log("Profile " + name + " applied");
      }
    } catch (err) {
      enginesErrorBox.textContent = "Profile " + name + " failed: " + err.message;
      enginesErrorBox.hidden = false;
      log("Profile " + name + " failed: " + err.message, "error");
      refreshHealth(true);
    }
    button.disabled = false;
    button.textContent = original;
    refreshGPU();
  }

  async function loadProfiles() {
    try {
      var response = await fetch("/v1/engines/profiles", { method: "GET" });
      if (!response.ok) {
        return;
      }
      var payload = await response.json();
      var names = Object.keys(payload.profiles || {}).sort();
      if (!names.length) {
        return;
      }
      profilesButtons.textContent = "";
      names.forEach(function (name) {
        var button = createElement("button", "secondary compact-button", name);
        button.type = "button";
        button.addEventListener("click", function () {
          applyProfile(name, button);
        });
        profilesButtons.appendChild(button);
      });
      profilesRow.hidden = false;
    } catch (err) {
      /* profiles are optional */
    }
  }

  async function refreshGPU() {
    try {
      var response = await fetch("/v1/gpu", { method: "GET" });
      if (!response.ok) {
        return;
      }
      var payload = await response.json();
      if (!payload.available || !payload.gpus || !payload.gpus.length) {
        gpuMeter.textContent = "";
        return;
      }
      var gpu = payload.gpus[0];
      gpuMeter.textContent = gpu.name + " · " + (gpu.usedMiB / 1024).toFixed(1) + " / " + (gpu.totalMiB / 1024).toFixed(1) + " GB VRAM";
    } catch (err) {
      /* meter is decorative */
    }
  }

  async function readErrorBody(response) {
    var text = await response.text();
    if (!text) {
      return response.status + " " + response.statusText;
    }
    try {
      var parsed = JSON.parse(text);
      if (parsed.error && typeof parsed.error === "object") {
        return parsed.error.message || parsed.error.code || text;
      }
      return parsed.error || text;
    } catch (error) {
      return text;
    }
  }

  async function ensureOk(response, label) {
    if (response.ok) {
      return;
    }
    var body = await readErrorBody(response);
    throw new Error(label + " failed: HTTP " + response.status + ": " + body);
  }

  async function refreshHealth(silent) {
    healthButton.disabled = true;
    try {
      if (!silent) {
        log("GET /health");
      }
      var response = await fetch("/health", { method: "GET" });
      await ensureOk(response, "Health");
      var data = await response.json();
      renderHealth(data);
      var status = data.status || "unknown";
      if (!silent || status !== lastHealthStatus) {
        log("Health status: " + status);
      }
      lastHealthStatus = status;
    } catch (error) {
      renderStatus("error");
      renderEngineRack(null);
      healthUpdated.textContent = "Health check failed";
      lastHealthStatus = "error";
      log("Error: " + (error && error.message ? error.message : String(error)), "error");
    } finally {
      healthButton.disabled = false;
    }
  }

  function base64ToBlob(b64, contentType) {
    var binary = atob(b64);
    var bytes = new Uint8Array(binary.length);
    for (var i = 0; i < binary.length; i += 1) {
      bytes[i] = binary.charCodeAt(i);
    }
    return new Blob([bytes], { type: contentType });
  }

  function readGeneratedImage(data) {
    var images = data && Array.isArray(data.data) ? data.data : [];
    var first = images[0] || {};
    var b64 = typeof first.b64_json === "string" ? first.b64_json : "";
    if (!b64) {
      throw new Error("Image generation returned no b64_json data");
    }
    return b64;
  }

  function collectStorySources() {
    var titles = Array.prototype.slice.call(document.querySelectorAll(".story-source-title"));
    var urls = Array.prototype.slice.call(document.querySelectorAll(".story-source-url"));
    var excerpts = Array.prototype.slice.call(document.querySelectorAll(".story-source-excerpt"));
    return titles.map(function (titleInput, index) {
      return {
        id: "src-" + (index + 1),
        title: titleInput.value.trim(),
        url: urls[index] ? urls[index].value.trim() : "",
        excerpt: excerpts[index] ? excerpts[index].value.trim() : ""
      };
    });
  }

  function clearStoryPoll() {
    if (activeStoryPoll) {
      window.clearTimeout(activeStoryPoll);
      activeStoryPoll = 0;
    }
  }

  function setStoryStatus(status, progress) {
    storyStatus.textContent = status || "Idle";
    storyProgress.value = Number.isFinite(progress) ? progress : 0;
  }

  function appendSection(container, title, body) {
    container.appendChild(createElement("h3", "", title));
    container.appendChild(body);
  }

  function renderStoryManifest(manifest) {
    storyFacts.textContent = "";
    if (!manifest) {
      return;
    }

    var sourcesList = createElement("ul");
    (manifest.sources || []).forEach(function (source) {
      var item = createElement("li");
      if (source.url) {
        var link = createElement("a", "story-source-link", source.title || source.id || source.url);
        link.href = source.url;
        link.target = "_blank";
        link.rel = "noreferrer";
        item.appendChild(link);
      } else {
        item.textContent = source.title || source.id || "source";
      }
      sourcesList.appendChild(item);
    });

    var speakers = {};
    var castList = createElement("ul", "story-cast-list");
    (manifest.cast || []).forEach(function (member) {
      speakers[member.id] = member.display_name || member.id;
      castList.appendChild(createElement("li", "", (member.display_name || member.id || "speaker") + " / " + (member.voice_id || "voice")));
    });

    var scriptList = createElement("div", "story-script");
    (manifest.script || []).forEach(function (line) {
      var row = createElement("article", "story-line");
      row.appendChild(createElement("span", "story-speaker", speakers[line.speaker_id] || line.speaker_id || "speaker"));
      row.appendChild(createElement("p", "story-text", line.text || ""));
      row.appendChild(createElement("span", "story-fact-refs", (line.fact_ids || []).join(", ")));
      scriptList.appendChild(row);
    });

    var factsList = createElement("ul");
    (manifest.fact_cards || []).forEach(function (fact) {
      factsList.appendChild(createElement("li", "", (fact.id || "fact") + ": " + (fact.claim || "")));
    });

    appendSection(storyFacts, "Sources", sourcesList);
    appendSection(storyFacts, "Cast", castList);
    appendSection(storyFacts, "Script", scriptList);
    appendSection(storyFacts, "Fact cards", factsList);
  }

  function renderStoryLibrary(stories) {
    storyLibrary.textContent = "";
    if (!stories || stories.length === 0) {
      storyLibrary.appendChild(createElement("span", "story-library-empty", "No retained stories yet"));
      return;
    }
    stories.forEach(function (story) {
      var item = createElement("button", "story-library-item");
      item.type = "button";
      item.dataset.storyId = story.id || "";
      var title = story.title || story.subject || story.id || "Story";
      var created = story.created_at ? new Date(story.created_at).toLocaleString() : "";
      var detail = [story.status || "unknown", story.duration_seconds ? story.duration_seconds + "s" : "", created].filter(Boolean).join(" / ");
      item.appendChild(createElement("span", "story-library-title", title));
      item.appendChild(createElement("span", "story-library-detail", detail));
      item.addEventListener("click", function () {
        loadStory(story.id);
      });
      storyLibrary.appendChild(item);
    });
  }

  async function refreshStoryLibrary(silent) {
    clearStoryError();
    storyLibraryButton.disabled = true;
    try {
      if (!silent) {
        log("GET /v1/stories");
      }
      var response = await fetch("/v1/stories", { method: "GET" });
      await ensureOk(response, "Story library");
      var data = await response.json();
      renderStoryLibrary(data.stories || []);
    } catch (error) {
      setStoryError(error);
    } finally {
      storyLibraryButton.disabled = false;
    }
  }

  async function loadStory(id) {
    if (!id) {
      return;
    }
    clearStoryError();
    if (activeStoryID) {
      setStoryError(new Error("A story is already running"));
      return;
    }
    clearStoryPoll();
    setRunning(true);
    try {
      log("GET /v1/stories/" + id);
      var response = await fetch("/v1/stories/" + encodeURIComponent(id), { method: "GET" });
      await ensureOk(response, "Story load");
      var data = await response.json();
      setStoryStatus(data.status || data.stage || "unknown", data.progress || 0);
      if (!data.manifest) {
        throw new Error("Story has no completed manifest");
      }
      if (data.artifact_url) {
        storyAudio.src = data.artifact_url;
        storyAudio.load();
        saveStoryButton.disabled = false;
      }
      renderStoryManifest(data.manifest);
      log("Story loaded: " + id);
    } catch (error) {
      setStoryStatus("Error", 0);
      setStoryError(error);
    } finally {
      setRunning(false);
    }
  }

  async function pollStory(id) {
    clearStoryPoll();
    try {
      var response = await fetch("/v1/stories/" + encodeURIComponent(id), { method: "GET" });
      await ensureOk(response, "Story status");
      var data = await response.json();
      setStoryStatus(data.status || data.stage || "unknown", data.progress || 0);
      if (data.status === "complete") {
        activeStoryID = "";
        storyCancelButton.disabled = true;
        storyGenerateButton.disabled = false;
        if (data.artifact_url) {
          storyAudio.src = data.artifact_url;
          storyAudio.load();
          saveStoryButton.disabled = false;
        }
        renderStoryManifest(data.manifest);
        log("Story complete");
        refreshStoryLibrary(true);
        return;
      }
      if (data.status === "failed") {
        activeStoryID = "";
        storyCancelButton.disabled = true;
        storyGenerateButton.disabled = false;
        throw new Error(data.error && data.error.message ? data.error.message : "Story failed");
      }
      if (data.status === "cancelled") {
        activeStoryID = "";
        storyCancelButton.disabled = true;
        storyGenerateButton.disabled = false;
        log("Story cancelled");
        return;
      }
      activeStoryPoll = window.setTimeout(function () {
        pollStory(id);
      }, data.retry_after_ms || 500);
    } catch (error) {
      activeStoryID = "";
      storyCancelButton.disabled = true;
      storyGenerateButton.disabled = false;
      setStoryStatus("Error", 0);
      setStoryError(error);
    }
  }

  async function startStory(event) {
    if (event) {
      event.preventDefault();
    }
    clearStoryError();
    if (activeStoryID) {
      setStoryError(new Error("A story is already running"));
      return;
    }
    clearStoryPoll();
    setRunning(true);
    setBusy(storyGenerateButton, "Queueing...");
    try {
      storyAudio.removeAttribute("src");
      storyAudio.load();
      saveStoryButton.disabled = true;
      storyFacts.textContent = "";
      setStoryStatus("Starting...", 0);
      var body = storyRequestBody();
      if (storyDraft && !scriptEditor.hidden) {
        body.title = storyTitleInput.value.trim();
        body.script = collectEditedScript();
        log("POST /v1/stories (producing the edited draft)");
      } else {
        log("POST /v1/stories");
      }
      var response = await fetch("/v1/stories", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify(body)
      });
      await ensureOk(response, "Story");
      var data = await response.json();
      activeStoryID = data.id;
      storyCancelButton.disabled = false;
      log("Story queued: " + activeStoryID);
      pollStory(activeStoryID);
    } catch (error) {
      activeStoryID = "";
      storyCancelButton.disabled = true;
      setStoryStatus("Error", 0);
      setStoryError(error);
    } finally {
      clearBusy(storyGenerateButton);
      setRunning(false);
    }
  }

  async function cancelStory() {
    if (!activeStoryID) {
      return;
    }
    clearStoryError();
    var id = activeStoryID;
    clearStoryPoll();
    try {
      log("POST /v1/stories/" + id + "/cancel");
      var response = await fetch("/v1/stories/" + encodeURIComponent(id) + "/cancel", {
        method: "POST"
      });
      await ensureOk(response, "Cancel story");
      var data = await response.json();
      activeStoryID = "";
      storyCancelButton.disabled = true;
      storyGenerateButton.disabled = false;
      setStoryStatus(data.status || "cancelled", data.progress || 0);
    } catch (error) {
      setStoryError(error);
    }
  }

  async function generateImage(event) {
    if (event) {
      event.preventDefault();
    }
    clearImageError();
    setRunning(true);
    setBusy(generateImageButton, "Generating...");
    try {
      var prompt = imagePromptInput.value.trim();
      var size = imageSizeInput.value.trim() || "512x512";
      if (!prompt) {
        throw new Error("Enter an image prompt");
      }
      imageStatus.textContent = "Generating...";
      log("POST /v1/images/generations");
      var response = await fetch("/v1/images/generations", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify({
          prompt: prompt,
          size: size,
          response_format: "b64_json"
        })
      });
      await ensureOk(response, "Image generation");
      var data = await response.json();
      var b64 = readGeneratedImage(data);
      imagePreview.src = "data:image/png;base64," + b64;
      imagePreview.hidden = false;
      imagePlaceholder.hidden = true;
      saveImageButton.disabled = false;
      libraryImageButton.disabled = false;
      imageDescriptionOutput.value = "";
      clearDescribeAudio();
      imageStatus.textContent = size + " PNG, " + formatBytes(estimateBase64Bytes(b64));
      log("Image PNG received: " + imageStatus.textContent);
    } catch (error) {
      imageStatus.textContent = "Error";
      setImageError(error);
    } finally {
      clearBusy(generateImageButton);
      setRunning(false);
    }
  }

  function chooseImageFile(event) {
    clearImageError();
    var file = event.target.files && event.target.files[0];
    if (!file) {
      return;
    }
    var name = file.name || "image.png";
    var looksLikePng = /\.png$/i.test(name) || /png/i.test(file.type || "");
    if (!looksLikePng) {
      event.target.value = "";
      setImageError(new Error("Choose a PNG file"));
      return;
    }
    var reader = new FileReader();
    reader.onload = function () {
      imagePreview.src = reader.result;
      imagePreview.hidden = false;
      imagePlaceholder.hidden = true;
      saveImageButton.disabled = false;
      libraryImageButton.disabled = false;
      imageDescriptionOutput.value = "";
      clearDescribeAudio();
      imageStatus.textContent = "upload: " + name + " (" + formatBytes(file.size) + ")";
      syncControls();
      log("Image PNG loaded from upload: " + name + ", " + formatBytes(file.size));
    };
    reader.onerror = function () {
      setImageError(new Error("Could not read the chosen PNG"));
    };
    reader.readAsDataURL(file);
  }

  // 1024px keeps descriptions accurate while roughly halving the vision
  // model's image-token load versus 1536px (faster encode, smaller VRAM
  // scratch alongside the other resident engines).
  var DESCRIBE_MAX_DIMENSION = 1024;

  // describeImageSource re-encodes whatever is in the preview frame as a
  // PNG no larger than DESCRIBE_MAX_DIMENSION on its longest side. The
  // vision model does not need full-resolution input, and huge uploads
  // otherwise blow past the request body limit.
  function describeImageSource() {
    var width = imagePreview.naturalWidth;
    var height = imagePreview.naturalHeight;
    if (!width || !height) {
      return null;
    }
    var scale = Math.min(1, DESCRIBE_MAX_DIMENSION / Math.max(width, height));
    var canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(width * scale));
    canvas.height = Math.max(1, Math.round(height * scale));
    var context = canvas.getContext("2d");
    context.drawImage(imagePreview, 0, 0, canvas.width, canvas.height);
    var prefix = "data:image/png;base64,";
    var dataUrl;
    try {
      dataUrl = canvas.toDataURL("image/png");
    } catch (error) {
      return null;
    }
    if (!dataUrl.startsWith(prefix)) {
      return null;
    }
    return {
      b64: dataUrl.slice(prefix.length),
      width: canvas.width,
      height: canvas.height,
      scaled: scale < 1
    };
  }

  async function describeImage() {
    clearImageError();
    if (imagePreview.hidden || !imagePreview.src) {
      setImageError(new Error("Generate an image or load a PNG first"));
      return;
    }
    var source = describeImageSource();
    if (!source) {
      setImageError(new Error("Could not read the current image"));
      return;
    }
    setRunning(true);
    setBusy(describeImageButton, "Looking...");
    try {
      imageDescriptionOutput.value = "";
      clearDescribeAudio();
      if (source.scaled) {
        log("Image downscaled to " + source.width + "x" + source.height + " for the vision engine");
      }
      log("POST /v1/images/descriptions" + (selectedVoiceId ? " with voice " + selectedVoiceId : ""));
      var response = await fetch("/v1/images/descriptions", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify({
          image_b64: source.b64,
          voice: selectedVoiceId
        })
      });
      await ensureOk(response, "Image description");
      var data = await response.json();
      imageDescriptionOutput.value = data.description || "";
      if (!data.audio_b64) {
        throw new Error("Image description returned no audio");
      }
      var speech = base64ToBlob(data.audio_b64, "audio/wav");
      log("Vision description spoken: " + formatBytes(speech.size) + " WAV");
      describeAudioUrl = URL.createObjectURL(speech);
      describeAudio.src = describeAudioUrl;
      describeAudio.load();
      saveDescriptionButton.disabled = false;
      try {
        await describeAudio.play();
      } catch (error) {
        log("Description playback is ready");
      }
    } catch (error) {
      setImageError(error);
    } finally {
      clearBusy(describeImageButton);
      setRunning(false);
    }
  }

  async function runVoiceLoop(event) {
    if (event) {
      event.preventDefault();
    }
    clearError();
    setRunning(true);
    setBusy(runButton, "Running...");
    try {
      resetOutputs();
      var form = new FormData();
      if (activeWavFile) {
        form.append("file", activeWavFile, activeWavFile.name || "input.wav");
      } else {
        var text = messageInput.value.trim();
        if (!text) {
          throw new Error("Record audio, choose a WAV, or type a message");
        }
        form.append("message", text);
        log("Using typed message as transcript");
      }
      if (conversation.length > 0) {
        form.append("history", JSON.stringify(conversation));
      }
      if (voiceSelect.value) {
        form.append("voice", voiceSelect.value);
        var chosen = voiceSelect.options[voiceSelect.selectedIndex];
        log("Speaking with cloned voice: " + (chosen ? chosen.textContent : voiceSelect.value));
      }

      log("POST /v1/voice");
      var response = await fetch("/v1/voice", {
        method: "POST",
        body: form
      });
      await ensureOk(response, "Voice loop");
      var data = await response.json();
      transcriptOutput.value = data.transcript || "";
      replyOutput.value = data.reply || "";
      if (!data.audio_b64) {
        throw new Error("Voice loop returned no audio");
      }
      var speech = base64ToBlob(data.audio_b64, "audio/wav");
      recordExchange(data.transcript || "", data.reply || "");
      // The turn is in the conversation now; clear the inputs so the next
      // recording or message is a fresh follow-up.
      clearActiveWav();
      messageInput.value = "";
      log("Voice loop complete: " + formatBytes(speech.size) + " WAV");

      activeAudioUrl = URL.createObjectURL(speech);
      replyAudio.src = activeAudioUrl;
      replyAudio.load();
      saveReplyButton.disabled = false;
      libraryReplyButton.disabled = false;
      try {
        await replyAudio.play();
      } catch (error) {
        log("Audio playback is ready");
      }
    } catch (error) {
      setError(error);
    } finally {
      clearBusy(runButton);
      setRunning(false);
    }
  }

  function setCloneError(error) {
    var message = error && error.message ? error.message : String(error);
    cloneErrorBox.textContent = message;
    cloneErrorBox.hidden = false;
    log("Error: " + message, "error");
  }

  function clearCloneError() {
    cloneErrorBox.textContent = "";
    cloneErrorBox.hidden = true;
  }

  function setCloneWav(file, source) {
    cloneWavFile = file;
    cloneWavStatus.textContent = source + ": " + file.name + " (" + formatBytes(file.size) + ")";
    cloneWavSaveButton.disabled = false;
    log("Reference WAV ready from " + source + ": " + file.name + ", " + formatBytes(file.size));
  }

  function clearCloneWav() {
    cloneWavFile = null;
    cloneWavInput.value = "";
    cloneWavStatus.textContent = "None";
    cloneWavSaveButton.disabled = true;
  }

  function transcriptSnippet(text) {
    if (!text) {
      return "";
    }
    return text.length > 120 ? text.slice(0, 117) + "..." : text;
  }

  function renderVoiceLibrary(voices) {
    voiceLibrary.textContent = "";
    if (!voices || voices.length === 0) {
      voiceLibrary.appendChild(createElement("span", "voice-library-empty", "No cloned voices yet"));
      return;
    }
    voices.forEach(function (clone) {
      var item = createElement("article", "voice-item" + (clone.id === selectedVoiceId ? " selected" : ""));
      var head = createElement("div", "voice-item-head");
      head.appendChild(createElement("span", "voice-item-name", clone.name || clone.id));
      head.appendChild(createElement("span", "voice-item-detail", clone.created_at ? new Date(clone.created_at).toLocaleString() : ""));
      item.appendChild(head);
      item.appendChild(createElement("p", "voice-item-transcript", clone.transcript || ""));

      var actions = createElement("div", "voice-item-actions");
      var useButton = createElement("button", "secondary compact-button", clone.id === selectedVoiceId ? "In use" : "Use in loop");
      useButton.type = "button";
      useButton.addEventListener("click", function () {
        selectVoice(clone.id === selectedVoiceId ? "" : clone.id);
      });
      var playButton = createElement("button", "secondary compact-button", "Play");
      playButton.type = "button";
      playButton.addEventListener("click", function () {
        clonePreviewAudio.src = clone.audio_url;
        clonePreviewAudio.load();
        clonePreviewAudio.play().catch(function () {
          log("Reference playback is ready");
        });
      });
      actions.appendChild(useButton);
      actions.appendChild(playButton);
      if (clone.protected) {
        actions.appendChild(createElement("span", "voice-item-lock", "Protected"));
      } else {
        var deleteButton = createElement("button", "plain compact-button", "Delete");
        deleteButton.type = "button";
        deleteButton.addEventListener("click", function () {
          deleteVoice(clone.id, clone.name);
        });
        actions.appendChild(deleteButton);
      }
      item.appendChild(actions);
      voiceLibrary.appendChild(item);
    });
  }

  function renderVoiceSelect(voices) {
    renderAudiobookVoices(voices);
    var previous = voiceSelect.value;
    voiceSelect.textContent = "";
    var fallback = createElement("option", "", "Studio default");
    fallback.value = "";
    voiceSelect.appendChild(fallback);
    (voices || []).forEach(function (clone) {
      var option = createElement("option", "", clone.name || clone.id);
      option.value = clone.id;
      voiceSelect.appendChild(option);
    });
    var wanted = selectedVoiceId || previous;
    voiceSelect.value = wanted;
    if (voiceSelect.value !== wanted) {
      voiceSelect.value = "";
      selectedVoiceId = "";
      persistSelectedVoice();
    }
    updateSpeakVoiceLabel();
  }

  // ---------- story cast editor ----------

  var STORY_DEFAULT_CAST = [
    { name: "Narrator", role: "sets scenes and links ideas" },
    { name: "Nova", role: "asks curious questions" },
    { name: "Dr. Lumen", role: "explains clearly" }
  ];
  var STORY_MIN_CAST = 2;
  var STORY_MAX_CAST = 6;

  function fillVoiceOptions(select, keepValue) {
    select.textContent = "";
    var fallback = createElement("option", "", "Studio default");
    fallback.value = "";
    select.appendChild(fallback);
    libraryVoices.forEach(function (clone) {
      var option = createElement("option", "", clone.name || clone.id);
      option.value = clone.id;
      select.appendChild(option);
    });
    select.value = keepValue || "";
    if (select.value !== (keepValue || "")) {
      select.value = "";
    }
  }

  function addCastRow(prefill) {
    var row = createElement("div", "cast-row");
    var nameInput = createElement("input", "text-input cast-name");
    nameInput.type = "text";
    nameInput.maxLength = 60;
    nameInput.placeholder = "Speaker name";
    nameInput.value = (prefill && prefill.name) || "";
    var roleInput = createElement("input", "text-input cast-role");
    roleInput.type = "text";
    roleInput.maxLength = 200;
    roleInput.placeholder = "Role, e.g. gruff sea captain";
    roleInput.value = (prefill && prefill.role) || "";
    var voiceSelectEl = createElement("select", "text-input cast-voice-select");
    fillVoiceOptions(voiceSelectEl, (prefill && prefill.voice) || "");
    var removeButton = createElement("button", "plain icon-button cast-remove", "×");
    removeButton.type = "button";
    removeButton.title = "Remove speaker";
    removeButton.addEventListener("click", function () {
      if (castList.children.length <= STORY_MIN_CAST) {
        setStoryError(new Error("A story needs at least " + STORY_MIN_CAST + " speakers"));
        return;
      }
      row.remove();
      syncControls();
    });
    row.appendChild(nameInput);
    row.appendChild(roleInput);
    row.appendChild(voiceSelectEl);
    row.appendChild(removeButton);
    castList.appendChild(row);
  }

  function resetCast() {
    castList.textContent = "";
    STORY_DEFAULT_CAST.forEach(addCastRow);
  }

  function collectCast() {
    return Array.prototype.map.call(castList.children, function (row) {
      return {
        name: row.querySelector(".cast-name").value.trim(),
        role: row.querySelector(".cast-role").value.trim(),
        voice_id: row.querySelector(".cast-voice-select").value
      };
    }).filter(function (member) {
      return member.name !== "";
    });
  }

  function renderCastVoiceSelects() {
    Array.prototype.forEach.call(castList.querySelectorAll(".cast-voice-select"), function (select) {
      fillVoiceOptions(select, select.value);
    });
  }

  // ---------- story sources ----------

  var STORY_SOURCE_PREFILLS = [
    {
      title: "NASA Science: Star Basics",
      url: "https://science.nasa.gov/universe/stars/",
      excerpt: "Stars form inside molecular clouds of gas and dust. Cold cloud conditions help gas clump into denser pockets. As clumps gain mass, gravity can make them collapse."
    },
    {
      title: "NASA Webb: Fiery Hourglass",
      url: "https://science.nasa.gov/missions/webb/nasas-webb-catches-fiery-hourglass-as-new-star-forms/",
      excerpt: "A forming protostar gathers material from its surrounding molecular cloud. Falling material spirals inward and forms an accretion disk. The disk feeds material onto the protostar."
    },
    {
      title: "NASA Hubble: Planet-Forming Disks",
      url: "https://science.nasa.gov/missions/hubble/hubbles-album-of-planet-forming-disks/",
      excerpt: "Some falling material forms a rotating disk around the protostar. Jets from magnetic poles are part of star formation. Jets help carry away angular momentum so material can continue collecting."
    }
  ];
  var STORY_MIN_SOURCES = 3;
  var STORY_MAX_SOURCES = 5;

  function sourceField(labelText, element) {
    var label = createElement("label", "field");
    label.appendChild(createElement("span", "", labelText));
    label.appendChild(element);
    return label;
  }

  function addSourceRow(prefill, open) {
    var details = createElement("details", "source-group");
    details.open = Boolean(open);
    var summary = createElement("summary", "", "Source");
    var titleInput = createElement("input", "text-input story-source-title");
    titleInput.type = "text";
    titleInput.value = (prefill && prefill.title) || "";
    titleInput.addEventListener("input", renumberSources);
    var urlInput = createElement("input", "text-input story-source-url");
    urlInput.type = "url";
    urlInput.value = (prefill && prefill.url) || "";
    var excerptInput = createElement("textarea", "story-source-excerpt");
    excerptInput.rows = 3;
    excerptInput.value = (prefill && prefill.excerpt) || "";
    var fields = createElement("div", "source-fields");
    fields.appendChild(sourceField("Title", titleInput));
    fields.appendChild(sourceField("URL", urlInput));
    fields.appendChild(sourceField("Excerpt", excerptInput));
    var removeButton = createElement("button", "plain compact-button", "Remove source");
    removeButton.type = "button";
    removeButton.addEventListener("click", function () {
      if (sourceStack.children.length <= STORY_MIN_SOURCES) {
        setStoryError(new Error("A story needs at least " + STORY_MIN_SOURCES + " sources"));
        return;
      }
      details.remove();
      renumberSources();
    });
    fields.appendChild(removeButton);
    details.appendChild(summary);
    details.appendChild(fields);
    sourceStack.appendChild(details);
    renumberSources();
  }

  function renumberSources() {
    Array.prototype.forEach.call(sourceStack.children, function (details, index) {
      var title = details.querySelector(".story-source-title").value.trim();
      details.querySelector("summary").textContent = "Source " + (index + 1) + (title ? " · " + title : "");
    });
  }

  function resetSources() {
    sourceStack.textContent = "";
    STORY_SOURCE_PREFILLS.forEach(function (prefill, index) {
      addSourceRow(prefill, index === 0);
    });
  }

  // ---------- story draft editor ----------

  function discardDraft() {
    storyDraft = null;
    scriptEditor.hidden = true;
    scriptLines.textContent = "";
    scriptDraftMeta.textContent = "";
    storyTitleInput.value = "";
  }

  function renderDraft(draft) {
    storyDraft = draft;
    storyTitleInput.value = draft.title || "";
    scriptDraftMeta.textContent = (draft.script || []).length + " lines · " + (draft.fact_cards || []).length + " facts";
    scriptLines.textContent = "";
    (draft.script || []).forEach(function (line) {
      var row = createElement("div", "script-line-row");
      row.dataset.factIds = JSON.stringify(line.fact_ids || []);
      var speakerSelect = createElement("select", "text-input script-speaker");
      (draft.cast || []).forEach(function (member) {
        var option = createElement("option", "", member.display_name || member.id);
        option.value = member.id;
        speakerSelect.appendChild(option);
      });
      speakerSelect.value = line.speaker_id;
      var textArea = createElement("textarea", "script-text");
      textArea.rows = 2;
      textArea.value = line.text || "";
      var facts = createElement("span", "script-facts", (line.fact_ids || []).join(", "));
      row.appendChild(speakerSelect);
      row.appendChild(textArea);
      row.appendChild(facts);
      scriptLines.appendChild(row);
    });
    scriptEditor.hidden = false;
  }

  function collectEditedScript() {
    return Array.prototype.map.call(scriptLines.children, function (row) {
      return {
        speaker_id: row.querySelector(".script-speaker").value,
        text: row.querySelector(".script-text").value.trim(),
        fact_ids: JSON.parse(row.dataset.factIds || "[]")
      };
    }).filter(function (line) {
      return line.text !== "";
    });
  }

  function storyRequestBody() {
    return {
      subject: storySubjectInput.value.trim(),
      target_seconds: Number(storySecondsInput.value || "90"),
      source_mode: "curated",
      voice_mode: storyVoiceSelect.value,
      cast: collectCast(),
      sources: collectStorySources()
    };
  }

  async function draftStory() {
    clearStoryError();
    setRunning(true);
    setBusy(storyDraftButton, "Writing...");
    try {
      discardDraft();
      log("POST /v1/stories/draft");
      var response = await fetch("/v1/stories/draft", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify(storyRequestBody())
      });
      await ensureOk(response, "Story draft");
      var draft = await response.json();
      renderDraft(draft);
      log("Draft ready: \"" + (draft.title || "") + "\", " + (draft.script || []).length + " lines");
    } catch (error) {
      setStoryError(error);
    } finally {
      clearBusy(storyDraftButton);
      setRunning(false);
    }
  }

  function persistSelectedVoice() {
    try {
      window.localStorage.setItem("cpp-studio-voice", selectedVoiceId);
    } catch (error) {
      // Private browsing: selection just doesn't survive a reload.
    }
  }

  function selectVoice(id) {
    selectedVoiceId = id || "";
    persistSelectedVoice();
    voiceSelect.value = selectedVoiceId;
    if (voiceSelect.value !== selectedVoiceId) {
      voiceSelect.value = "";
    }
    updateSpeakVoiceLabel();
    refreshVoices(true);
    log(selectedVoiceId ? "Voice loop will speak with " + selectedVoiceId : "Voice loop back to the studio default voice");
  }

  async function refreshVoices(silent) {
    voiceLibraryButton.disabled = true;
    try {
      if (!silent) {
        log("GET /v1/voices");
      }
      var response = await fetch("/v1/voices", { method: "GET" });
      await ensureOk(response, "Voice library");
      var data = await response.json();
      var voices = data.voices || [];
      if (selectedVoiceId && !voices.some(function (clone) { return clone.id === selectedVoiceId; })) {
        selectedVoiceId = "";
        persistSelectedVoice();
      }
      libraryVoices = voices;
      renderVoiceLibrary(voices);
      renderVoiceSelect(voices);
      renderCastVoiceSelects();
    } catch (error) {
      setCloneError(error);
    } finally {
      voiceLibraryButton.disabled = false;
    }
  }

  async function saveClone(event) {
    if (event) {
      event.preventDefault();
    }
    clearCloneError();
    if (!cloneWavFile) {
      setCloneError(new Error("Record or choose a reference WAV first"));
      return;
    }
    setRunning(true);
    setBusy(cloneSaveButton, "Transcribing & saving...");
    try {
      var form = new FormData();
      form.append("file", cloneWavFile, cloneWavFile.name || "reference.wav");
      var name = cloneNameInput.value.trim();
      if (name) {
        form.append("name", name);
      }
      log("POST /v1/voices");
      var response = await fetch("/v1/voices", { method: "POST", body: form });
      await ensureOk(response, "Voice clone");
      var clone = await response.json();
      log("Voice saved: " + clone.name + " — \"" + transcriptSnippet(clone.transcript) + "\"");
      clearCloneWav();
      cloneNameInput.value = "";
      selectedVoiceId = clone.id;
      persistSelectedVoice();
      await refreshVoices(true);
    } catch (error) {
      setCloneError(error);
    } finally {
      clearBusy(cloneSaveButton);
      setRunning(false);
    }
  }

  async function deleteVoice(id, name) {
    clearCloneError();
    try {
      log("DELETE /v1/voices/" + id);
      var response = await fetch("/v1/voices/" + encodeURIComponent(id), { method: "DELETE" });
      await ensureOk(response, "Voice delete");
      if (selectedVoiceId === id) {
        selectedVoiceId = "";
        persistSelectedVoice();
      }
      log("Voice deleted: " + (name || id));
      await refreshVoices(true);
    } catch (error) {
      setCloneError(error);
    }
  }

  function updateSpeakVoiceLabel() {
    var chosen = voiceSelect.options[voiceSelect.selectedIndex];
    var label = voiceSelect.value && chosen ? chosen.textContent : "the studio default voice";
    speakVoiceLabel.textContent = label;
    describeVoiceLabel.textContent = label;
  }

  function clearSpeakAudio() {
    if (speakAudioUrl) {
      URL.revokeObjectURL(speakAudioUrl);
      speakAudioUrl = "";
    }
    speakAudio.removeAttribute("src");
    speakAudio.load();
    saveSpeakButton.disabled = true;
    librarySpeakButton.disabled = true;
  }

  async function speakText(event) {
    if (event) {
      event.preventDefault();
    }
    clearCloneError();
    var text = speakTextInput.value.trim();
    if (!text) {
      setCloneError(new Error("Type something for the voice to say"));
      return;
    }
    setRunning(true);
    setBusy(speakButton, "Speaking...");
    try {
      clearSpeakAudio();
      log("POST /v1/audio/speech" + (selectedVoiceId ? " with voice " + selectedVoiceId : ""));
      var response = await fetch("/v1/audio/speech", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify({
          input: text,
          voice: selectedVoiceId,
          format: "wav"
        })
      });
      await ensureOk(response, "Speak");
      var blob = await response.blob();
      log("Spoken text ready: " + formatBytes(blob.size) + " WAV");
      speakAudioUrl = URL.createObjectURL(blob);
      speakAudio.src = speakAudioUrl;
      speakAudio.load();
      saveSpeakButton.disabled = false;
      librarySpeakButton.disabled = false;
      try {
        await speakAudio.play();
      } catch (error) {
        log("Spoken audio playback is ready");
      }
    } catch (error) {
      setCloneError(error);
    } finally {
      clearBusy(speakButton);
      setRunning(false);
    }
  }

  function setDesignError(error) {
    var message = error && error.message ? error.message : String(error);
    designErrorBox.textContent = message;
    designErrorBox.hidden = false;
    log("Error: " + message, "error");
  }

  function clearDesignError() {
    designErrorBox.textContent = "";
    designErrorBox.hidden = true;
  }

  function clearDesignCandidate() {
    designCandidate = null;
    designStatus.textContent = "None yet";
    designEngineInput.textContent = "–";
    if (designAudioUrl) {
      URL.revokeObjectURL(designAudioUrl);
      designAudioUrl = "";
    }
    designAudio.removeAttribute("src");
    designAudio.load();
    saveDesignWavButton.disabled = true;
    syncControls();
  }

  async function generateDesign(event) {
    if (event) {
      event.preventDefault();
    }
    clearDesignError();
    var description = designDescriptionInput.value.trim();
    if (!description) {
      setDesignError(new Error("Describe the voice you want first"));
      return;
    }
    setRunning(true);
    setBusy(designGenerateButton, "Designing...");
    try {
      clearDesignCandidate();
      var model = designModelSelect.value || "voxcpm2";
      log("POST /v1/voices/design (" + model + "): " + description);
      var response = await fetch("/v1/voices/design", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify({ description: description, model: model })
      });
      await ensureOk(response, "Voice design");
      var data = await response.json();
      if (!data.reference_b64 || !data.preview_b64) {
        throw new Error("Voice design returned no audio");
      }
      designCandidate = {
        description: data.description || description,
        model: data.model || model,
        reference: base64ToBlob(data.reference_b64, "audio/wav"),
        transcript: data.transcript || ""
      };
      designStatus.textContent = "\"" + designCandidate.description + "\" via " + designCandidate.model;
      designEngineInput.textContent = data.engine_input || designCandidate.description;
      if (data.engine_input && data.engine_input !== designCandidate.description) {
        log("Description adapted for " + designCandidate.model + ": " + data.engine_input);
      }
      if (!designNameInput.value.trim()) {
        designNameInput.value = designCandidate.description.slice(0, 80);
      }
      var preview = base64ToBlob(data.preview_b64, "audio/wav");
      log("Designed voice ready: " + formatBytes(preview.size) + " WAV audition");
      designAudioUrl = URL.createObjectURL(preview);
      designAudio.src = designAudioUrl;
      designAudio.load();
      saveDesignWavButton.disabled = false;
      try {
        await designAudio.play();
      } catch (error) {
        log("Voice audition playback is ready");
      }
    } catch (error) {
      setDesignError(error);
    } finally {
      clearBusy(designGenerateButton);
      setRunning(false);
    }
  }

  async function saveDesign() {
    clearDesignError();
    if (!designCandidate) {
      setDesignError(new Error("Generate a voice first"));
      return;
    }
    setRunning(true);
    setBusy(designSaveButton, "Saving...");
    try {
      var form = new FormData();
      form.append("file", new File([designCandidate.reference], "designed-voice.wav", { type: "audio/wav" }));
      var name = designNameInput.value.trim() || designCandidate.description.slice(0, 80);
      form.append("name", name);
      form.append("transcript", designCandidate.transcript);
      log("POST /v1/voices (designed voice)");
      var response = await fetch("/v1/voices", { method: "POST", body: form });
      await ensureOk(response, "Voice save");
      var clone = await response.json();
      log("Designed voice saved to the library: " + clone.name);
      selectedVoiceId = clone.id;
      persistSelectedVoice();
      designNameInput.value = "";
      await refreshVoices(true);
    } catch (error) {
      setDesignError(error);
    } finally {
      clearBusy(designSaveButton);
      setRunning(false);
    }
  }

  function mergeChunks(chunks, length) {
    var output = new Float32Array(length);
    var offset = 0;
    chunks.forEach(function (chunk) {
      output.set(chunk, offset);
      offset += chunk.length;
    });
    return output;
  }

  function writeAscii(view, offset, text) {
    for (var i = 0; i < text.length; i += 1) {
      view.setUint8(offset + i, text.charCodeAt(i));
    }
  }

  function encodeWav(samples, sampleRate) {
    var bytesPerSample = 2;
    var blockAlign = bytesPerSample;
    var buffer = new ArrayBuffer(44 + samples.length * bytesPerSample);
    var view = new DataView(buffer);

    writeAscii(view, 0, "RIFF");
    view.setUint32(4, 36 + samples.length * bytesPerSample, true);
    writeAscii(view, 8, "WAVE");
    writeAscii(view, 12, "fmt ");
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true);
    view.setUint16(22, 1, true);
    view.setUint32(24, sampleRate, true);
    view.setUint32(28, sampleRate * blockAlign, true);
    view.setUint16(32, blockAlign, true);
    view.setUint16(34, 16, true);
    writeAscii(view, 36, "data");
    view.setUint32(40, samples.length * bytesPerSample, true);

    var offset = 44;
    for (var i = 0; i < samples.length; i += 1) {
      var sample = Math.max(-1, Math.min(1, samples[i]));
      view.setInt16(offset, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
      offset += 2;
    }

    return new Blob([view], { type: "audio/wav" });
  }

  async function cleanupRecorderResources(target) {
    if (!target) {
      return;
    }
    try {
      if (target.processor) {
        target.processor.disconnect();
      }
    } catch (error) {
      // Already disconnected.
    }
    try {
      if (target.source) {
        target.source.disconnect();
      }
    } catch (error) {
      // Already disconnected.
    }
    if (target.stream) {
      target.stream.getTracks().forEach(function (track) {
        track.stop();
      });
    }
    if (target.audioContext && target.audioContext.state !== "closed") {
      try {
        await target.audioContext.close();
      } catch (error) {
        // Ignore cleanup errors after a failed recording setup.
      }
    }
  }

  async function startRecording(event) {
    if (event && recordButton.setPointerCapture && event.pointerId !== undefined) {
      recordButton.setPointerCapture(event.pointerId);
    }
    clearError();
    if (!canRecord()) {
      setError(new Error("Audio recording is not available in this browser"));
      return;
    }
    if (recording || running) {
      return;
    }

    recordStopRequested = false;
    setRecordSetupPending(true);
    var stream = null;
    var audioContext = null;
    var source = null;
    var processor = null;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true
        }
      });
      if (recordStopRequested) {
        await cleanupRecorderResources({ stream: stream });
        recordStopRequested = false;
        setRecordSetupPending(false);
        log("Recording cancelled before microphone setup completed");
        return;
      }
      var AudioContextClass = window.AudioContext || window.webkitAudioContext;
      audioContext = new AudioContextClass();
      source = audioContext.createMediaStreamSource(stream);
      processor = audioContext.createScriptProcessor(4096, 1, 1);
      var chunks = [];
      var length = 0;

      processor.onaudioprocess = function (processEvent) {
        if (!recording) {
          return;
        }
        var input = processEvent.inputBuffer.getChannelData(0);
        var copy = new Float32Array(input.length);
        copy.set(input);
        chunks.push(copy);
        length += copy.length;
        updateVuLevel(input);

        var output = processEvent.outputBuffer.getChannelData(0);
        output.fill(0);
      };

      source.connect(processor);
      processor.connect(audioContext.destination);
      recorder = {
        audioContext: audioContext,
        chunks: chunks,
        length: length,
        processor: processor,
        sampleRate: audioContext.sampleRate,
        source: source,
        stream: stream
      };

      Object.defineProperty(recorder, "length", {
        get: function () {
          return length;
        }
      });

      setRecordSetupPending(false);
      setRecording(true);
      log("Recording started at " + audioContext.sampleRate + " Hz");
      if (recordStopRequested) {
        await stopRecording();
      }
    } catch (error) {
      await cleanupRecorderResources({
        audioContext: audioContext,
        processor: processor,
        source: source,
        stream: stream
      });
      recordStopRequested = false;
      setRecordSetupPending(false);
      setRecording(false);
      setError(error);
    }
  }

  async function stopRecording() {
    if (recordSetupPending && !recording) {
      recordStopRequested = true;
      return;
    }
    if (!recording || !recorder) {
      return;
    }

    var current = recorder;
    recordStopRequested = false;
    setRecording(false);
    resetVuLevel();
    await cleanupRecorderResources(current);

    var samples = mergeChunks(current.chunks, current.length);
    if (samples.length < current.sampleRate / 4) {
      recorder = null;
      setError(new Error("Recording is too short"));
      return;
    }

    var wavBlob = encodeWav(samples, current.sampleRate);
    var file = new File([wavBlob], "recording.wav", { type: "audio/wav" });
    setActiveWav(file, "recording");
    recorder = null;
  }

  function cloneRecordLabel() {
    var elapsed = Math.max(0, (Date.now() - cloneStartedAt) / 1000);
    var minutes = Math.floor(elapsed / 60);
    var seconds = Math.floor(elapsed % 60);
    return "Recording " + minutes + ":" + (seconds < 10 ? "0" : "") + seconds + " · release to stop";
  }

  function stopCloneTimer() {
    if (cloneTimer) {
      window.clearInterval(cloneTimer);
      cloneTimer = 0;
    }
  }

  function setCloneRecording(value) {
    cloneRecording = value;
    cloneRecordButton.classList.toggle("recording", value);
    cloneRecordButton.setAttribute("aria-pressed", value ? "true" : "false");
    if (value) {
      cloneStartedAt = Date.now();
      cloneRecordButton.textContent = cloneRecordLabel();
      stopCloneTimer();
      cloneTimer = window.setInterval(function () {
        cloneRecordButton.textContent = cloneRecordLabel();
      }, 250);
    } else {
      stopCloneTimer();
      cloneRecordButton.textContent = "Push to record reference";
    }
    syncControls();
  }

  function setCloneSetupPending(value) {
    cloneSetupPending = value;
    if (value) {
      cloneRecordButton.textContent = "Preparing...";
    } else if (cloneRecording) {
      cloneRecordButton.textContent = cloneRecordLabel();
    } else {
      cloneRecordButton.textContent = "Push to record reference";
    }
    syncControls();
  }

  async function startCloneRecording(event) {
    if (event && cloneRecordButton.setPointerCapture && event.pointerId !== undefined) {
      cloneRecordButton.setPointerCapture(event.pointerId);
    }
    clearCloneError();
    if (!canRecord()) {
      setCloneError(new Error("Audio recording is not available in this browser"));
      return;
    }
    if (cloneRecording || recording || recordSetupPending || live || running) {
      return;
    }

    cloneStopRequested = false;
    setCloneSetupPending(true);
    var stream = null;
    var audioContext = null;
    var source = null;
    var processor = null;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true
        }
      });
      if (cloneStopRequested) {
        await cleanupRecorderResources({ stream: stream });
        cloneStopRequested = false;
        setCloneSetupPending(false);
        log("Reference recording cancelled before microphone setup completed");
        return;
      }
      var AudioContextClass = window.AudioContext || window.webkitAudioContext;
      audioContext = new AudioContextClass();
      source = audioContext.createMediaStreamSource(stream);
      processor = audioContext.createScriptProcessor(4096, 1, 1);
      var chunks = [];
      var length = 0;

      processor.onaudioprocess = function (processEvent) {
        if (!cloneRecording) {
          return;
        }
        var input = processEvent.inputBuffer.getChannelData(0);
        var copy = new Float32Array(input.length);
        copy.set(input);
        chunks.push(copy);
        length += copy.length;
        updateVuInto(cloneVuLevel, input);

        processEvent.outputBuffer.getChannelData(0).fill(0);
      };

      source.connect(processor);
      processor.connect(audioContext.destination);
      cloneRecorder = {
        audioContext: audioContext,
        chunks: chunks,
        processor: processor,
        sampleRate: audioContext.sampleRate,
        source: source,
        stream: stream
      };
      Object.defineProperty(cloneRecorder, "length", {
        get: function () {
          return length;
        }
      });

      setCloneSetupPending(false);
      setCloneRecording(true);
      log("Reference recording started at " + audioContext.sampleRate + " Hz");
      if (cloneStopRequested) {
        await stopCloneRecording();
      }
    } catch (error) {
      await cleanupRecorderResources({
        audioContext: audioContext,
        processor: processor,
        source: source,
        stream: stream
      });
      cloneStopRequested = false;
      setCloneSetupPending(false);
      setCloneRecording(false);
      setCloneError(error);
    }
  }

  async function stopCloneRecording() {
    if (cloneSetupPending && !cloneRecording) {
      cloneStopRequested = true;
      return;
    }
    if (!cloneRecording || !cloneRecorder) {
      return;
    }

    var current = cloneRecorder;
    cloneStopRequested = false;
    setCloneRecording(false);
    resetVuInto(cloneVuLevel);
    await cleanupRecorderResources(current);

    var samples = mergeChunks(current.chunks, current.length);
    if (samples.length < current.sampleRate) {
      cloneRecorder = null;
      setCloneError(new Error("Reference recording is too short; aim for 5-15 seconds"));
      return;
    }

    var wavBlob = encodeWav(samples, current.sampleRate);
    var file = new File([wavBlob], "reference.wav", { type: "audio/wav" });
    setCloneWav(file, "recording");
    cloneRecorder = null;
  }

  function chooseCloneWav(event) {
    clearCloneError();
    var file = event.target.files && event.target.files[0];
    if (!file) {
      return;
    }
    var name = file.name || "reference.wav";
    var looksLikeWav = /\.wav$/i.test(name) || /wav/i.test(file.type || "");
    if (!looksLikeWav) {
      event.target.value = "";
      setCloneError(new Error("Choose a WAV file"));
      return;
    }
    setCloneWav(file, "upload");
  }

  function downloadURL(url, filename) {
    var link = document.createElement("a");
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    link.remove();
  }

  function updateVuInto(element, samples) {
    var peak = 0;
    for (var i = 0; i < samples.length; i += 1) {
      var value = Math.abs(samples[i]);
      if (value > peak) {
        peak = value;
      }
    }
    var percent = Math.min(100, Math.round(peak * 140));
    element.style.width = percent + "%";
    element.classList.toggle("hot", percent > 92);
  }

  function resetVuInto(element) {
    element.style.width = "0%";
    element.classList.remove("hot");
  }

  function updateVuLevel(samples) {
    updateVuInto(vuLevel, samples);
  }

  function resetVuLevel() {
    resetVuInto(vuLevel);
  }

  function renderConversation() {
    var exchanges = conversation.length / 2;
    convoStatus.textContent = exchanges === 0 ? "No turns yet" : exchanges + (exchanges === 1 ? " exchange of context" : " exchanges of context");
    convoList.textContent = "";
    convoList.hidden = conversation.length === 0;
    conversation.forEach(function (turn) {
      var item = createElement("div", "convo-turn " + turn.role);
      item.appendChild(createElement("span", "convo-role", turn.role === "user" ? "You" : "Assistant"));
      item.appendChild(createElement("p", "convo-text", turn.text));
      convoList.appendChild(item);
    });
    convoList.scrollTop = convoList.scrollHeight;
  }

  function recordExchange(transcript, reply) {
    conversation.push({ role: "user", text: transcript });
    conversation.push({ role: "assistant", text: reply });
    while (conversation.length > CONVO_MAX_TURNS) {
      conversation.shift();
      conversation.shift();
    }
    renderConversation();
  }

  function startNewConversation() {
    conversation = [];
    renderConversation();
    resetOutputs();
    log("Started a new conversation");
  }

  function liveLabel() {
    var elapsed = Math.max(0, (Date.now() - liveStartedAt) / 1000);
    var minutes = Math.floor(elapsed / 60);
    var seconds = Math.floor(elapsed % 60);
    return "Stop live " + minutes + ":" + (seconds < 10 ? "0" : "") + seconds;
  }

  function setLive(value) {
    live = value;
    liveButton.classList.toggle("active", value);
    liveButton.setAttribute("aria-pressed", value ? "true" : "false");
    liveButton.textContent = value ? liveLabel() : "Live transcribe";
    syncControls();
  }

  function stopLiveTimers() {
    if (liveLabelTimer) {
      window.clearInterval(liveLabelTimer);
      liveLabelTimer = 0;
    }
    if (livePassTimer) {
      window.clearInterval(livePassTimer);
      livePassTimer = 0;
    }
  }

  function downsampleForLive(samples, fromRate) {
    var ratio = fromRate / LIVE_TARGET_RATE;
    var length = Math.floor(samples.length / ratio);
    var output = new Float32Array(length);
    for (var i = 0; i < length; i += 1) {
      var pos = i * ratio;
      var left = Math.floor(pos);
      var right = Math.min(left + 1, samples.length - 1);
      var frac = pos - left;
      output[i] = samples[left] * (1 - frac) + samples[right] * frac;
    }
    return output;
  }

  async function transcribeLiveTake(capture, finalPass) {
    if (livePassBusy || !capture) {
      return;
    }
    if (capture.length < capture.sampleRate / 2) {
      return;
    }
    livePassBusy = true;
    try {
      var samples = mergeChunks(capture.chunks, capture.length);
      var rate = capture.sampleRate;
      if (rate > LIVE_TARGET_RATE) {
        samples = downsampleForLive(samples, rate);
        rate = LIVE_TARGET_RATE;
      }
      var blob = encodeWav(samples, rate);
      var form = new FormData();
      form.append("file", new File([blob], "live.wav", { type: "audio/wav" }));
      var response = await fetch("/v1/audio/transcriptions", { method: "POST", body: form });
      if (response.status === 429 && !finalPass) {
        return;
      }
      await ensureOk(response, "Live transcription");
      var data = await response.json();
      transcriptOutput.value = data.text || "";
      liveFailures = 0;
    } catch (error) {
      liveFailures += 1;
      if (finalPass) {
        setError(error);
      } else {
        log("Live transcription pass failed: " + (error && error.message ? error.message : String(error)), "error");
      }
    } finally {
      livePassBusy = false;
    }
  }

  async function startLive() {
    if (live || running || recording || recordSetupPending) {
      return;
    }
    if (!canRecord()) {
      setError(new Error("Audio recording is not available in this browser"));
      return;
    }
    clearError();
    liveButton.disabled = true;
    var stream = null;
    var audioContext = null;
    var source = null;
    var processor = null;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true
        }
      });
      var AudioContextClass = window.AudioContext || window.webkitAudioContext;
      audioContext = new AudioContextClass();
      source = audioContext.createMediaStreamSource(stream);
      processor = audioContext.createScriptProcessor(4096, 1, 1);
      var capture = {
        active: true,
        audioContext: audioContext,
        chunks: [],
        length: 0,
        processor: processor,
        sampleRate: audioContext.sampleRate,
        source: source,
        stream: stream
      };

      processor.onaudioprocess = function (processEvent) {
        if (!capture.active) {
          return;
        }
        var input = processEvent.inputBuffer.getChannelData(0);
        var copy = new Float32Array(input.length);
        copy.set(input);
        capture.chunks.push(copy);
        capture.length += copy.length;
        updateVuLevel(input);

        processEvent.outputBuffer.getChannelData(0).fill(0);
      };

      source.connect(processor);
      processor.connect(audioContext.destination);

      resetOutputs();
      clearActiveWav();
      liveCapture = capture;
      liveFailures = 0;
      liveStartedAt = Date.now();
      setLive(true);
      liveLabelTimer = window.setInterval(function () {
        liveButton.textContent = liveLabel();
      }, 250);
      livePassTimer = window.setInterval(function () {
        if (liveFailures >= LIVE_MAX_FAILURES) {
          log("Live transcription stopped after repeated failures", "error");
          stopLive();
          return;
        }
        if ((Date.now() - liveStartedAt) / 1000 >= LIVE_MAX_SECONDS) {
          log("Live transcription reached the " + LIVE_MAX_SECONDS + "s cap");
          stopLive();
          return;
        }
        transcribeLiveTake(liveCapture, false);
      }, LIVE_TICK_MS);
      log("Live transcription started at " + audioContext.sampleRate + " Hz");
    } catch (error) {
      await cleanupRecorderResources({
        audioContext: audioContext,
        processor: processor,
        source: source,
        stream: stream
      });
      liveCapture = null;
      setLive(false);
      setError(error);
    }
  }

  async function stopLive() {
    if (!live || !liveCapture) {
      return;
    }
    var capture = liveCapture;
    liveCapture = null;
    capture.active = false;
    stopLiveTimers();
    setLive(false);
    resetVuLevel();
    liveButton.disabled = true;
    try {
      await cleanupRecorderResources(capture);

      var waited = 0;
      while (livePassBusy && waited < 15000) {
        await new Promise(function (resolve) {
          window.setTimeout(resolve, 100);
        });
        waited += 100;
      }

      if (capture.length < capture.sampleRate / 4) {
        log("Live take was too short to keep");
        return;
      }

      var samples = mergeChunks(capture.chunks, capture.length);
      var wavBlob = encodeWav(samples, capture.sampleRate);
      var file = new File([wavBlob], "live-take.wav", { type: "audio/wav" });
      setActiveWav(file, "live");
      await transcribeLiveTake(capture, true);
      log("Live take ready: " + (capture.length / capture.sampleRate).toFixed(1) + "s");
    } finally {
      syncControls();
    }
  }

  function chooseWav(event) {
    clearError();
    var file = event.target.files && event.target.files[0];
    if (!file) {
      return;
    }
    var name = file.name || "upload.wav";
    var looksLikeWav = /\.wav$/i.test(name) || /wav/i.test(file.type || "");
    if (!looksLikeWav) {
      event.target.value = "";
      setError(new Error("Choose a WAV file"));
      return;
    }
    setActiveWav(file, "upload");
  }

  function clearAll() {
    clearActiveWav();
    messageInput.value = "";
    resetOutputs();
    log("Cleared workspace");
  }

  // clearEverything resets every panel to a fresh page: inputs, outputs,
  // conversation, and the session log. Stored voices and retained stories
  // are library data, not page state, so they stay; a story mid-generation
  // keeps running.
  function clearEverything() {
    clearActiveWav();
    messageInput.value = "";
    resetOutputs();
    conversation = [];
    renderConversation();

    clearCloneWav();
    cloneNameInput.value = "";
    clearCloneError();
    speakTextInput.value = "";
    clearSpeakAudio();
    clonePreviewAudio.removeAttribute("src");
    clonePreviewAudio.load();

    designDescriptionInput.value = "";
    designNameInput.value = "";
    clearDesignError();
    clearDesignCandidate();

    imagePromptInput.value = "";
    clearImageOutput();

    if (!activeStoryID) {
      setStoryStatus("Idle", 0);
      storyAudio.removeAttribute("src");
      storyAudio.load();
      saveStoryButton.disabled = true;
      storyFacts.textContent = "";
      clearStoryError();
      discardDraft();
      resetCast();
      resetSources();
    }

    logOutput.textContent = "";
    log("Cleared the workspace");
  }

  function syncSizePresets() {
    var current = imageSizeInput.value.trim();
    sizePresets.forEach(function (preset) {
      preset.classList.toggle("active", preset.dataset.size === current);
    });
  }

  function submitOnCtrlEnter(input, form) {
    input.addEventListener("keydown", function (event) {
      if ((event.ctrlKey || event.metaKey) && event.key === "Enter" && !input.disabled) {
        event.preventDefault();
        form.requestSubmit();
      }
    });
  }

  healthButton.addEventListener("click", function () {
    refreshHealth(false);
  });
  voiceForm.addEventListener("submit", runVoiceLoop);
  imageForm.addEventListener("submit", generateImage);
  storyForm.addEventListener("submit", startStory);
  storyCancelButton.addEventListener("click", cancelStory);
  storyLibraryButton.addEventListener("click", function () {
    refreshStoryLibrary(false);
  });
  wavInput.addEventListener("change", chooseWav);
  clearButton.addEventListener("click", clearAll);
  newConvoButton.addEventListener("click", startNewConversation);
  saveReplyButton.addEventListener("click", function () {
    if (replyAudio.src) {
      downloadURL(replyAudio.src, "voice-reply.wav");
    }
  });
  saveImageButton.addEventListener("click", function () {
    if (imagePreview.src) {
      downloadURL(imagePreview.src, "generated-image.png");
    }
  });
  saveStoryButton.addEventListener("click", function () {
    var src = storyAudio.currentSrc || storyAudio.src;
    if (src) {
      downloadURL(src, "story.wav");
    }
  });
  wavClearButton.addEventListener("click", function () {
    if (!activeWavFile) {
      return;
    }
    clearActiveWav();
    log("Removed WAV source");
  });
  wavSaveButton.addEventListener("click", function () {
    if (activeWavFile) {
      downloadFile(activeWavFile, "recording.wav");
    }
  });
  cloneWavSaveButton.addEventListener("click", function () {
    if (cloneWavFile) {
      downloadFile(cloneWavFile, "reference.wav");
    }
  });
  clearAllButton.addEventListener("click", clearEverything);
  clearImageButton.addEventListener("click", function () {
    clearImageOutput();
    log("Cleared image");
  });
  imageFileInput.addEventListener("change", chooseImageFile);
  describeImageButton.addEventListener("click", describeImage);
  saveDescriptionButton.addEventListener("click", function () {
    if (describeAudio.src) {
      downloadURL(describeAudio.src, "image-description.wav");
    }
  });
  clearLogButton.addEventListener("click", function () {
    logOutput.textContent = "";
  });

  sizePresets.forEach(function (preset) {
    preset.addEventListener("click", function () {
      imageSizeInput.value = preset.dataset.size;
      syncSizePresets();
    });
  });
  imageSizeInput.addEventListener("input", syncSizePresets);

  submitOnCtrlEnter(messageInput, voiceForm);
  submitOnCtrlEnter(imagePromptInput, imageForm);
  submitOnCtrlEnter(speakTextInput, cloneSpeakForm);
  submitOnCtrlEnter(designDescriptionInput, designForm);

  storyVoiceSelect.addEventListener("change", syncControls);
  storyDraftButton.addEventListener("click", draftStory);
  scriptDiscardButton.addEventListener("click", function () {
    discardDraft();
    log("Draft discarded");
  });
  castAddButton.addEventListener("click", function () {
    if (castList.children.length >= STORY_MAX_CAST) {
      setStoryError(new Error("A story can have at most " + STORY_MAX_CAST + " speakers"));
      return;
    }
    addCastRow(null);
  });
  sourceAddButton.addEventListener("click", function () {
    if (sourceStack.children.length >= STORY_MAX_SOURCES) {
      setStoryError(new Error("A story can have at most " + STORY_MAX_SOURCES + " sources"));
      return;
    }
    addSourceRow(null, true);
  });

  liveButton.addEventListener("click", function () {
    if (live) {
      stopLive();
    } else {
      startLive();
    }
  });

  recordButton.addEventListener("pointerdown", startRecording);
  recordButton.addEventListener("pointerup", stopRecording);
  recordButton.addEventListener("pointercancel", stopRecording);
  recordButton.addEventListener("lostpointercapture", stopRecording);

  cloneForm.addEventListener("submit", saveClone);
  designForm.addEventListener("submit", generateDesign);
  designSaveButton.addEventListener("click", saveDesign);
  saveDesignWavButton.addEventListener("click", function () {
    if (designAudio.src) {
      downloadURL(designAudio.src, "designed-voice.wav");
    }
  });
  cloneSpeakForm.addEventListener("submit", speakText);
  saveSpeakButton.addEventListener("click", function () {
    if (speakAudio.src) {
      downloadURL(speakAudio.src, "spoken-text.wav");
    }
  });
  cloneWavInput.addEventListener("change", chooseCloneWav);
  cloneWavClearButton.addEventListener("click", function () {
    if (!cloneWavFile) {
      return;
    }
    clearCloneWav();
    log("Removed reference WAV");
  });
  voiceLibraryButton.addEventListener("click", function () {
    refreshVoices(false);
  });
  voiceSelect.addEventListener("change", function () {
    selectVoice(voiceSelect.value);
  });
  cloneRecordButton.addEventListener("pointerdown", startCloneRecording);
  cloneRecordButton.addEventListener("pointerup", stopCloneRecording);
  cloneRecordButton.addEventListener("pointercancel", stopCloneRecording);
  cloneRecordButton.addEventListener("lostpointercapture", stopCloneRecording);

  if (!canRecord()) {
    recordButton.disabled = true;
    recordButton.textContent = "Recording unavailable";
    liveButton.disabled = true;
    cloneRecordButton.disabled = true;
    cloneRecordButton.textContent = "Recording unavailable";
  }

  // --- Tabs: hash-routed sections -------------------------------------
  // Every module carries data-tab; the router shows the active tab's
  // modules and hides the rest. The session log drawer sits outside the
  // tab system and stays visible everywhere.
  var TABS = ["talk", "voices", "image", "story", "audiobook", "extract", "library", "models", "engines"];
  var tabLinks = document.querySelectorAll("[data-tab-link]");
  var tabModules = document.querySelectorAll(".module[data-tab]");

  function activeTabFromHash() {
    var name = (window.location.hash || "").replace("#", "");
    return TABS.indexOf(name) >= 0 ? name : "talk";
  }

  function applyTab(name) {
    tabModules.forEach(function (module) {
      module.hidden = module.getAttribute("data-tab") !== name;
    });
    tabLinks.forEach(function (link) {
      var active = link.getAttribute("data-tab-link") === name;
      link.classList.toggle("active", active);
      if (active) {
        link.setAttribute("aria-current", "page");
      } else {
        link.removeAttribute("aria-current");
      }
    });
    if (name === "models") {
      refreshModels(true);
    }
    if (name === "engines") {
      refreshGPU();
    }
    if (name === "library") {
      refreshLibrary(true);
    }
    if (name === "audiobook") {
      refreshAudiobooks(true);
    }
    if (name === "extract" && ex.samples) {
      // The canvas has zero width while its tab is hidden; repaint on entry.
      window.setTimeout(drawExtractWave, 0);
    }
  }

  window.addEventListener("hashchange", function () {
    applyTab(activeTabFromHash());
  });

  // --- Session log drawer ----------------------------------------------
  var logToggleButton = document.getElementById("logToggleButton");
  var logDrawerBody = document.getElementById("logDrawerBody");
  logToggleButton.addEventListener("click", function () {
    var open = logDrawerBody.hidden;
    logDrawerBody.hidden = !open;
    logToggleButton.textContent = open ? "Hide" : "Show";
    logToggleButton.setAttribute("aria-expanded", open ? "true" : "false");
  });

  // --- Models catalog ---------------------------------------------------
  var modelsRefreshButton = document.getElementById("modelsRefreshButton");
  var modelsSummary = document.getElementById("modelsSummary");
  var modelsList = document.getElementById("modelsList");

  function formatBytes(bytes) {
    if (!bytes || bytes <= 0) {
      return "–";
    }
    var units = ["B", "KB", "MB", "GB", "TB"];
    var i = 0;
    var v = bytes;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i += 1;
    }
    return (v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)) + " " + units[i];
  }

  function modelStateClass(state) {
    if (state === "present" || state === "verified") {
      return "ready";
    }
    if (state === "unverified") {
      return "warn";
    }
    return "danger";
  }

  function renderModels(payload) {
    var models = (payload && payload.models) || [];
    modelsList.textContent = "";
    if (!models.length) {
      modelsSummary.textContent = "No models declared. Add a models block to the gateway config to enable the catalog.";
      return;
    }
    var present = models.filter(function (m) { return m.state === "present" || m.state === "verified" || m.state === "unverified"; }).length;
    var totalBytes = models.reduce(function (sum, m) { return sum + (m.bytes || m.actualBytes || 0); }, 0);
    modelsSummary.textContent = present + " of " + models.length + " models on disk · " + formatBytes(totalBytes) + " total · root: " + (payload.root || "–");

    models.forEach(function (model) {
      var row = document.createElement("div");
      row.className = "model-row";

      var head = document.createElement("div");
      head.className = "model-row-head";
      var name = document.createElement("span");
      name.className = "model-name";
      name.textContent = model.id;
      var state = document.createElement("span");
      state.className = "status-pill " + modelStateClass(model.state);
      state.textContent = model.state;
      head.appendChild(name);
      head.appendChild(state);

      var meta = document.createElement("div");
      meta.className = "model-meta";
      var engineTag = document.createElement("span");
      engineTag.textContent = model.engine + " · " + (model.family || "");
      var sizeTag = document.createElement("span");
      sizeTag.textContent = formatBytes(model.bytes || model.actualBytes);
      meta.appendChild(engineTag);
      meta.appendChild(sizeTag);
      if (model.license) {
        var licenseTag = document.createElement("span");
        licenseTag.textContent = model.license;
        meta.appendChild(licenseTag);
      }

      var pathLine = document.createElement("div");
      pathLine.className = "model-path";
      pathLine.textContent = model.path;

      row.appendChild(head);
      row.appendChild(meta);
      row.appendChild(pathLine);
      if (model.description) {
        var desc = document.createElement("div");
        desc.className = "model-desc";
        desc.textContent = model.description;
        row.appendChild(desc);
      }
      if (model.source) {
        var source = document.createElement("a");
        source.className = "model-source";
        source.href = model.source;
        source.target = "_blank";
        source.rel = "noreferrer noopener";
        source.textContent = model.source;
        row.appendChild(source);
      }
      modelsList.appendChild(row);
    });
  }

  async function refreshModels(silent) {
    try {
      var response = await fetch("/v1/models/catalog", { method: "GET" });
      if (!response.ok) {
        throw new Error("catalog returned " + response.status);
      }
      renderModels(await response.json());
      if (!silent) {
        log("Model catalog refreshed");
      }
    } catch (err) {
      modelsSummary.textContent = "Catalog unavailable: " + err.message;
      if (!silent) {
        log("Model catalog failed: " + err.message, "error");
      }
    }
  }

  modelsRefreshButton.addEventListener("click", function () {
    refreshModels(false);
  });

  // --- Verify all: deep integrity check as a tracked job ----------------
  var modelsVerifyButton = document.getElementById("modelsVerifyButton");

  function pollVerify(id) {
    window.setTimeout(async function () {
      try {
        var response = await fetch("/v1/jobs/" + encodeURIComponent(id), { method: "GET" });
        if (!response.ok) {
          throw new Error(await readErrorBody(response));
        }
        var job = await response.json();
        if (job.status === "complete") {
          modelsVerifyButton.textContent = "Verify all";
          modelsVerifyButton.disabled = false;
          var corrupt = job.result && job.result.corrupt;
          log("Model verification: " + (job.result ? job.result.verified + "/" + job.result.total : "?") + " verified" + (corrupt ? "; CORRUPT: " + corrupt : ""), corrupt ? "error" : undefined);
          refreshModels(true);
          return;
        }
        if (job.status === "failed" || job.status === "cancelled") {
          modelsVerifyButton.textContent = "Verify all";
          modelsVerifyButton.disabled = false;
          modelsSummary.textContent = "Verification " + job.status + (job.error ? ": " + job.error : "");
          log("Model verification " + job.status + (job.error ? ": " + job.error : ""), "error");
          return;
        }
        modelsVerifyButton.textContent = "Verifying " + Math.round((job.progress || 0) * 100) + "%";
        if (job.detail) {
          modelsSummary.textContent = job.detail + "…";
        }
        pollVerify(id);
      } catch (err) {
        modelsVerifyButton.textContent = "Verify all";
        modelsVerifyButton.disabled = false;
        log("Verification status failed: " + err.message, "error");
      }
    }, 700);
  }

  modelsVerifyButton.addEventListener("click", async function () {
    modelsVerifyButton.disabled = true;
    modelsVerifyButton.textContent = "Verifying…";
    try {
      var response = await fetch("/v1/models/verify", { method: "POST" });
      if (!response.ok) {
        throw new Error(await readErrorBody(response));
      }
      var created = await response.json();
      log("Model verification started (full checksums; this reads every model once)");
      pollVerify(created.id);
    } catch (err) {
      modelsVerifyButton.textContent = "Verify all";
      modelsVerifyButton.disabled = false;
      log("Verification failed to start: " + err.message, "error");
    }
  });

  // --- Audiobook desk ----------------------------------------------------
  var audiobookForm = document.getElementById("audiobookForm");
  var audiobookFileInput = document.getElementById("audiobookFileInput");
  var audiobookFileStatus = document.getElementById("audiobookFileStatus");
  var audiobookTitleInput = document.getElementById("audiobookTitleInput");
  var audiobookVoiceSelect = document.getElementById("audiobookVoiceSelect");
  var audiobookNarrateButton = document.getElementById("audiobookNarrateButton");
  var audiobookCancelButton = document.getElementById("audiobookCancelButton");
  var audiobookErrorBox = document.getElementById("audiobookErrorBox");
  var audiobookStatus = document.getElementById("audiobookStatus");
  var audiobookProgress = document.getElementById("audiobookProgress");
  var audiobookAudio = document.getElementById("audiobookAudio");
  var audiobookSaveButton = document.getElementById("audiobookSaveButton");
  var audiobookRefreshButton = document.getElementById("audiobookRefreshButton");
  var audiobookShelf = document.getElementById("audiobookShelf");
  var activeBookId = "";
  var bookPollTimer = null;

  function renderAudiobookVoices(voices) {
    var previous = audiobookVoiceSelect.value;
    audiobookVoiceSelect.textContent = "";
    var fallback = createElement("option", "", "Studio default");
    fallback.value = "";
    audiobookVoiceSelect.appendChild(fallback);
    (voices || []).forEach(function (clone) {
      var option = createElement("option", "", clone.name || clone.id);
      option.value = clone.id;
      audiobookVoiceSelect.appendChild(option);
    });
    audiobookVoiceSelect.value = previous;
    if (audiobookVoiceSelect.value !== previous) {
      audiobookVoiceSelect.value = "";
    }
  }

  audiobookFileInput.addEventListener("change", function () {
    var file = audiobookFileInput.files && audiobookFileInput.files[0];
    audiobookFileStatus.textContent = file ? file.name + " (" + Math.round(file.size / 1024) + " KB)" : "None";
  });

  function setAudiobookBusy(busy) {
    audiobookNarrateButton.disabled = busy;
    audiobookCancelButton.disabled = !busy;
  }

  function pollAudiobook(id) {
    window.clearTimeout(bookPollTimer);
    bookPollTimer = window.setTimeout(async function () {
      try {
        var response = await fetch("/v1/jobs/" + encodeURIComponent(id), { method: "GET" });
        if (!response.ok) {
          throw new Error(await readErrorBody(response));
        }
        var job = await response.json();
        audiobookProgress.value = job.progress || 0;
        audiobookStatus.textContent = job.detail || job.status;
        if (job.status === "complete") {
          audiobookStatus.textContent = "Complete";
          audiobookProgress.value = 1;
          setAudiobookBusy(false);
          activeBookId = "";
          if (job.result && job.result.artifactUrl) {
            audiobookAudio.src = job.result.artifactUrl;
            audiobookSaveButton.disabled = false;
            audiobookSaveButton.dataset.url = job.result.artifactUrl;
            audiobookSaveButton.dataset.title = job.result.title || "audiobook";
          }
          log("Audiobook complete: " + ((job.result && job.result.title) || id));
          refreshAudiobooks(true);
          return;
        }
        if (job.status === "failed" || job.status === "cancelled") {
          audiobookStatus.textContent = job.status + (job.error ? ": " + job.error : "");
          setAudiobookBusy(false);
          activeBookId = "";
          if (job.status === "failed") {
            log("Audiobook failed: " + job.error, "error");
          }
          return;
        }
        pollAudiobook(id);
      } catch (err) {
        audiobookStatus.textContent = "Status check failed: " + err.message;
        setAudiobookBusy(false);
        activeBookId = "";
      }
    }, 1200);
  }

  audiobookForm.addEventListener("submit", async function (event) {
    event.preventDefault();
    audiobookErrorBox.hidden = true;
    var file = audiobookFileInput.files && audiobookFileInput.files[0];
    if (!file) {
      audiobookErrorBox.textContent = "Choose a .txt, .md, or .epub document first.";
      audiobookErrorBox.hidden = false;
      return;
    }
    var form = new FormData();
    form.append("file", file, file.name);
    if (audiobookTitleInput.value.trim()) {
      form.append("title", audiobookTitleInput.value.trim());
    }
    if (audiobookVoiceSelect.value) {
      form.append("voice", audiobookVoiceSelect.value);
    }
    setAudiobookBusy(true);
    audiobookStatus.textContent = "Uploading…";
    audiobookProgress.value = 0;
    try {
      var response = await fetch("/v1/audiobooks", { method: "POST", body: form });
      if (!response.ok) {
        throw new Error(await readErrorBody(response));
      }
      var created = await response.json();
      activeBookId = created.id;
      audiobookStatus.textContent = "Narrating " + created.chunks + " chunks…";
      log("Audiobook started: " + created.id + " (" + created.chunks + " chunks)");
      pollAudiobook(created.id);
    } catch (err) {
      audiobookErrorBox.textContent = err.message;
      audiobookErrorBox.hidden = false;
      audiobookStatus.textContent = "Idle";
      setAudiobookBusy(false);
      log("Audiobook failed to start: " + err.message, "error");
    }
  });

  audiobookCancelButton.addEventListener("click", async function () {
    if (!activeBookId) {
      return;
    }
    try {
      await fetch("/v1/jobs/" + encodeURIComponent(activeBookId) + "/cancel", { method: "POST" });
    } catch (err) {
      /* the poll reports the outcome */
    }
  });

  audiobookSaveButton.addEventListener("click", function () {
    if (audiobookSaveButton.dataset.url) {
      downloadURL(audiobookSaveButton.dataset.url, (audiobookSaveButton.dataset.title || "audiobook") + ".wav");
    }
  });

  async function refreshAudiobooks(silent) {
    try {
      var response = await fetch("/v1/audiobooks", { method: "GET" });
      if (!response.ok) {
        return;
      }
      var payload = await response.json();
      var books = payload.audiobooks || [];
      audiobookShelf.textContent = "";
      if (!books.length) {
        audiobookShelf.textContent = "No audiobooks yet.";
        return;
      }
      books.forEach(function (book) {
        var item = createElement("div", "story-library-item");
        var title = createElement("div", "story-library-title", book.title);
        var meta = createElement("div", "story-library-meta",
          new Date(book.createdAt).toLocaleString() + " · " + book.chunks + " chunks · " + Math.floor(book.durationSeconds / 60) + "m" + (book.durationSeconds % 60) + "s");
        item.appendChild(title);
        item.appendChild(meta);
        item.addEventListener("click", function () {
          audiobookAudio.src = book.artifactUrl;
          audiobookSaveButton.disabled = false;
          audiobookSaveButton.dataset.url = book.artifactUrl;
          audiobookSaveButton.dataset.title = book.title;
          audiobookStatus.textContent = "Loaded: " + book.title;
        });
        audiobookShelf.appendChild(item);
      });
      if (!silent) {
        log("Audiobook shelf refreshed");
      }
    } catch (err) {
      if (!silent) {
        log("Audiobook shelf failed: " + err.message, "error");
      }
    }
  }

  audiobookRefreshButton.addEventListener("click", function () {
    refreshAudiobooks(false);
  });

  // --- The Extractor -----------------------------------------------------
  // Load audio/video, scrub a waveform, read a whisper transcript timeline,
  // tag who's speaking, and extract clips for cloning. Speaker tags live on
  // each segment's speaker field — the same slot automatic diarization will
  // fill in a future milestone.
  var extractFileInput = document.getElementById("extractFileInput");
  var extractFileStatus = document.getElementById("extractFileStatus");
  var extractTranscribeButton = document.getElementById("extractTranscribeButton");
  var extractTranscribeStatus = document.getElementById("extractTranscribeStatus");
  var extractZoomInButton = document.getElementById("extractZoomInButton");
  var extractZoomOutButton = document.getElementById("extractZoomOutButton");
  var extractZoomFitButton = document.getElementById("extractZoomFitButton");
  var extractCanvas = document.getElementById("extractCanvas");
  var extractViewStart = document.getElementById("extractViewStart");
  var extractViewEnd = document.getElementById("extractViewEnd");
  var extractCursor = document.getElementById("extractCursor");
  var extractRegionStatus = document.getElementById("extractRegionStatus");
  var extractPlayButton = document.getElementById("extractPlayButton");
  var extractStopButton = document.getElementById("extractStopButton");
  var extractCloneButton = document.getElementById("extractCloneButton");
  var extractLibraryButton = document.getElementById("extractLibraryButton");
  var extractErrorBox = document.getElementById("extractErrorBox");
  var extractTimeline = document.getElementById("extractTimeline");
  var extractFilterRow = document.getElementById("extractFilterRow");

  var EXTRACT_MAX_SECONDS = 30 * 60;
  // Whisper end-stamps chronically clip the final phoneme; every
  // segment-derived region gets this much breathing room.
  var EXTRACT_TAIL_PAD = 0.25;
  // One shared AudioContext for decode and playback: browsers cap live
  // contexts per tab, so creating one per Play dies after a few clicks.
  var extractAudioCtx = null;
  function extractCtx() {
    var AC = window.AudioContext || window.webkitAudioContext;
    if (!extractAudioCtx || extractAudioCtx.state === "closed") {
      extractAudioCtx = new AC();
    }
    if (extractAudioCtx.state === "suspended") {
      extractAudioCtx.resume();
    }
    return extractAudioCtx;
  }
  var ex = {
    samples: null,      // mono Float32Array at ex.rate
    rate: 0,
    duration: 0,
    sourceName: "",
    view: { start: 0, end: 0 },
    cursor: 0,          // playhead position when no region is marked
    region: null,       // {start, end} seconds
    segments: [],       // {start, end, text, speaker}
    checked: {},        // segment index -> true: multi-segment selection
    filter: "",
    playback: null,     // {ctx, source, anchor, offset, raf}
    selectedRow: -1
  };

  // checkedIndices returns the multi-selection in chronological order.
  function checkedIndices() {
    return Object.keys(ex.checked).map(Number).sort(function (a, b) {
      return ex.segments[a].start - ex.segments[b].start;
    });
  }

  // checkedSpans merges the selected segments into time spans, joining
  // neighbours that touch (whisper often splits one utterance) so they
  // export seamlessly; distinct spans get a short breath of silence.
  function checkedSpans() {
    var spans = [];
    checkedIndices().forEach(function (index) {
      var segment = ex.segments[index];
      var end = Math.min(segment.end + EXTRACT_TAIL_PAD, ex.duration);
      var last = spans[spans.length - 1];
      if (last && segment.start - last.end <= 0.3) {
        last.end = Math.max(last.end, end);
      } else {
        spans.push({ start: segment.start, end: end });
      }
    });
    return spans;
  }

  // stitchSpans concatenates the spans' samples with a 200ms gap between
  // non-adjacent spans, returning one continuous take.
  function stitchSpans(spans) {
    var gap = Math.floor(0.2 * ex.rate);
    var total = 0;
    spans.forEach(function (span, i) {
      total += Math.floor((span.end - span.start) * ex.rate) + (i > 0 ? gap : 0);
    });
    var out = new Float32Array(total);
    var offset = 0;
    spans.forEach(function (span, i) {
      if (i > 0) {
        offset += gap;
      }
      var s0 = Math.floor(span.start * ex.rate);
      var s1 = Math.floor(span.end * ex.rate);
      out.set(ex.samples.subarray(s0, s1), offset);
      offset += s1 - s0;
    });
    return out;
  }

  function extractError(message) {
    extractErrorBox.textContent = message;
    extractErrorBox.hidden = false;
    log("Extractor: " + message, "error");
  }

  function fmtTime(seconds) {
    var m = Math.floor(seconds / 60);
    var s = seconds - m * 60;
    return m + ":" + (s < 10 ? "0" : "") + s.toFixed(1);
  }

  async function extractLoadFile(file) {
    extractErrorBox.hidden = true;
    extractFileStatus.textContent = "Decoding " + file.name + "…";
    try {
      var buffer = await file.arrayBuffer();
      var decoded = await extractCtx().decodeAudioData(buffer);
      if (decoded.duration > EXTRACT_MAX_SECONDS) {
        extractFileStatus.textContent = "Nothing loaded";
        extractError("That file is " + fmtTime(decoded.duration) + " long; the in-browser editor caps at 30 minutes. Trim or split it first.");
        return;
      }
      // Mix down to mono once; every later operation reads this array.
      var mono = new Float32Array(decoded.length);
      for (var c = 0; c < decoded.numberOfChannels; c += 1) {
        var channel = decoded.getChannelData(c);
        for (var i = 0; i < channel.length; i += 1) {
          mono[i] += channel[i] / decoded.numberOfChannels;
        }
      }
      extractStopPlayback();
      ex.samples = mono;
      ex.rate = decoded.sampleRate;
      ex.duration = decoded.duration;
      ex.sourceName = file.name;
      ex.view = { start: 0, end: decoded.duration };
      ex.cursor = 0;
      ex.region = null;
      ex.segments = [];
      ex.checked = {};
      ex.filter = "";
      ex.selectedRow = -1;
      extractFileStatus.textContent = file.name + " · " + fmtTime(decoded.duration) + " · " + decoded.sampleRate + " Hz";
      extractTranscribeButton.disabled = false;
      extractDiarizeButton.disabled = false;
      extractZoomInButton.disabled = false;
      extractZoomOutButton.disabled = false;
      extractZoomFitButton.disabled = false;
      extractTimeline.textContent = "No transcript yet — press Transcribe to map the speech.";
      extractFilterRow.hidden = true;
      extractTranscribeStatus.textContent = "";
      updateExtractRegionUI();
      drawExtractWave();
      log("Extractor loaded " + file.name + " (" + fmtTime(decoded.duration) + ")");
    } catch (err) {
      extractFileStatus.textContent = "Nothing loaded";
      extractError("Could not decode " + file.name + ": " + (err.message || "unsupported format") + ". Convert to WAV/MP3/OGG/FLAC first.");
    }
  }

  extractFileInput.addEventListener("change", function () {
    var file = extractFileInput.files && extractFileInput.files[0];
    if (file) {
      extractLoadFile(file);
    }
  });

  // --- waveform rendering ------------------------------------------------
  function drawExtractWave() {
    var dpr = window.devicePixelRatio || 1;
    var cssWidth = extractCanvas.clientWidth || extractCanvas.parentElement.clientWidth || 600;
    var cssHeight = 140;
    extractCanvas.width = Math.floor(cssWidth * dpr);
    extractCanvas.height = Math.floor(cssHeight * dpr);
    var g = extractCanvas.getContext("2d");
    g.scale(dpr, dpr);
    g.fillStyle = "#151a21";
    g.fillRect(0, 0, cssWidth, cssHeight);
    if (!ex.samples) {
      g.fillStyle = "#5d6775";
      g.font = "13px sans-serif";
      g.fillText("Load a file to see its waveform", 16, cssHeight / 2);
      return;
    }

    var viewStart = ex.view.start;
    var viewLen = ex.view.end - ex.view.start;
    var startSample = Math.floor(viewStart * ex.rate);
    var samplesPerPx = Math.max(1, Math.floor((viewLen * ex.rate) / cssWidth));
    var mid = cssHeight / 2;

    // Region shading under the waveform.
    if (ex.region) {
      var rx0 = ((ex.region.start - viewStart) / viewLen) * cssWidth;
      var rx1 = ((ex.region.end - viewStart) / viewLen) * cssWidth;
      g.fillStyle = "rgba(240, 166, 75, 0.16)";
      g.fillRect(rx0, 0, rx1 - rx0, cssHeight);
    }
    // Ticked-selection shading (green: what a combined export would take).
    checkedSpans().forEach(function (span) {
      if (span.end < viewStart || span.start > ex.view.end) {
        return;
      }
      var cx0 = ((span.start - viewStart) / viewLen) * cssWidth;
      var cx1 = ((span.end - viewStart) / viewLen) * cssWidth;
      g.fillStyle = "rgba(88, 201, 124, 0.14)";
      g.fillRect(cx0, 0, cx1 - cx0, cssHeight);
    });

    g.strokeStyle = "#f0a64b";
    g.lineWidth = 1;
    g.beginPath();
    for (var x = 0; x < cssWidth; x += 1) {
      var s0 = startSample + x * samplesPerPx;
      if (s0 >= ex.samples.length) {
        break;
      }
      var min = 1.0;
      var max = -1.0;
      var s1 = Math.min(s0 + samplesPerPx, ex.samples.length);
      for (var i = s0; i < s1; i += 1) {
        var v = ex.samples[i];
        if (v < min) { min = v; }
        if (v > max) { max = v; }
      }
      g.moveTo(x + 0.5, mid - max * (mid - 4));
      g.lineTo(x + 0.5, mid - min * (mid - 4));
    }
    g.stroke();

    // Segment boundary ticks along the top, tagged ones brighter.
    ex.segments.forEach(function (segment) {
      if (segment.end < viewStart || segment.start > ex.view.end) {
        return;
      }
      var sx = ((segment.start - viewStart) / viewLen) * cssWidth;
      g.fillStyle = segment.speaker ? "#ffbe73" : "#333c49";
      g.fillRect(sx, 0, 2, segment.speaker ? 14 : 8);
    });

    // Playhead while playing; parked cursor otherwise.
    if (ex.playback) {
      var t = extractPlayheadTime();
      if (t >= viewStart && t <= ex.view.end) {
        var px = ((t - viewStart) / viewLen) * cssWidth;
        g.strokeStyle = "#58c97c";
        g.lineWidth = 2;
        g.beginPath();
        g.moveTo(px, 0);
        g.lineTo(px, cssHeight);
        g.stroke();
      }
    } else if (!ex.region && ex.cursor >= viewStart && ex.cursor <= ex.view.end) {
      var cx = ((ex.cursor - viewStart) / viewLen) * cssWidth;
      g.strokeStyle = "#8d97a6";
      g.lineWidth = 1;
      g.beginPath();
      g.moveTo(cx + 0.5, 0);
      g.lineTo(cx + 0.5, cssHeight);
      g.stroke();
    }

    extractViewStart.textContent = fmtTime(viewStart);
    extractViewEnd.textContent = fmtTime(ex.view.end);
  }

  window.addEventListener("resize", function () {
    if (ex.samples && activeTabFromHash() === "extract") {
      drawExtractWave();
    }
  });

  function canvasXToTime(clientX) {
    var rect = extractCanvas.getBoundingClientRect();
    var frac = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
    return ex.view.start + frac * (ex.view.end - ex.view.start);
  }

  var extractDrag = null;
  extractCanvas.addEventListener("mousedown", function (event) {
    if (!ex.samples) {
      return;
    }
    extractDrag = { anchor: canvasXToTime(event.clientX), moved: false };
    event.preventDefault();
  });
  window.addEventListener("mousemove", function (event) {
    if (!extractDrag) {
      return;
    }
    var t = canvasXToTime(event.clientX);
    if (Math.abs(t - extractDrag.anchor) > 0.02) {
      extractDrag.moved = true;
      setExtractRegion(Math.min(extractDrag.anchor, t), Math.max(extractDrag.anchor, t), -1);
    }
  });
  window.addEventListener("mouseup", function (event) {
    if (!extractDrag) {
      return;
    }
    if (!extractDrag.moved) {
      // A plain click behaves like any audio editor: clear the selection
      // and park the playhead there — Play then plays from the cursor.
      ex.cursor = canvasXToTime(event.clientX);
      ex.region = null;
      ex.selectedRow = -1;
      extractCursor.textContent = "cursor " + fmtTime(ex.cursor);
      updateExtractRegionUI();
      renderExtractTimeline();
      drawExtractWave();
    }
    extractDrag = null;
  });

  function setExtractRegion(start, end, rowIndex) {
    ex.region = { start: Math.max(0, start), end: Math.min(ex.duration, end) };
    ex.selectedRow = rowIndex;
    updateExtractRegionUI();
    renderExtractTimeline();
    drawExtractWave();
  }

  function updateExtractRegionUI() {
    var spans = checkedSpans();
    var hasChecked = spans.length > 0;
    var hasRegion = Boolean(ex.region && ex.region.end - ex.region.start > 0.05);
    // Play works from the cursor even without a marked region; extraction
    // needs a region or a ticked selection to know what to cut. A ticked
    // selection wins over the region: it is the more deliberate act.
    extractPlayButton.disabled = !ex.samples;
    extractCloneButton.disabled = !(hasChecked || hasRegion);
    extractLibraryButton.disabled = !(hasChecked || hasRegion);
    if (hasChecked) {
      var seconds = spans.reduce(function (sum, span) { return sum + (span.end - span.start); }, 0);
      extractRegionStatus.textContent = checkedIndices().length + " segments ticked · " + spans.length + " span" + (spans.length === 1 ? "" : "s") + " · " + seconds.toFixed(1) + "s as one WAV";
    } else if (hasRegion) {
      extractRegionStatus.textContent = fmtTime(ex.region.start) + " – " + fmtTime(ex.region.end) + " (" + (ex.region.end - ex.region.start).toFixed(1) + "s)";
    } else {
      extractRegionStatus.textContent = "None — drag on the waveform, click a transcript line, or tick segments";
    }
  }

  // --- zoom --------------------------------------------------------------
  function extractZoom(factor) {
    var center = ex.region ? (ex.region.start + ex.region.end) / 2 : (ex.view.start + ex.view.end) / 2;
    var half = ((ex.view.end - ex.view.start) * factor) / 2;
    half = Math.max(0.5, Math.min(ex.duration / 2, half));
    ex.view.start = Math.max(0, center - half);
    ex.view.end = Math.min(ex.duration, center + half);
    drawExtractWave();
  }
  extractZoomInButton.addEventListener("click", function () { extractZoom(0.5); });
  extractZoomOutButton.addEventListener("click", function () { extractZoom(2); });
  extractZoomFitButton.addEventListener("click", function () {
    ex.view = { start: 0, end: ex.duration };
    drawExtractWave();
  });

  // --- playback ----------------------------------------------------------
  function extractPlayheadTime() {
    if (!ex.playback) {
      return 0;
    }
    return ex.playback.offset + (ex.playback.ctx.currentTime - ex.playback.anchor);
  }

  function extractStopPlayback() {
    if (!ex.playback) {
      return;
    }
    try { ex.playback.source.stop(); } catch (err) { /* already stopped */ }
    window.cancelAnimationFrame(ex.playback.raf);
    // The shared context stays open for the next play.
    ex.playback = null;
    extractStopButton.disabled = true;
    drawExtractWave();
  }

  extractPlayButton.addEventListener("click", function () {
    if (!ex.samples) {
      return;
    }
    // Priority: audition the ticked selection (stitched, exactly what would
    // export), else the marked region, else play from the cursor to the end.
    var spans = checkedSpans();
    var stitched = null;
    var start;
    if (spans.length > 0) {
      stitched = stitchSpans(spans);
      start = spans[0].start;
    } else {
      start = ex.region ? ex.region.start : Math.min(ex.cursor, ex.duration);
      var end = ex.region ? ex.region.end : ex.duration;
      if (end - start <= 0.01) {
        return;
      }
    }
    extractStopPlayback();
    var ctx = extractCtx();
    var length = stitched ? stitched.length : Math.floor(((ex.region ? ex.region.end : ex.duration) - start) * ex.rate);
    var buffer = ctx.createBuffer(1, length, ex.rate);
    buffer.copyToChannel(stitched || ex.samples.subarray(Math.floor(start * ex.rate), Math.floor(start * ex.rate) + length), 0);
    var source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(ctx.destination);
    ex.playback = { ctx: ctx, source: source, anchor: ctx.currentTime, offset: start, raf: 0 };
    source.onended = extractStopPlayback;
    source.start();
    extractStopButton.disabled = false;
    (function tick() {
      if (!ex.playback) {
        return;
      }
      drawExtractWave();
      ex.playback.raf = window.requestAnimationFrame(tick);
    }());
  });
  extractStopButton.addEventListener("click", extractStopPlayback);

  // --- extraction --------------------------------------------------------
  // extractSelectionWav builds the export: the ticked segments stitched into
  // one take when any are ticked, otherwise the marked region.
  function extractSelectionWav() {
    var spans = checkedSpans();
    if (spans.length > 0) {
      return encodeWav(stitchSpans(spans), ex.rate);
    }
    var s0 = Math.floor(ex.region.start * ex.rate);
    var s1 = Math.floor(ex.region.end * ex.rate);
    return encodeWav(ex.samples.subarray(s0, s1), ex.rate);
  }

  // selectionSpeaker returns the single speaker every ticked segment shares,
  // or "" when mixed/untagged.
  function selectionSpeaker() {
    var indices = checkedIndices();
    if (!indices.length) {
      var row = ex.selectedRow >= 0 && ex.segments[ex.selectedRow];
      return (row && row.speaker) || "";
    }
    var speaker = ex.segments[indices[0]].speaker;
    for (var i = 1; i < indices.length; i += 1) {
      if (ex.segments[indices[i]].speaker !== speaker) {
        return "";
      }
    }
    return speaker || "";
  }

  function extractClipName() {
    var base = ex.sourceName.replace(/\.[^.]+$/, "");
    var speaker = selectionSpeaker();
    var indices = checkedIndices();
    if (indices.length > 0) {
      var spans = checkedSpans();
      return base + " " + indices.length + " segments" + (speaker ? " (" + speaker + ")" : "") + " " + fmtTime(spans[0].start) + "-" + fmtTime(spans[spans.length - 1].end);
    }
    return base + " " + fmtTime(ex.region.start) + "-" + fmtTime(ex.region.end) + (speaker ? " (" + speaker + ")" : "");
  }

  extractCloneButton.addEventListener("click", function () {
    if (!ex.region && !checkedIndices().length) {
      return;
    }
    var blob = extractSelectionWav();
    var file = new File([blob], extractClipName().replace(/[:]/g, ".") + ".wav", { type: "audio/wav" });
    setCloneWav(file, "the Extractor");
    window.location.hash = "#voices";
    cloneNameInput.focus();
    log("Extractor clip sent to voice clone: " + file.name);
  });

  extractLibraryButton.addEventListener("click", async function () {
    var indices = checkedIndices();
    if (!ex.region && !indices.length) {
      return;
    }
    extractLibraryButton.disabled = true;
    var original = extractLibraryButton.textContent;
    extractLibraryButton.textContent = "Saving…";
    try {
      var b64 = await srcToB64(URL.createObjectURL(extractSelectionWav()));
      var meta = { source: ex.sourceName };
      if (indices.length) {
        var spans = checkedSpans();
        meta.segments = String(indices.length);
        meta.start = spans[0].start.toFixed(2);
        meta.end = spans[spans.length - 1].end.toFixed(2);
      } else {
        meta.start = ex.region.start.toFixed(2);
        meta.end = ex.region.end.toFixed(2);
      }
      if (selectionSpeaker()) {
        meta.speaker = selectionSpeaker();
      }
      var response = await fetch("/v1/library", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ kind: "audio", name: extractClipName(), data_b64: b64, meta: meta })
      });
      if (!response.ok) {
        throw new Error(await readErrorBody(response));
      }
      log("Extractor clip saved to library: " + extractClipName());
      extractLibraryButton.textContent = "Saved ✓";
      window.setTimeout(function () {
        extractLibraryButton.textContent = original;
        extractLibraryButton.disabled = false;
      }, 1500);
    } catch (err) {
      extractError("Save failed: " + err.message);
      extractLibraryButton.textContent = original;
      extractLibraryButton.disabled = false;
    }
  });

  // --- transcription -----------------------------------------------------
  var EXTRACT_CHUNK_SECONDS = 600; // 10 min of 16k mono ≈ 19MB WAV, inside the upload cap

  extractTranscribeButton.addEventListener("click", async function () {
    if (!ex.samples) {
      return;
    }
    extractErrorBox.hidden = true;
    extractTranscribeButton.disabled = true;
    ex.segments = [];
    try {
      var samples = ex.samples;
      var rate = ex.rate;
      if (rate > LIVE_TARGET_RATE) {
        samples = downsampleForLive(samples, rate);
        rate = LIVE_TARGET_RATE;
      }
      var chunkSamples = EXTRACT_CHUNK_SECONDS * rate;
      var chunks = Math.max(1, Math.ceil(samples.length / chunkSamples));
      for (var c = 0; c < chunks; c += 1) {
        var offsetSeconds = (c * chunkSamples) / rate;
        extractTranscribeStatus.textContent = "Transcribing " + fmtTime(offsetSeconds) + " – " + fmtTime(Math.min(ex.duration, offsetSeconds + EXTRACT_CHUNK_SECONDS)) + "…";
        var blob = encodeWav(samples.subarray(c * chunkSamples, Math.min((c + 1) * chunkSamples, samples.length)), rate);
        var form = new FormData();
        form.append("file", new File([blob], "extract.wav", { type: "audio/wav" }));
        var response = await fetch("/v1/audio/transcriptions?format=segments", { method: "POST", body: form });
        if (!response.ok) {
          throw new Error(await readErrorBody(response));
        }
        var payload = await response.json();
        (payload.segments || []).forEach(function (segment) {
          ex.segments.push({
            start: segment.start + offsetSeconds,
            end: segment.end + offsetSeconds,
            text: segment.text,
            speaker: segment.speaker || ""
          });
        });
        renderExtractTimeline();
        drawExtractWave();
      }
      extractTranscribeStatus.textContent = ex.segments.length + " segments";
      renderExtractFilter();
      log("Extractor transcribed " + ex.sourceName + ": " + ex.segments.length + " segments");
    } catch (err) {
      extractError("Transcription failed: " + err.message);
      extractTranscribeStatus.textContent = "";
    }
    extractTranscribeButton.disabled = false;
  });

  // --- transcript timeline + speaker tagging ------------------------------
  function renderExtractTimeline() {
    extractTimeline.textContent = "";
    if (!ex.segments.length) {
      extractTimeline.textContent = "No transcript yet — load a file and press Transcribe.";
      return;
    }
    ex.segments.forEach(function (segment, index) {
      if (ex.filter && segment.speaker !== ex.filter) {
        return;
      }
      var row = createElement("div", "extract-segment" + (index === ex.selectedRow ? " selected" : "") + (ex.checked[index] ? " checked" : ""));
      row.tabIndex = 0;
      var head = createElement("div", "extract-segment-head");
      var tick = document.createElement("input");
      tick.type = "checkbox";
      tick.className = "extract-segment-tick";
      tick.checked = Boolean(ex.checked[index]);
      tick.title = "Include in a combined export";
      tick.addEventListener("click", function (event) {
        event.stopPropagation();
      });
      tick.addEventListener("change", function () {
        if (tick.checked) {
          ex.checked[index] = true;
        } else {
          delete ex.checked[index];
        }
        row.classList.toggle("checked", tick.checked);
        updateExtractRegionUI();
        drawExtractWave();
      });
      head.appendChild(tick);
      head.appendChild(createElement("span", "extract-segment-time", fmtTime(segment.start)));
      if (segment.speaker) {
        head.appendChild(createElement("span", "extract-segment-speaker", segment.speaker));
      }
      var tags = createElement("span", "extract-segment-tags");
      // Offer every speaker present plus the manual defaults, so detected
      // clusters (D, E, ...) can be corrected by hand too.
      var tagNames = taggedSpeakers();
      ["A", "B", "C"].forEach(function (n) {
        if (tagNames.indexOf(n) < 0) {
          tagNames.push(n);
        }
      });
      tagNames.sort().forEach(function (name) {
        var tag = createElement("button", "plain tag-button" + (segment.speaker === name ? " active" : ""), name);
        tag.type = "button";
        tag.addEventListener("click", function (event) {
          event.stopPropagation();
          tagExtractSegment(index, name);
        });
        tags.appendChild(tag);
      });
      head.appendChild(tags);
      row.appendChild(head);
      var textDiv = createElement("div", "extract-segment-text", segment.text);
      // Transcripts are never 100%: lines are directly editable, and the
      // edited text is what merge/export metadata carries forward.
      textDiv.contentEditable = "true";
      textDiv.spellcheck = false;
      textDiv.addEventListener("blur", function () {
        segment.text = textDiv.textContent.trim();
      });
      textDiv.addEventListener("keydown", function (event) {
        if (event.key === "Enter") {
          event.preventDefault();
          textDiv.blur();
        }
        event.stopPropagation(); // typing digits must not retag the row
      });
      row.appendChild(textDiv);
      row.addEventListener("click", function () {
        setExtractRegion(segment.start, Math.min(segment.end + EXTRACT_TAIL_PAD, ex.duration), index);
      });
      row.addEventListener("keydown", function (event) {
        if (event.key === "1" || event.key === "2" || event.key === "3") {
          tagExtractSegment(index, ["A", "B", "C"][Number(event.key) - 1]);
          event.preventDefault();
        }
      });
      extractTimeline.appendChild(row);
    });
    if (!extractTimeline.childNodes.length) {
      extractTimeline.textContent = "No segments tagged " + ex.filter + " yet.";
    }
  }

  function tagExtractSegment(index, name) {
    var segment = ex.segments[index];
    segment.speaker = segment.speaker === name ? "" : name;
    renderExtractFilter();
    renderExtractTimeline();
    drawExtractWave();
  }

  extractFilterRow.addEventListener("click", function (event) {
    var button = event.target.closest(".speaker-filter");
    if (!button) {
      return;
    }
    ex.filter = button.getAttribute("data-speaker");
    extractFilterRow.querySelectorAll(".speaker-filter").forEach(function (b) {
      b.classList.toggle("active", b === button);
    });
    renderExtractTimeline();
  });

  // --- Clone the cast -----------------------------------------------------
  // One click after speaker tagging: for every tagged speaker, pick their
  // best material (longest merged run, capped at 15s) and mint a library
  // voice per speaker — cast extraction for making your own episodes.
  var extractCastButton = document.getElementById("extractCastButton");

  function taggedSpeakers() {
    var speakers = [];
    ex.segments.forEach(function (segment) {
      if (segment.speaker && speakers.indexOf(segment.speaker) < 0) {
        speakers.push(segment.speaker);
      }
    });
    return speakers.sort();
  }

  // bestSpanFor merges one speaker's segments (joining ≤0.3s gaps) and
  // returns their longest run, trimmed to at most 15s — clone references
  // want 5–15s of clean single-speaker speech.
  function bestSpanFor(speaker) {
    var spans = [];
    ex.segments.forEach(function (segment) {
      if (segment.speaker !== speaker) {
        return;
      }
      var end = Math.min(segment.end + EXTRACT_TAIL_PAD, ex.duration);
      var last = spans[spans.length - 1];
      if (last && segment.start - last.end <= 0.3) {
        last.end = Math.max(last.end, end);
      } else {
        spans.push({ start: segment.start, end: end });
      }
    });
    var best = null;
    spans.forEach(function (span) {
      if (!best || span.end - span.start > best.end - best.start) {
        best = span;
      }
    });
    if (best && best.end - best.start > 15) {
      best = { start: best.start, end: best.start + 15 };
    }
    return best;
  }

  extractCastButton.addEventListener("click", async function () {
    var speakers = taggedSpeakers();
    if (!speakers.length) {
      return;
    }
    extractErrorBox.hidden = true;
    extractCastButton.disabled = true;
    var original = extractCastButton.textContent;
    var made = [];
    var base = ex.sourceName.replace(/\.[^.]+$/, "");
    try {
      for (var i = 0; i < speakers.length; i += 1) {
        var speaker = speakers[i];
        var span = bestSpanFor(speaker);
        if (!span || span.end - span.start < 2) {
          log("Cast: skipping " + speaker + " — under 2s of usable speech");
          continue;
        }
        extractCastButton.textContent = "Cloning " + speaker + " (" + (i + 1) + "/" + speakers.length + ")…";
        var s0 = Math.floor(span.start * ex.rate);
        var s1 = Math.floor(span.end * ex.rate);
        var form = new FormData();
        form.append("file", new File([encodeWav(ex.samples.subarray(s0, s1), ex.rate)], (base + " " + speaker + ".wav").replace(/[:]/g, "."), { type: "audio/wav" }));
        form.append("name", base + " " + speaker);
        var response = await fetch("/v1/voices", { method: "POST", body: form });
        if (!response.ok) {
          throw new Error(speaker + ": " + (await readErrorBody(response)));
        }
        var clone = await response.json();
        made.push(clone.name);
        log("Cast voice saved: " + clone.name + " (" + (span.end - span.start).toFixed(1) + "s reference)");
      }
      await refreshVoices(true);
      log("Clone the cast: " + made.length + "/" + speakers.length + " voices ready" + (made.length ? " — rename them in the Voices tab after a listen" : ""));
    } catch (err) {
      extractError("Cast cloning failed: " + err.message);
    }
    extractCastButton.textContent = original;
    extractCastButton.disabled = taggedSpeakers().length === 0;
  });

  // renderExtractFilter rebuilds the speaker chips from whatever speakers the
  // segments actually carry — manual A/B/C tags or diarization's clusters.
  function renderExtractFilter() {
    var speakers = [];
    ex.segments.forEach(function (segment) {
      if (segment.speaker && speakers.indexOf(segment.speaker) < 0) {
        speakers.push(segment.speaker);
      }
    });
    speakers.sort();
    extractFilterRow.textContent = "";
    ["", "A", "B", "C"].concat(speakers).forEach(function (name, index, all) {
      if (all.indexOf(name) !== index) {
        return; // dedupe: manual defaults vs detected clusters
      }
      var chip = createElement("button", "plain compact-button speaker-filter" + (ex.filter === name ? " active" : ""), name === "" ? "All" : name);
      chip.type = "button";
      chip.setAttribute("data-speaker", name);
      extractFilterRow.appendChild(chip);
    });

    // Selection helpers: tick every visible line (respects the speaker
    // filter, so filter-to-B + Select shown = all of B as one export).
    var selectShown = createElement("button", "plain compact-button", "Select shown");
    selectShown.type = "button";
    selectShown.id = "extractSelectShownButton";
    selectShown.addEventListener("click", function () {
      ex.segments.forEach(function (segment, index) {
        if (!ex.filter || segment.speaker === ex.filter) {
          ex.checked[index] = true;
        }
      });
      renderExtractTimeline();
      updateExtractRegionUI();
      drawExtractWave();
    });
    extractFilterRow.appendChild(selectShown);
    var clearSel = createElement("button", "plain compact-button", "Clear");
    clearSel.type = "button";
    clearSel.addEventListener("click", function () {
      ex.checked = {};
      renderExtractTimeline();
      updateExtractRegionUI();
      drawExtractWave();
    });
    extractFilterRow.appendChild(clearSel);

    // Merge ticked: collapse a continuous run of lines into one segment —
    // the human fix for whisper splitting one utterance (or diarization
    // splitting one voice) across rows.
    var mergeBtn = createElement("button", "plain compact-button", "Merge ticked");
    mergeBtn.type = "button";
    mergeBtn.id = "extractMergeButton";
    mergeBtn.addEventListener("click", function () {
      var indices = checkedIndices();
      if (indices.length < 2) {
        extractError("Tick two or more adjacent lines to merge.");
        return;
      }
      for (var i = 1; i < indices.length; i += 1) {
        if (indices[i] !== indices[i - 1] + 1) {
          extractError("Merge needs a continuous run of lines — untick the gaps.");
          return;
        }
      }
      var speaker = "";
      indices.forEach(function (i) {
        if (!speaker && ex.segments[i].speaker) {
          speaker = ex.segments[i].speaker;
        }
      });
      var merged = {
        start: ex.segments[indices[0]].start,
        end: ex.segments[indices[indices.length - 1]].end,
        text: indices.map(function (i) { return ex.segments[i].text; }).join(" "),
        speaker: speaker
      };
      ex.segments.splice(indices[0], indices.length, merged);
      ex.checked = {};
      ex.selectedRow = -1;
      renderExtractFilter();
      renderExtractTimeline();
      updateExtractRegionUI();
      drawExtractWave();
      log("Merged " + indices.length + " lines into one segment (" + fmtTime(merged.start) + " – " + fmtTime(merged.end) + ")");
    });
    extractFilterRow.appendChild(mergeBtn);
    extractFilterRow.hidden = ex.segments.length === 0;
    extractCastButton.disabled = taggedSpeakers().length === 0;
  }

  // --- automatic speaker detection (the diarize engine) -------------------
  var extractDiarizeButton = document.getElementById("extractDiarizeButton");

  extractDiarizeButton.addEventListener("click", async function () {
    if (!ex.samples) {
      return;
    }
    if (!ex.segments.length) {
      extractError("Transcribe first — speaker detection labels the transcript lines.");
      return;
    }
    extractErrorBox.hidden = true;
    extractDiarizeButton.disabled = true;
    var original = extractDiarizeButton.textContent;
    extractDiarizeButton.textContent = "Detecting…";
    try {
      var samples = ex.samples;
      var rate = ex.rate;
      if (rate > LIVE_TARGET_RATE) {
        samples = downsampleForLive(samples, rate);
        rate = LIVE_TARGET_RATE;
      }
      var form = new FormData();
      form.append("file", new File([encodeWav(samples, rate)], "diarize.wav", { type: "audio/wav" }));
      var speakersInput = document.getElementById("extractSpeakersInput");
      var count = parseInt(speakersInput.value, 10);
      var url = "/v1/audio/diarization" + (count >= 1 && count <= 26 ? "?speakers=" + count : "");
      var response = await fetch(url, { method: "POST", body: form });
      if (!response.ok) {
        throw new Error(await readErrorBody(response));
      }
      var payload = await response.json();
      var spans = payload.spans || [];
      // Assign each transcript line the speaker whose span overlaps it most;
      // lines with no meaningful overlap keep their manual tag.
      var tagged = 0;
      ex.segments.forEach(function (segment) {
        var best = null;
        var bestOverlap = 0;
        spans.forEach(function (span) {
          var overlap = Math.min(segment.end, span.end) - Math.max(segment.start, span.start);
          if (overlap > bestOverlap) {
            bestOverlap = overlap;
            best = span;
          }
        });
        if (best && bestOverlap > 0.2 * (segment.end - segment.start)) {
          segment.speaker = best.speaker;
          tagged += 1;
        }
      });
      var speakerCount = {};
      spans.forEach(function (span) { speakerCount[span.speaker] = true; });
      renderExtractFilter();
      renderExtractTimeline();
      drawExtractWave();
      log("Speaker detection: " + Object.keys(speakerCount).length + " speakers, " + tagged + "/" + ex.segments.length + " lines tagged (" + payload.duration_ms + "ms)");
    } catch (err) {
      extractError("Speaker detection failed: " + err.message);
    }
    extractDiarizeButton.textContent = original;
    extractDiarizeButton.disabled = false;
  });

  // --- Studio library & jobs -------------------------------------------
  var libraryRefreshButton = document.getElementById("libraryRefreshButton");
  var libraryErrorBox = document.getElementById("libraryErrorBox");
  var jobsList = document.getElementById("jobsList");
  var libraryList = document.getElementById("libraryList");
  var libraryReplyButton = document.getElementById("libraryReplyButton");
  var librarySpeakButton = document.getElementById("librarySpeakButton");
  var libraryImageButton = document.getElementById("libraryImageButton");
  var jobsPollTimer = null;

  // srcToB64 turns an <audio>/<img> src (data: URL or blob: URL) back into
  // raw base64 for a library save.
  async function srcToB64(src) {
    if (src.indexOf("data:") === 0) {
      return src.split(",", 2)[1];
    }
    var response = await fetch(src);
    var buffer = await response.arrayBuffer();
    var bytes = new Uint8Array(buffer);
    var chunks = [];
    for (var i = 0; i < bytes.length; i += 32768) {
      chunks.push(String.fromCharCode.apply(null, bytes.subarray(i, i + 32768)));
    }
    return btoa(chunks.join(""));
  }

  async function saveToLibrary(kind, name, src, meta, button) {
    if (!src) {
      return;
    }
    var original = button.textContent;
    button.disabled = true;
    button.textContent = "Saving…";
    try {
      var b64 = await srcToB64(src);
      var response = await fetch("/v1/library", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ kind: kind, name: name, data_b64: b64, meta: meta || {} })
      });
      if (!response.ok) {
        throw new Error(await readErrorBody(response));
      }
      log("Saved to library: " + name);
      button.textContent = "Saved ✓";
      window.setTimeout(function () {
        button.textContent = original;
        button.disabled = false;
      }, 1500);
    } catch (err) {
      log("Library save failed: " + err.message, "error");
      button.textContent = original;
      button.disabled = false;
    }
  }

  libraryReplyButton.addEventListener("click", function () {
    var name = (replyOutput.value || "Voice reply").slice(0, 60);
    saveToLibrary("audio", name, replyAudio.src, { source: "voice-loop" }, libraryReplyButton);
  });
  librarySpeakButton.addEventListener("click", function () {
    var name = (speakTextInput.value || "Spoken text").slice(0, 60);
    saveToLibrary("audio", name, speakAudio.src, { source: "speak" }, librarySpeakButton);
  });
  libraryImageButton.addEventListener("click", function () {
    var name = (imagePromptInput.value || "Generated image").slice(0, 60);
    saveToLibrary("image", name, imagePreview.src, { source: "image-lab" }, libraryImageButton);
  });

  function jobStateClass(status) {
    if (status === "complete") {
      return "ready";
    }
    if (status === "failed" || status === "cancelled") {
      return "danger";
    }
    return "warn";
  }

  function renderJobs(jobs) {
    jobsList.textContent = "";
    if (!jobs.length) {
      jobsList.textContent = "No jobs yet.";
      return false;
    }
    var anyActive = false;
    jobs.forEach(function (job) {
      var row = createElement("div", "job-row");
      var head = createElement("div", "job-row-head");
      head.appendChild(createElement("span", "job-kind", job.kind));
      head.appendChild(createElement("span", "job-id", job.id));
      head.appendChild(createElement("span", "status-pill " + jobStateClass(job.status), job.status));
      row.appendChild(head);

      var active = job.status === "queued" || job.status === "running";
      if (active) {
        anyActive = true;
        var progress = document.createElement("progress");
        progress.max = 1;
        progress.value = job.progress || 0;
        row.appendChild(progress);
        if (job.detail) {
          row.appendChild(createElement("div", "job-detail", job.detail));
        }
        var cancelButton = createElement("button", "plain compact-button", "Cancel");
        cancelButton.type = "button";
        cancelButton.addEventListener("click", async function () {
          cancelButton.disabled = true;
          try {
            var response = await fetch("/v1/jobs/" + encodeURIComponent(job.id) + "/cancel", { method: "POST" });
            if (!response.ok) {
              throw new Error(await readErrorBody(response));
            }
            refreshLibrary(true);
          } catch (err) {
            log("Cancel failed: " + err.message, "error");
            cancelButton.disabled = false;
          }
        });
        row.appendChild(cancelButton);
      } else if (job.error) {
        row.appendChild(createElement("div", "job-detail", job.error));
      } else if (job.result && job.result.title) {
        row.appendChild(createElement("div", "job-detail", job.result.title));
      }
      jobsList.appendChild(row);
    });
    return anyActive;
  }

  function renderLibraryItems(items) {
    libraryList.textContent = "";
    if (!items.length) {
      libraryList.textContent = "Nothing saved yet.";
      return;
    }
    items.forEach(function (item) {
      var row = createElement("div", "library-item");
      var head = createElement("div", "library-item-head");
      head.appendChild(createElement("span", "library-item-name", item.name));
      head.appendChild(createElement("span", "library-item-kind", item.kind));
      row.appendChild(head);
      row.appendChild(createElement("div", "library-item-meta",
        new Date(item.createdAt).toLocaleString() + " · " + Math.round(item.bytes / 1024) + " KB"));

      var artifactURL = "/v1/library/" + encodeURIComponent(item.id) + "/artifact";
      if (item.kind === "audio") {
        var audio = document.createElement("audio");
        audio.controls = true;
        audio.preload = "none";
        audio.src = artifactURL;
        row.appendChild(audio);
      } else if (item.kind === "image") {
        var img = document.createElement("img");
        img.className = "library-item-image";
        img.loading = "lazy";
        img.alt = item.name;
        img.src = artifactURL;
        row.appendChild(img);
      }

      var actions = createElement("div", "library-item-actions");
      var download = createElement("button", "plain compact-button", "Download");
      download.type = "button";
      download.addEventListener("click", function () {
        downloadURL(artifactURL, item.name + (item.kind === "audio" ? ".wav" : ".png"));
      });
      actions.appendChild(download);
      var remove = createElement("button", "plain compact-button", "Delete");
      remove.type = "button";
      remove.addEventListener("click", async function () {
        remove.disabled = true;
        try {
          var response = await fetch("/v1/library/" + encodeURIComponent(item.id), { method: "DELETE" });
          if (!response.ok && response.status !== 204) {
            throw new Error(await readErrorBody(response));
          }
          refreshLibrary(true);
        } catch (err) {
          log("Delete failed: " + err.message, "error");
          remove.disabled = false;
        }
      });
      actions.appendChild(remove);
      row.appendChild(actions);
      libraryList.appendChild(row);
    });
  }

  async function refreshLibrary(silent) {
    libraryErrorBox.hidden = true;
    try {
      var results = await Promise.all([
        fetch("/v1/jobs", { method: "GET" }),
        fetch("/v1/library", { method: "GET" })
      ]);
      if (!results[0].ok || !results[1].ok) {
        throw new Error("library endpoints returned " + results[0].status + "/" + results[1].status);
      }
      var jobsPayload = await results[0].json();
      var libraryPayload = await results[1].json();
      var anyActive = renderJobs(jobsPayload.jobs || []);
      renderLibraryItems(libraryPayload.items || []);
      if (!silent) {
        log("Library refreshed");
      }
      window.clearTimeout(jobsPollTimer);
      if (anyActive && activeTabFromHash() === "library") {
        jobsPollTimer = window.setTimeout(function () {
          refreshLibrary(true);
        }, 1500);
      }
    } catch (err) {
      libraryErrorBox.textContent = "Library unavailable: " + err.message;
      libraryErrorBox.hidden = false;
      if (!silent) {
        log("Library refresh failed: " + err.message, "error");
      }
    }
  }

  libraryRefreshButton.addEventListener("click", function () {
    refreshLibrary(false);
  });

  resetCast();
  resetSources();
  renderEngineRack(null);
  applyTab(activeTabFromHash());
  log("Demo loaded");
  refreshHealth(false);
  refreshStoryLibrary(true);
  refreshVoices(true);
  loadProfiles();
  refreshGPU();
  window.setInterval(function () {
    if (document.visibilityState === "visible") {
      refreshHealth(true);
    }
  }, HEALTH_POLL_MS);
}());
