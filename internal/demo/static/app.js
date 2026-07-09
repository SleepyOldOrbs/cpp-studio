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
  var wavClearButton = document.getElementById("wavClearButton");
  var runButton = document.getElementById("runButton");
  var clearButton = document.getElementById("clearButton");
  var clearLogButton = document.getElementById("clearLogButton");
  var transcriptOutput = document.getElementById("transcriptOutput");
  var replyOutput = document.getElementById("replyOutput");
  var replyAudio = document.getElementById("replyAudio");
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
  var storyForm = document.getElementById("storyForm");
  var storySubjectInput = document.getElementById("storySubjectInput");
  var storySecondsInput = document.getElementById("storySecondsInput");
  var storyVoiceSelect = document.getElementById("storyVoiceSelect");
  var storyGenerateButton = document.getElementById("storyGenerateButton");
  var storyCancelButton = document.getElementById("storyCancelButton");
  var storyErrorBox = document.getElementById("storyErrorBox");
  var storyStatus = document.getElementById("storyStatus");
  var storyProgress = document.getElementById("storyProgress");
  var storyAudio = document.getElementById("storyAudio");
  var storyLibraryButton = document.getElementById("storyLibraryButton");
  var storyLibrary = document.getElementById("storyLibrary");
  var storyFacts = document.getElementById("storyFacts");

  var ENGINE_NAMES = ["llama", "whisper", "audio", "sd"];
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
    newConvoButton
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
    var busy = running || recording || recordSetupPending || live;
    apiControls.forEach(function (control) {
      control.disabled = busy;
    });
    if (activeStoryID) {
      storyGenerateButton.disabled = true;
    }
    recordButton.disabled = running || live || (!recording && !recordSetupPending && !canRecord());
    liveButton.disabled = running || recording || recordSetupPending || (!live && !canRecord());
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
    log("WAV ready from " + source + ": " + file.name + ", " + formatBytes(file.size));
  }

  function clearActiveWav() {
    activeWavFile = null;
    wavInput.value = "";
    wavStatus.textContent = "None";
  }

  function clearAudioUrl() {
    if (activeAudioUrl) {
      URL.revokeObjectURL(activeAudioUrl);
      activeAudioUrl = "";
    }
    replyAudio.removeAttribute("src");
    replyAudio.load();
  }

  function resetOutputs() {
    transcriptOutput.value = "";
    replyOutput.value = "";
    clearAudioUrl();
    clearError();
  }

  function clearImageOutput() {
    imagePreview.removeAttribute("src");
    imagePreview.hidden = true;
    imagePlaceholder.hidden = false;
    imageStatus.textContent = "Idle";
    clearImageError();
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

  function renderHealth(data) {
    renderStatus(data.status);
    renderEngineRack(data.engines);
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
      healthBody.appendChild(row);
    });
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
      storyFacts.textContent = "";
      setStoryStatus("Starting...", 0);
      var targetSeconds = Number(storySecondsInput.value || "90");
      log("POST /v1/stories");
      var response = await fetch("/v1/stories", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify({
          subject: storySubjectInput.value.trim(),
          target_seconds: targetSeconds,
          source_mode: "curated",
          voice_mode: storyVoiceSelect.value,
          sources: collectStorySources()
        })
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
  wavClearButton.addEventListener("click", function () {
    if (!activeWavFile) {
      return;
    }
    clearActiveWav();
    log("Removed WAV source");
  });
  clearImageButton.addEventListener("click", function () {
    clearImageOutput();
    log("Cleared image");
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

  if (!canRecord()) {
    recordButton.disabled = true;
    recordButton.textContent = "Recording unavailable";
    liveButton.disabled = true;
  }

  renderEngineRack(null);
  log("Demo loaded");
  refreshHealth(false);
  refreshStoryLibrary(true);
  window.setInterval(function () {
    if (document.visibilityState === "visible") {
      refreshHealth(true);
    }
  }, HEALTH_POLL_MS);
}());
