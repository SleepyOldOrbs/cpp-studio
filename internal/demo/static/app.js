(function () {
  "use strict";

  var shell = document.querySelector(".app-shell");
  var healthButton = document.getElementById("healthButton");
  var gatewayStatus = document.getElementById("gatewayStatus");
  var healthUpdated = document.getElementById("healthUpdated");
  var healthBody = document.getElementById("healthBody");
  var voiceForm = document.getElementById("voiceForm");
  var messageInput = document.getElementById("messageInput");
  var recordButton = document.getElementById("recordButton");
  var wavInput = document.getElementById("wavInput");
  var wavStatus = document.getElementById("wavStatus");
  var runButton = document.getElementById("runButton");
  var clearButton = document.getElementById("clearButton");
  var clearLogButton = document.getElementById("clearLogButton");
  var transcriptOutput = document.getElementById("transcriptOutput");
  var replyOutput = document.getElementById("replyOutput");
  var replyAudio = document.getElementById("replyAudio");
  var errorBox = document.getElementById("errorBox");
  var logOutput = document.getElementById("logOutput");
  var imageForm = document.getElementById("imageForm");
  var imagePromptInput = document.getElementById("imagePromptInput");
  var imageSizeInput = document.getElementById("imageSizeInput");
  var generateImageButton = document.getElementById("generateImageButton");
  var clearImageButton = document.getElementById("clearImageButton");
  var imagePreview = document.getElementById("imagePreview");
  var imagePlaceholder = document.getElementById("imagePlaceholder");
  var imageStatus = document.getElementById("imageStatus");
  var imageErrorBox = document.getElementById("imageErrorBox");
  var storyForm = document.getElementById("storyForm");
  var storySubjectInput = document.getElementById("storySubjectInput");
  var storySecondsInput = document.getElementById("storySecondsInput");
  var storyGenerateButton = document.getElementById("storyGenerateButton");
  var storyCancelButton = document.getElementById("storyCancelButton");
  var storyErrorBox = document.getElementById("storyErrorBox");
  var storyStatus = document.getElementById("storyStatus");
  var storyProgress = document.getElementById("storyProgress");
  var storyAudio = document.getElementById("storyAudio");
  var storyLibraryButton = document.getElementById("storyLibraryButton");
  var storyLibrary = document.getElementById("storyLibrary");
  var storyFacts = document.getElementById("storyFacts");

  var running = false;
  var recording = false;
  var activeStoryID = "";
  var activeStoryPoll = 0;
  var activeWavFile = null;
  var activeAudioUrl = "";
  var recorder = null;
  var recordSetupPending = false;
  var recordStopRequested = false;

  var apiControls = [
    healthButton,
    wavInput,
    runButton,
    clearButton,
    clearLogButton,
    messageInput,
    imagePromptInput,
    imageSizeInput,
    generateImageButton,
    clearImageButton,
    storySubjectInput,
    storySecondsInput,
    storyGenerateButton,
    storyLibraryButton
  ];

  function log(message) {
    var stamp = new Date().toLocaleTimeString();
    logOutput.textContent += "[" + stamp + "] " + message + "\n";
    logOutput.scrollTop = logOutput.scrollHeight;
  }

  function setError(error) {
    var message = error && error.message ? error.message : String(error);
    errorBox.textContent = message;
    errorBox.hidden = false;
    log("Error: " + message);
  }

  function setImageError(error) {
    var message = error && error.message ? error.message : String(error);
    imageErrorBox.textContent = message;
    imageErrorBox.hidden = false;
    log("Error: " + message);
  }

  function setStoryError(error) {
    var message = error && error.message ? error.message : String(error);
    storyErrorBox.textContent = message;
    storyErrorBox.hidden = false;
    log("Error: " + message);
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

  function setRunning(value) {
    running = value;
    shell.setAttribute("aria-busy", value ? "true" : "false");
    apiControls.forEach(function (control) {
      control.disabled = value || recording || recordSetupPending;
    });
    recordButton.disabled = value || (!recording && !recordSetupPending && !canRecord());
  }

  function setRecording(value) {
    recording = value;
    recordButton.classList.toggle("recording", value);
    recordButton.setAttribute("aria-pressed", value ? "true" : "false");
    recordButton.textContent = value ? "Release to stop" : "Push to record";
    apiControls.forEach(function (control) {
      control.disabled = value || running || recordSetupPending;
    });
    recordButton.disabled = running || (!value && !recordSetupPending && !canRecord());
  }

  function setRecordSetupPending(value) {
    recordSetupPending = value;
    recordButton.textContent = value ? "Preparing..." : recording ? "Release to stop" : "Push to record";
    apiControls.forEach(function (control) {
      control.disabled = value || recording || running;
    });
    recordButton.disabled = running || (!recording && !value && !canRecord());
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

  function renderHealth(data) {
    renderStatus(data.status);
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

  async function refreshHealth() {
    clearError();
    setRunning(true);
    try {
      log("GET /health");
      var response = await fetch("/health", { method: "GET" });
      await ensureOk(response, "Health");
      var data = await response.json();
      renderHealth(data);
      log("Health status: " + (data.status || "unknown"));
    } catch (error) {
      renderStatus("error");
      setError(error);
    } finally {
      setRunning(false);
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
      storyLibrary.appendChild(createElement("span", "story-library-empty", "No retained stories"));
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

  async function refreshStoryLibrary() {
    clearStoryError();
    setRunning(true);
    try {
      log("GET /v1/stories");
      var response = await fetch("/v1/stories", { method: "GET" });
      await ensureOk(response, "Story library");
      var data = await response.json();
      renderStoryLibrary(data.stories || []);
    } catch (error) {
      setStoryError(error);
    } finally {
      setRunning(false);
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
      log("GET /v1/stories/" + id);
      var response = await fetch("/v1/stories/" + encodeURIComponent(id), { method: "GET" });
      await ensureOk(response, "Story status");
      var data = await response.json();
      setStoryStatus(data.status || data.stage || "unknown", data.progress || 0);
      if (data.status === "complete") {
        activeStoryID = "";
        storyCancelButton.disabled = true;
        if (data.artifact_url) {
          storyAudio.src = data.artifact_url;
          storyAudio.load();
        }
        renderStoryManifest(data.manifest);
        log("Story complete");
        refreshStoryLibrary();
        return;
      }
      if (data.status === "failed") {
        activeStoryID = "";
        storyCancelButton.disabled = true;
        throw new Error(data.error && data.error.message ? data.error.message : "Story failed");
      }
      if (data.status === "cancelled") {
        activeStoryID = "";
        storyCancelButton.disabled = true;
        log("Story cancelled");
        return;
      }
      activeStoryPoll = window.setTimeout(function () {
        pollStory(id);
      }, data.retry_after_ms || 500);
    } catch (error) {
      activeStoryID = "";
      storyCancelButton.disabled = true;
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
    try {
      storyAudio.removeAttribute("src");
      storyAudio.load();
      storyFacts.textContent = "";
      setStoryStatus("Starting...", 0);
      var targetSeconds = Number(storySecondsInput.value || "90");
      var response = await fetch("/v1/stories", {
        method: "POST",
        headers: {
          "Content-Type": "application/json"
        },
        body: JSON.stringify({
          subject: storySubjectInput.value.trim(),
          target_seconds: targetSeconds,
          source_mode: "curated",
          voice_mode: "placeholder",
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
      setRunning(false);
    }
  }

  async function runVoiceLoop(event) {
    if (event) {
      event.preventDefault();
    }
    clearError();
    setRunning(true);
    try {
      resetOutputs();
      var form = new FormData();
      if (activeWavFile) {
        form.append("file", activeWavFile, activeWavFile.name || "input.wav");
      } else {
        var text = messageInput.value.trim();
        if (!text) {
          throw new Error("Record audio, choose a WAV, or enter a typed message");
        }
        form.append("message", text);
        log("Using typed message as transcript");
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
    activeWavFile = null;
    wavInput.value = "";
    wavStatus.textContent = "None";
    messageInput.value = "";
    resetOutputs();
    log("Cleared workspace");
  }

  healthButton.addEventListener("click", refreshHealth);
  voiceForm.addEventListener("submit", runVoiceLoop);
  imageForm.addEventListener("submit", generateImage);
  storyForm.addEventListener("submit", startStory);
  storyCancelButton.addEventListener("click", cancelStory);
  storyLibraryButton.addEventListener("click", refreshStoryLibrary);
  wavInput.addEventListener("change", chooseWav);
  clearButton.addEventListener("click", clearAll);
  clearImageButton.addEventListener("click", function () {
    clearImageOutput();
    log("Cleared image");
  });
  clearLogButton.addEventListener("click", function () {
    logOutput.textContent = "";
  });

  recordButton.addEventListener("pointerdown", startRecording);
  recordButton.addEventListener("pointerup", stopRecording);
  recordButton.addEventListener("pointercancel", stopRecording);
  recordButton.addEventListener("lostpointercapture", stopRecording);

  if (!canRecord()) {
    recordButton.disabled = true;
    recordButton.textContent = "Recording unavailable";
  }

  log("Demo loaded");
  refreshHealth();
  refreshStoryLibrary();
}());
