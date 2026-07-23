param()

$ErrorActionPreference = "Stop"

function Assert-Equal {
  param(
    [object]$Actual,
    [object]$Expected,
    [string]$Label
  )

  if ($Actual -ne $Expected) {
    throw "$Label expected '$Expected', got '$Actual'"
  }
}

function Assert-Contains {
  param(
    [object[]]$Values,
    [string]$Expected,
    [string]$Label
  )

  if ($Expected -notin $Values) {
    throw "$Label did not contain '$Expected': $($Values -join ', ')"
  }
}

$benchmark = Join-Path $PSScriptRoot "benchmark-story-local.ps1"
$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) "cpp-studio-benchmark-test-$PID"
$audioConfig = Join-Path $testRoot "audio.json"
$combinedConfig = Join-Path $testRoot "combined.json"
$outDir = Join-Path $testRoot "result"
$fakeAudio = Join-Path $testRoot "fake-audio.ps1"
$fakePlanner = Join-Path $testRoot "fake-planner.ps1"
$runConfig = Join-Path $testRoot "run-audio.json"
$runOutDir = Join-Path $testRoot "run-result"
$combinedRunConfig = Join-Path $testRoot "run-combined.json"
$nonLoopbackConfig = Join-Path $testRoot "run-non-loopback.json"
$combinedRunOutDir = Join-Path $testRoot "run-combined-result"
$nonLoopbackOutDir = Join-Path $testRoot "run-non-loopback-result"
$exclusiveRunOutDir = Join-Path $testRoot "run-exclusive-result"
$cpuRunOutDir = Join-Path $testRoot "run-cpu-result"
$restartFailureOutDir = Join-Path $testRoot "run-restart-failure-result"
$badChatOutDir = Join-Path $testRoot "run-bad-chat-result"
$coexistFailureOutDir = Join-Path $testRoot "run-coexist-failure-result"
$performanceMissOutDir = Join-Path $testRoot "run-performance-miss-result"
$batchFallbackOutDir = Join-Path $testRoot "run-batch-fallback-result"
$slowCoexistOutDir = Join-Path $testRoot "run-slow-coexist-result"
$slowCpuCoexistOutDir = Join-Path $testRoot "run-slow-cpu-coexist-result"
$requireCompleteOutDir = Join-Path $testRoot "run-require-complete-result"
$gpuHangOutDir = Join-Path $testRoot "run-gpu-hang-result"
$fakeNvidia = Join-Path $testRoot "nvidia-smi.cmd"
$testWorkloadPath = Join-Path $testRoot "test-workload.txt"
$concurrentOutDir = Join-Path $testRoot "run-concurrent-result"
$originalPath = $env:PATH

$portProbe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$portProbe.Start()
$plannerPort = ([System.Net.IPEndPoint]$portProbe.LocalEndpoint).Port
$portProbe.Stop()
$plannerHealthURL = "http://127.0.0.1:$plannerPort/health"

New-Item -ItemType Directory -Force -Path $testRoot | Out-Null
try {
  @'
@echo off
if "%FAKE_NVIDIA_HANG%"=="1" powershell.exe -NoProfile -Command "Start-Sleep -Seconds 5"
if "%FAKE_NVIDIA_USED%"=="" set FAKE_NVIDIA_USED=15360
echo NVIDIA Test GPU, 16384, %FAKE_NVIDIA_USED%
'@ | Set-Content -LiteralPath $fakeNvidia -Encoding ascii
  $env:FAKE_NVIDIA_USED = "15360"
  $env:PATH = "$testRoot;$originalPath"
  @("First test line.", "Second test line.", "Third test line.") | Set-Content -LiteralPath $testWorkloadPath -Encoding utf8
  @{
    gateway = @{ host = "127.0.0.1"; port = 18765 }
    engines = @{
      audio = @{
        command = "fake-audio.exe"
        args = @("--task", "tts", "--model", "fake-model")
        mode = "subprocess"
      }
    }
  } | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $audioConfig -Encoding utf8

  @{
    gateway = @{ host = "127.0.0.1"; port = 18766 }
    engines = @{
      llama = @{
        command = "fake-llama.exe"
        args = @("--host", "127.0.0.1", "--port", [string]$plannerPort, "-m", "fake.gguf", "-ngl", "99")
        mode = "server"
        healthUrl = $plannerHealthURL
      }
      audio = @{
        command = "fake-audio.exe"
        args = @("--task", "tts", "--model", "fake-model")
        mode = "subprocess"
      }
    }
  } | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $combinedConfig -Encoding utf8

  $params = @{
    AudioConfig = $audioConfig
    CombinedConfig = $combinedConfig
    OutDir = $outDir
    PlanOnly = $true
  }
  & $benchmark @params | Out-Null

  $jsonPath = Join-Path $outDir "benchmark.json"
  $markdownPath = Join-Path $outDir "benchmark.md"
  if (-not (Test-Path -LiteralPath $jsonPath)) {
    throw "PlanOnly did not write benchmark.json"
  }
  if (-not (Test-Path -LiteralPath $markdownPath)) {
    throw "PlanOnly did not write benchmark.md"
  }
  if (Test-Path -LiteralPath (Join-Path $outDir ".benchmark-running")) {
    throw "PlanOnly left a stale running marker after publishing artifacts"
  }

  $result = Get-Content -LiteralPath $jsonPath -Raw | ConvertFrom-Json
  Assert-Equal $result.schema_version "eng-t0.v1" "schema_version"
  Assert-Equal $result.mode "plan" "mode"
  Assert-Equal $result.decision.status "pending_measurements" "decision status"
  Assert-Equal $result.inputs.workload_lines 10 "default representative workload line count"
  Assert-Equal $result.inputs.workload_words 171 "default representative workload word count"

  $caseIDs = @($result.cases | ForEach-Object { $_.id })
  @(
    "audio.per_line",
    "audio.batch_session",
    "audio.paragraph",
    "audio.chapter",
    "planner.gpu_startup",
    "planner.gpu_chat",
    "planner.gpu_restart",
    "planner.gpu_audio_coexist",
    "planner.cpu_startup",
    "planner.cpu_chat",
    "planner.cpu_audio_coexist"
  ) | ForEach-Object {
    Assert-Contains $caseIDs $_ "planned cases"
  }

  $markdown = Get-Content -LiteralPath $markdownPath -Raw
  if ($markdown -notmatch "ENG-T0 Local Engine Feasibility") {
    throw "benchmark.md did not contain the expected title"
  }
  if ($markdown -notmatch "pending_measurements") {
    throw "benchmark.md did not expose the pending decision"
  }
  if ($markdown -notmatch [regex]::Escape([string]$result.run_id)) {
    throw "benchmark.md and benchmark.json do not share a run id"
  }

  @'
function Write-TestWav {
  param([string]$Path)

  $sampleRate = 16000
  $slowBatch = $env:FAKE_AUDIO_SLOW_BATCH -eq "1" -and $script:FakeBatchMode
  $slowCoexist = $env:FAKE_AUDIO_SLOW_COEXIST -eq "1" -and $script:FakeCoexistCase
  $samples = if ($env:FAKE_AUDIO_SLOW_RTF -eq "1" -or $slowBatch -or $slowCoexist) { 160 } else { 16000 }
  $dataBytes = $samples * 2
  $bytes = [byte[]]::new(44 + $dataBytes)
  [System.Text.Encoding]::ASCII.GetBytes("RIFF").CopyTo($bytes, 0)
  [BitConverter]::GetBytes(36 + $dataBytes).CopyTo($bytes, 4)
  [System.Text.Encoding]::ASCII.GetBytes("WAVEfmt ").CopyTo($bytes, 8)
  [BitConverter]::GetBytes(16).CopyTo($bytes, 16)
  [BitConverter]::GetBytes([uint16]1).CopyTo($bytes, 20)
  [BitConverter]::GetBytes([uint16]1).CopyTo($bytes, 22)
  [BitConverter]::GetBytes($sampleRate).CopyTo($bytes, 24)
  [BitConverter]::GetBytes($sampleRate * 2).CopyTo($bytes, 28)
  [BitConverter]::GetBytes([uint16]2).CopyTo($bytes, 32)
  [BitConverter]::GetBytes([uint16]16).CopyTo($bytes, 34)
  [System.Text.Encoding]::ASCII.GetBytes("data").CopyTo($bytes, 36)
  [BitConverter]::GetBytes($dataBytes).CopyTo($bytes, 40)
  switch ($env:FAKE_AUDIO_MODE) {
    "short" {
      [System.IO.File]::WriteAllBytes($Path, [byte[]]::new(12))
      return
    }
    "invalid_header" {
      [System.Text.Encoding]::ASCII.GetBytes("NOPE").CopyTo($bytes, 0)
    }
    "missing_metadata" {
      [System.Text.Encoding]::ASCII.GetBytes("JUNK").CopyTo($bytes, 12)
    }
    "truncated_data" {
      [System.IO.File]::WriteAllBytes($Path, [byte[]]$bytes[0..43])
      return
    }
  }
  [System.IO.File]::WriteAllBytes($Path, $bytes)
}

$mode = [string]$env:FAKE_AUDIO_MODE
if ($env:FAKE_AUDIO_BREAK_PLANNER -eq "1" -and $env:FAKE_PLANNER_LIVENESS_MARKER -and (Test-Path -LiteralPath $env:FAKE_PLANNER_LIVENESS_MARKER)) {
  Remove-Item -LiteralPath $env:FAKE_PLANNER_LIVENESS_MARKER -Force
}
if ($mode -eq "hang") {
  Start-Sleep -Seconds 5
}
$outIndex = [Array]::IndexOf($args, "--out")
$batchIndex = [Array]::IndexOf($args, "--batch-text-file")
$script:FakeBatchMode = $batchIndex -ge 0
$script:FakeCoexistCase = ($args -join " ") -match "planner-(gpu|cpu)_audio_coexist"
if ($outIndex -ge 0) {
  if ($mode -eq "no_output") {
    exit 0
  }
  Write-TestWav $args[$outIndex + 1]
  exit 0
}
if ($batchIndex -ge 0) {
  $inputPath = $args[$batchIndex + 1]
  $dirIndex = [Array]::IndexOf($args, "--out-dir")
  $manifestIndex = [Array]::IndexOf($args, "--batch-manifest-out")
  $outputDir = $args[$dirIndex + 1]
  New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
  $i = 0
  $requests = [System.Collections.Generic.List[object]]::new()
  $inputLines = @(Get-Content -LiteralPath $inputPath | Where-Object { $_.Trim() })
  foreach ($line in $inputLines) {
    if ($mode -eq "batch_missing_output" -and $i -eq $inputLines.Count - 1) {
      break
    }
    $requestID = "line_$($i + 1)"
    $wavName = if ($mode -eq "manifest_wrong_name" -and $i -eq $inputLines.Count - 1) { "wrong.wav" } else { "$requestID.wav" }
    $wavPath = Join-Path $outputDir $wavName
    Write-TestWav $wavPath
    $samples = ([System.IO.FileInfo]$wavPath).Length - 44
    $samples = [int64]($samples / 2)
    $manifestID = if ($mode -eq "manifest_duplicate_id" -and $i -eq 1) { "line_1" } else { $requestID }
    $manifestSamples = if ($mode -eq "manifest_wrong_samples" -and $i -eq 0) { $samples + 1 } else { $samples }
    $requests.Add([pscustomobject]@{ id = $manifestID; sample_rate = 16000; channels = 1; samples = $manifestSamples })
    $i++
  }
  if ($manifestIndex -ge 0) {
    $manifestPath = $args[$manifestIndex + 1]
    if ($mode -eq "manifest_invalid_json") {
      "not-json" | Set-Content -LiteralPath $manifestPath -Encoding utf8
    }
    elseif ($mode -ne "manifest_missing") {
      if ($mode -eq "manifest_count_only") {
        @{ count = $i } | ConvertTo-Json | Set-Content -LiteralPath $manifestPath -Encoding utf8
      }
      else {
        $manifestRequests = if ($mode -eq "manifest_wrong_count") { @($requests | Select-Object -SkipLast 1) } else { @($requests) }
        @{ requests = $manifestRequests } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath -Encoding utf8
      }
    }
  }
  exit 0
}
throw "expected --out or --batch-text-file"
'@ | Set-Content -LiteralPath $fakeAudio -Encoding utf8

  @{
    gateway = @{ host = "127.0.0.1"; port = 18767 }
    engines = @{
      audio = @{
        command = "pwsh.exe"
        args = @("-NoProfile", "-File", $fakeAudio)
        mode = "subprocess"
      }
    }
  } | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $runConfig -Encoding utf8

  $runParams = @{
    AudioConfig = $runConfig
    CombinedConfig = $runConfig
    WorkloadPath = $testWorkloadPath
    OutDir = $runOutDir
    SkipPlanner = $true
    GpuPollMilliseconds = 50
  }

  $env:FAKE_AUDIO_MODE = "hang"
  $concurrentParams = $runParams.Clone()
  $concurrentParams.OutDir = $concurrentOutDir
  $concurrentParams.ProcessTimeoutSeconds = 2
  $firstRun = Start-Job -ArgumentList $benchmark, $concurrentParams -ScriptBlock {
    param($BenchmarkPath, $Parameters)
    & $BenchmarkPath @Parameters | Out-Null
  }
  $markerPath = Join-Path $concurrentOutDir ".benchmark-running"
  $markerDeadline = [DateTime]::UtcNow.AddSeconds(10)
  while (-not (Test-Path -LiteralPath $markerPath) -and [DateTime]::UtcNow -lt $markerDeadline) {
    Start-Sleep -Milliseconds 50
  }
  if (-not (Test-Path -LiteralPath $markerPath)) {
    Stop-Job $firstRun -ErrorAction SilentlyContinue
    Remove-Job $firstRun -Force -ErrorAction SilentlyContinue
    throw "first concurrent benchmark did not acquire its output lock"
  }
  $secondRunRejected = $false
  try {
    & $benchmark @concurrentParams | Out-Null
  }
  catch {
    $secondRunRejected = $_.Exception.Message -like "*output directory is already in use*"
  }
  Assert-Equal $secondRunRejected $true "concurrent output-directory lock"
  Wait-Job $firstRun -Timeout 30 | Out-Null
  Receive-Job $firstRun -ErrorAction Stop | Out-Null
  Remove-Job $firstRun -Force
  Remove-Item Env:FAKE_AUDIO_MODE -ErrorAction SilentlyContinue

  & $benchmark @runParams | Out-Null

  $runResult = Get-Content -LiteralPath (Join-Path $runOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $runResult.mode "run" "run mode"
  Assert-Equal $runResult.decision.status "partial_measurements" "partial decision status"
  Assert-Equal $runResult.decision.synthesis_unit "chapter_batch_session" "synthesis unit"
  Assert-Equal $runResult.capabilities.persistent_tts $true "persistent TTS capability"
  foreach ($id in @("audio.per_line", "audio.batch_session", "audio.paragraph", "audio.chapter")) {
    $case = $runResult.cases | Where-Object id -eq $id
    Assert-Equal $case.status "complete" "$id status"
  }

  $requireCompleteParams = $runParams.Clone()
  $requireCompleteParams.OutDir = $requireCompleteOutDir
  $requireCompleteParams.RequireComplete = $true
  $requireCompleteFailed = $false
  try {
    & $benchmark @requireCompleteParams | Out-Null
  }
  catch {
    $requireCompleteFailed = $_.Exception.Message -like "*benchmark is incomplete*"
  }
  Assert-Equal $requireCompleteFailed $true "RequireComplete failure gate"
  if (-not (Test-Path -LiteralPath (Join-Path $requireCompleteOutDir "benchmark.json"))) {
    throw "RequireComplete did not retain diagnostic artifacts"
  }

  $env:FAKE_NVIDIA_HANG = "1"
  $gpuHangParams = $runParams.Clone()
  $gpuHangParams.OutDir = $gpuHangOutDir
  $gpuHangTimer = [System.Diagnostics.Stopwatch]::StartNew()
  & $benchmark @gpuHangParams | Out-Null
  $gpuHangTimer.Stop()
  if ($gpuHangTimer.Elapsed.TotalSeconds -gt 15) {
    throw "hung nvidia-smi was not disabled promptly: $($gpuHangTimer.Elapsed.TotalSeconds) seconds"
  }
  $gpuHangResult = Get-Content -LiteralPath (Join-Path $gpuHangOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $null $gpuHangResult.environment.gpu "hung nvidia-smi GPU snapshot"
  Remove-Item Env:FAKE_NVIDIA_HANG -ErrorAction SilentlyContinue

  foreach ($malformed in @(
    @{ mode = "short"; error = "*too small*" },
    @{ mode = "invalid_header"; error = "*RIFF/WAVE*" },
    @{ mode = "missing_metadata"; error = "*missing fmt/data metadata*" },
    @{ mode = "truncated_data"; error = "*truncated*" }
  )) {
    $env:FAKE_AUDIO_MODE = $malformed.mode
    $malformedOutDir = Join-Path $testRoot ("malformed-{0}" -f $malformed.mode)
    $malformedParams = $runParams.Clone()
    $malformedParams.OutDir = $malformedOutDir
    & $benchmark @malformedParams | Out-Null
    $malformedResult = Get-Content -LiteralPath (Join-Path $malformedOutDir "benchmark.json") -Raw | ConvertFrom-Json
    Assert-Equal $malformedResult.decision.status "blocked" "$($malformed.mode) decision status"
    foreach ($case in @($malformedResult.cases | Where-Object category -eq "audio")) {
      Assert-Equal $case.status "failed" "$($malformed.mode) $($case.id) status"
      if ($case.error -notlike $malformed.error) {
        throw "$($malformed.mode) $($case.id) returned unexpected error: $($case.error)"
      }
    }
  }

  $env:FAKE_AUDIO_MODE = "batch_missing_output"
  $batchFailureOutDir = Join-Path $testRoot "batch-missing-output"
  $batchFailureParams = $runParams.Clone()
  $batchFailureParams.OutDir = $batchFailureOutDir
  & $benchmark @batchFailureParams | Out-Null
  $batchFailureResult = Get-Content -LiteralPath (Join-Path $batchFailureOutDir "benchmark.json") -Raw | ConvertFrom-Json
  $batchFailureCase = $batchFailureResult.cases | Where-Object id -eq "audio.batch_session"
  Assert-Equal $batchFailureCase.status "failed" "batch output-count mismatch status"
  Assert-Equal $batchFailureResult.capabilities.persistent_tts $false "batch output-count capability"

  foreach ($manifestFailure in @(
    @{ mode = "manifest_missing"; error = "*did not write its manifest*" },
    @{ mode = "manifest_invalid_json"; error = "*invalid JSON*" },
    @{ mode = "manifest_count_only"; error = "*line-preserving requests metadata*" },
    @{ mode = "manifest_wrong_count"; error = "*manifest describes*" },
    @{ mode = "manifest_duplicate_id"; error = "*duplicate request id*" },
    @{ mode = "manifest_wrong_name"; error = "*no matching WAV*" },
    @{ mode = "manifest_wrong_samples"; error = "*metadata does not match WAV*" }
  )) {
    $env:FAKE_AUDIO_MODE = $manifestFailure.mode
    $manifestFailureOutDir = Join-Path $testRoot $manifestFailure.mode
    $manifestFailureParams = $runParams.Clone()
    $manifestFailureParams.OutDir = $manifestFailureOutDir
    & $benchmark @manifestFailureParams | Out-Null
    $manifestFailureResult = Get-Content -LiteralPath (Join-Path $manifestFailureOutDir "benchmark.json") -Raw | ConvertFrom-Json
    $manifestFailureCase = $manifestFailureResult.cases | Where-Object id -eq "audio.batch_session"
    Assert-Equal $manifestFailureCase.status "failed" "$($manifestFailure.mode) status"
    Assert-Equal $manifestFailureResult.capabilities.persistent_tts $false "$($manifestFailure.mode) capability"
    if ($manifestFailureCase.error -notlike $manifestFailure.error) {
      throw "$($manifestFailure.mode) returned unexpected error: $($manifestFailureCase.error)"
    }
  }

  $env:FAKE_AUDIO_MODE = "no_output"
  $staleOutDir = Join-Path $testRoot "stale-output"
  $staleCaseDir = Join-Path $staleOutDir "audio-per_line"
  New-Item -ItemType Directory -Force -Path $staleCaseDir | Out-Null
  Copy-Item -LiteralPath (Join-Path $runOutDir "audio-per_line\r01-line-01.wav") -Destination (Join-Path $staleCaseDir "r01-line-01.wav")
  $staleParams = $runParams.Clone()
  $staleParams.OutDir = $staleOutDir
  & $benchmark @staleParams | Out-Null
  $staleResult = Get-Content -LiteralPath (Join-Path $staleOutDir "benchmark.json") -Raw | ConvertFrom-Json
  $stalePerLine = $staleResult.cases | Where-Object id -eq "audio.per_line"
  Assert-Equal $stalePerLine.status "failed" "stale per-line output rejection"

  $env:FAKE_AUDIO_MODE = "hang"
  $timeoutOutDir = Join-Path $testRoot "timeout"
  $timeoutParams = $runParams.Clone()
  $timeoutParams.OutDir = $timeoutOutDir
  $timeoutParams.ProcessTimeoutSeconds = 1
  & $benchmark @timeoutParams | Out-Null
  $timeoutResult = Get-Content -LiteralPath (Join-Path $timeoutOutDir "benchmark.json") -Raw | ConvertFrom-Json
  foreach ($case in @($timeoutResult.cases | Where-Object category -eq "audio")) {
    Assert-Equal $case.status "failed" "timeout $($case.id) status"
    if ($case.error -notlike "*timed out*") {
      throw "timeout $($case.id) returned unexpected error: $($case.error)"
    }
  }
  Remove-Item Env:FAKE_AUDIO_MODE -ErrorAction SilentlyContinue

  $fakePlannerSource = @'
$deviceIndex = [Array]::IndexOf($args, "--device")
$isCPU = $deviceIndex -ge 0 -and $args[$deviceIndex + 1] -eq "none"
if ($env:FAKE_PLANNER_CPU_ONLY -eq "1" -and -not $isCPU) {
  exit 41
}
if ($isCPU) {
  $nglIndex = [Array]::IndexOf($args, "-ngl")
  $hasZeroLayers = $nglIndex -ge 0 -and $args[$nglIndex + 1] -eq "0"
  if (-not $hasZeroLayers -or "--no-op-offload" -notin $args -or "--no-kv-offload" -notin $args) {
    exit 43
  }
}
if ($env:FAKE_PLANNER_RESTART_MARKER -and -not $isCPU) {
  if (Test-Path -LiteralPath $env:FAKE_PLANNER_RESTART_MARKER) {
    exit 42
  }
  "started" | Set-Content -LiteralPath $env:FAKE_PLANNER_RESTART_MARKER -Encoding ascii
}
if ($env:FAKE_PLANNER_LIVENESS_MARKER) {
  "alive" | Set-Content -LiteralPath $env:FAKE_PLANNER_LIVENESS_MARKER -Encoding ascii
}
$listener = [System.Net.HttpListener]::new()
$listener.Prefixes.Add("http://127.0.0.1:__PORT__/")
$listener.Start()
try {
  while ($listener.IsListening) {
    $context = $listener.GetContext()
    $path = $context.Request.Url.AbsolutePath
    if ($path -eq "/health") {
      $body = '{"status":"ok"}'
      $context.Response.StatusCode = 200
    }
    elseif ($path -eq "/v1/chat/completions") {
      $plannerBroken = $env:FAKE_PLANNER_LIVENESS_MARKER -and -not (Test-Path -LiteralPath $env:FAKE_PLANNER_LIVENESS_MARKER)
      if ($env:FAKE_PLANNER_CHAT_MODE -eq "delay") {
        Start-Sleep -Seconds 3
      }
      if ($env:FAKE_PLANNER_CHAT_MODE -eq "http_error") {
        $body = '{"error":"planned"}'
        $context.Response.StatusCode = 503
      }
      elseif ($env:FAKE_PLANNER_CHAT_MODE -eq "invalid_json") {
        $body = 'not-json'
        $context.Response.StatusCode = 200
      }
      else {
        $body = if ($env:FAKE_PLANNER_BAD_CHAT -eq "1" -or $plannerBroken) { '{"choices":[]}' } else { '{"choices":[{"message":{"content":"ready"}}]}' }
        $context.Response.StatusCode = 200
      }
    }
    else {
      $body = '{"error":"not found"}'
      $context.Response.StatusCode = 404
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($body)
    $context.Response.ContentType = "application/json"
    $context.Response.ContentLength64 = $bytes.Length
    $context.Response.OutputStream.Write($bytes, 0, $bytes.Length)
    $context.Response.Close()
  }
}
finally {
  $listener.Stop()
  $listener.Close()
}
'@
  $fakePlannerSource.Replace("__PORT__", [string]$plannerPort) | Set-Content -LiteralPath $fakePlanner -Encoding utf8

  @{
    gateway = @{ host = "127.0.0.1"; port = 18768 }
    engines = @{
      llama = @{
        command = "pwsh.exe"
        args = @("-NoProfile", "-File", $fakePlanner, "-ngl", "99")
        mode = "server"
        healthUrl = $plannerHealthURL
        startupTimeoutSeconds = 10
      }
      audio = @{
        command = "pwsh.exe"
        args = @("-NoProfile", "-File", $fakeAudio)
        mode = "subprocess"
      }
    }
  } | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $combinedRunConfig -Encoding utf8

  $nonLoopbackObject = Get-Content -LiteralPath $combinedRunConfig -Raw | ConvertFrom-Json
  $nonLoopbackObject.engines.llama.healthUrl = "http://192.0.2.1:$plannerPort/health"
  $nonLoopbackObject | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $nonLoopbackConfig -Encoding utf8

  $nonLoopbackParams = @{
    AudioConfig = $runConfig
    CombinedConfig = $nonLoopbackConfig
    WorkloadPath = $testWorkloadPath
    OutDir = $nonLoopbackOutDir
    GpuPollMilliseconds = 50
  }
  & $benchmark @nonLoopbackParams | Out-Null
  $nonLoopbackResult = Get-Content -LiteralPath (Join-Path $nonLoopbackOutDir "benchmark.json") -Raw | ConvertFrom-Json
  foreach ($case in @($nonLoopbackResult.cases | Where-Object category -ne "audio")) {
    Assert-Equal $case.status "failed" "non-loopback $($case.id) status"
    if ($case.error -notlike "*must target loopback*") {
      throw "non-loopback $($case.id) returned unexpected error: $($case.error)"
    }
  }

  $combinedParams = @{
    AudioConfig = $runConfig
    CombinedConfig = $combinedRunConfig
    WorkloadPath = $testWorkloadPath
    OutDir = $combinedRunOutDir
    GpuPollMilliseconds = 50
  }
  & $benchmark @combinedParams | Out-Null
  $combinedResult = Get-Content -LiteralPath (Join-Path $combinedRunOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $combinedResult.decision.status "selected" "selected decision status"
  Assert-Equal $combinedResult.decision.resource_policy "coexist" "coexist resource policy at 1024 MiB boundary"
  Assert-Equal $combinedResult.decision.selected_audio_case "audio.batch_session" "selected audio case"
  Assert-Equal $combinedResult.decision.resource_synthesis_profile "audio.batch_session" "resource synthesis profile"
  Assert-Equal $combinedResult.cases.Where({ $_.id -eq "planner.gpu_audio_coexist" })[0].wav_files.Count 3 "GPU coexist batch output count"
  foreach ($id in @(
    "planner.gpu_startup",
    "planner.gpu_chat",
    "planner.gpu_restart",
    "planner.gpu_audio_coexist",
    "planner.cpu_startup",
    "planner.cpu_chat",
    "planner.cpu_audio_coexist"
  )) {
    $case = $combinedResult.cases | Where-Object id -eq $id
    Assert-Equal $case.status "complete" "$id status"
  }

  $env:FAKE_NVIDIA_USED = "15361"
  $exclusiveParams = $combinedParams.Clone()
  $exclusiveParams.OutDir = $exclusiveRunOutDir
  & $benchmark @exclusiveParams | Out-Null
  $exclusiveResult = Get-Content -LiteralPath (Join-Path $exclusiveRunOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $exclusiveResult.decision.resource_policy "exclusive_restart" "exclusive resource policy below headroom boundary"

  $env:FAKE_AUDIO_SLOW_COEXIST = "1"
  $slowCoexistParams = $combinedParams.Clone()
  $slowCoexistParams.OutDir = $slowCoexistOutDir
  & $benchmark @slowCoexistParams | Out-Null
  $slowCoexistResult = Get-Content -LiteralPath (Join-Path $slowCoexistOutDir "benchmark.json") -Raw | ConvertFrom-Json
  $slowGpuCoexistCase = $slowCoexistResult.cases.Where({ $_.id -eq "planner.gpu_audio_coexist" })[0]
  if ($slowGpuCoexistCase.real_time_factor -le 2) {
    throw "slow GPU coexistence did not exceed the RTF gate"
  }
  Assert-Equal $slowGpuCoexistCase.process_count 1 "slow GPU coexist batch process count"
  Assert-Equal $slowGpuCoexistCase.wav_files.Count 3 "slow GPU coexist batch output count"
  Assert-Equal $slowCoexistResult.decision.resource_policy "exclusive_restart" "slow GPU coexist fallback policy"

  $env:FAKE_PLANNER_CPU_ONLY = "1"
  $slowCpuCoexistParams = $combinedParams.Clone()
  $slowCpuCoexistParams.OutDir = $slowCpuCoexistOutDir
  & $benchmark @slowCpuCoexistParams | Out-Null
  $slowCpuCoexistResult = Get-Content -LiteralPath (Join-Path $slowCpuCoexistOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $null $slowCpuCoexistResult.decision.resource_policy "slow CPU coexist policy rejection"
  Remove-Item Env:FAKE_PLANNER_CPU_ONLY -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_AUDIO_SLOW_COEXIST -ErrorAction SilentlyContinue

  $env:FAKE_PLANNER_RESTART_MARKER = Join-Path $testRoot "gpu-restart.marker"
  $restartFailureParams = $combinedParams.Clone()
  $restartFailureParams.OutDir = $restartFailureOutDir
  & $benchmark @restartFailureParams | Out-Null
  $restartFailureResult = Get-Content -LiteralPath (Join-Path $restartFailureOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $restartFailureResult.cases.Where({ $_.id -eq "planner.gpu_restart" })[0].status "failed" "GPU restart failure status"
  Assert-Equal $restartFailureResult.decision.resource_policy "planner_cpu" "restart failure policy fallback"
  Remove-Item Env:FAKE_PLANNER_RESTART_MARKER -ErrorAction SilentlyContinue

  $env:FAKE_PLANNER_BAD_CHAT = "1"
  $badChatParams = $combinedParams.Clone()
  $badChatParams.OutDir = $badChatOutDir
  & $benchmark @badChatParams | Out-Null
  $badChatResult = Get-Content -LiteralPath (Join-Path $badChatOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $badChatResult.cases.Where({ $_.id -eq "planner.gpu_chat" })[0].status "failed" "bad GPU chat response status"
  Assert-Equal $badChatResult.cases.Where({ $_.id -eq "planner.cpu_chat" })[0].status "failed" "bad CPU chat response status"
  Assert-Equal $null $badChatResult.decision.resource_policy "bad chat resource policy"
  Remove-Item Env:FAKE_PLANNER_BAD_CHAT -ErrorAction SilentlyContinue

  foreach ($chatFailure in @(
    @{ mode = "invalid_json"; error = "*invalid JSON*"; timeout = 10 },
    @{ mode = "http_error"; error = "*HTTP 503*"; timeout = 10 },
    @{ mode = "delay"; error = "*timed out*"; timeout = 1 }
  )) {
    $env:FAKE_PLANNER_CHAT_MODE = $chatFailure.mode
    $chatFailureOutDir = Join-Path $testRoot ("chat-{0}" -f $chatFailure.mode)
    $chatFailureParams = $combinedParams.Clone()
    $chatFailureParams.OutDir = $chatFailureOutDir
    $chatFailureParams.ProcessTimeoutSeconds = $chatFailure.timeout
    & $benchmark @chatFailureParams | Out-Null
    $chatFailureResult = Get-Content -LiteralPath (Join-Path $chatFailureOutDir "benchmark.json") -Raw | ConvertFrom-Json
    foreach ($caseID in @("planner.gpu_chat", "planner.cpu_chat")) {
      $chatFailureCase = $chatFailureResult.cases | Where-Object id -eq $caseID
      Assert-Equal $chatFailureCase.status "failed" "$($chatFailure.mode) $caseID status"
      if ($chatFailureCase.error -notlike $chatFailure.error) {
        throw "$($chatFailure.mode) $caseID returned unexpected error: $($chatFailureCase.error)"
      }
    }
    Remove-Item Env:FAKE_PLANNER_CHAT_MODE -ErrorAction SilentlyContinue
  }

  $env:FAKE_PLANNER_LIVENESS_MARKER = Join-Path $testRoot "planner-liveness.marker"
  $env:FAKE_AUDIO_BREAK_PLANNER = "1"
  $coexistFailureParams = $combinedParams.Clone()
  $coexistFailureParams.OutDir = $coexistFailureOutDir
  & $benchmark @coexistFailureParams | Out-Null
  $coexistFailureResult = Get-Content -LiteralPath (Join-Path $coexistFailureOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $coexistFailureResult.cases.Where({ $_.id -eq "planner.gpu_audio_coexist" })[0].status "failed" "GPU coexist liveness failure status"
  Assert-Equal $coexistFailureResult.cases.Where({ $_.id -eq "planner.cpu_audio_coexist" })[0].status "failed" "CPU coexist liveness failure status"
  Assert-Equal $coexistFailureResult.decision.resource_policy "exclusive_restart" "coexist liveness failure fallback"
  Remove-Item Env:FAKE_PLANNER_LIVENESS_MARKER -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_AUDIO_BREAK_PLANNER -ErrorAction SilentlyContinue

  $env:FAKE_AUDIO_SLOW_RTF = "1"
  $performanceMissParams = $combinedParams.Clone()
  $performanceMissParams.OutDir = $performanceMissOutDir
  & $benchmark @performanceMissParams | Out-Null
  $performanceMissResult = Get-Content -LiteralPath (Join-Path $performanceMissOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $performanceMissResult.decision.status "performance_target_missed" "performance target gate"
  Assert-Equal $performanceMissResult.decision.real_time_factor_target 2 "fixed performance target"
  if ($performanceMissResult.decision.suggested_real_time_factor_target -le 2) {
    throw "performance miss did not expose a slower measured target"
  }
  Remove-Item Env:FAKE_AUDIO_SLOW_RTF -ErrorAction SilentlyContinue

  $env:FAKE_AUDIO_SLOW_BATCH = "1"
  $batchFallbackParams = $combinedParams.Clone()
  $batchFallbackParams.OutDir = $batchFallbackOutDir
  & $benchmark @batchFallbackParams | Out-Null
  $batchFallbackResult = Get-Content -LiteralPath (Join-Path $batchFallbackOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $batchFallbackResult.decision.status "selected" "batch performance fallback status"
  Assert-Equal $batchFallbackResult.decision.synthesis_unit "per_line_process" "batch performance fallback synthesis unit"
  Remove-Item Env:FAKE_AUDIO_SLOW_BATCH -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_AUDIO_SLOW_COEXIST -ErrorAction SilentlyContinue

  $env:FAKE_PLANNER_CPU_ONLY = "1"
  $cpuParams = $combinedParams.Clone()
  $cpuParams.OutDir = $cpuRunOutDir
  & $benchmark @cpuParams | Out-Null
  $cpuResult = Get-Content -LiteralPath (Join-Path $cpuRunOutDir "benchmark.json") -Raw | ConvertFrom-Json
  Assert-Equal $cpuResult.decision.resource_policy "planner_cpu" "CPU resource policy"
  Assert-Equal $cpuResult.cases.Where({ $_.id -eq "planner.gpu_chat" })[0].status "failed" "CPU policy GPU chat status"
  Assert-Equal $cpuResult.cases.Where({ $_.id -eq "planner.cpu_chat" })[0].status "complete" "CPU policy CPU chat status"
  Remove-Item Env:FAKE_PLANNER_CPU_ONLY -ErrorAction SilentlyContinue

  [pscustomobject]@{
    status = "passed"
    cases = $caseIDs.Count
    run_cases = @($runResult.cases | Where-Object status -eq "complete").Count
    combined_cases = @($combinedResult.cases | Where-Object status -eq "complete").Count
    json = $jsonPath
    markdown = $markdownPath
  } | ConvertTo-Json
}
finally {
  $env:PATH = $originalPath
  Remove-Item Env:FAKE_AUDIO_MODE -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_PLANNER_CPU_ONLY -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_NVIDIA_HANG -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_NVIDIA_USED -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_PLANNER_RESTART_MARKER -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_PLANNER_BAD_CHAT -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_PLANNER_CHAT_MODE -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_PLANNER_LIVENESS_MARKER -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_AUDIO_BREAK_PLANNER -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_AUDIO_SLOW_RTF -ErrorAction SilentlyContinue
  Remove-Item Env:FAKE_AUDIO_SLOW_BATCH -ErrorAction SilentlyContinue
  Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
