param(
  [string]$AudioConfig = ".\config.audio-local.example.json",
  [string]$CombinedConfig = ".\config.real.json",
  [string]$WorkloadPath = ".\testdata\benchmark\chapter-lines.txt",
  [string]$OutDir = ".\out\local-engine-benchmark",
  [int]$RepeatCount = 1,
  [int]$GpuPollMilliseconds = 200,
  [int]$ProcessTimeoutSeconds = 600,
  [switch]$PlanOnly,
  [switch]$SkipPlanner,
  [switch]$RequireComplete
)

$ErrorActionPreference = "Stop"

$MaximumAcceptedRtf = 2.0
$RtfAllowanceFactor = 1.25
$RtfRoundingStep = 0.1
$MinimumGpuHeadroomMiB = 1024
$GpuSnapshotTimeoutMilliseconds = 3000
$script:GpuSamplingDisabled = $false

function Resolve-InputPath {
  param([string]$Path)

  if ([System.IO.Path]::IsPathRooted($Path)) {
    return [System.IO.Path]::GetFullPath($Path)
  }
  return [System.IO.Path]::GetFullPath((Join-Path (Get-Location) $Path))
}

function New-BenchmarkCases {
  @(
    [pscustomobject]@{ id = "audio.per_line"; category = "audio"; group = "audio"; description = "One audio.cpp process per script line"; status = "planned" }
    [pscustomobject]@{ id = "audio.batch_session"; category = "audio"; group = "audio"; description = "One audio.cpp offline batch session for all lines"; status = "planned" }
    [pscustomobject]@{ id = "audio.paragraph"; category = "audio"; group = "audio"; description = "One process for a paragraph-sized chunk"; status = "planned" }
    [pscustomobject]@{ id = "audio.chapter"; category = "audio"; group = "audio"; description = "One process for a chapter-sized chunk"; status = "planned" }
    [pscustomobject]@{ id = "planner.gpu_startup"; category = "planner"; group = "gpu_session"; description = "GPU llama-server cold startup"; status = "planned" }
    [pscustomobject]@{ id = "planner.gpu_chat"; category = "planner"; group = "gpu_session"; description = "GPU planner chat latency"; status = "planned" }
    [pscustomobject]@{ id = "planner.gpu_restart"; category = "planner"; group = "gpu_restart"; description = "GPU planner stop and restart cost"; status = "planned" }
    [pscustomobject]@{ id = "planner.gpu_audio_coexist"; category = "resource"; group = "gpu_session"; description = "audio.cpp while GPU llama-server is resident"; status = "planned" }
    [pscustomobject]@{ id = "planner.cpu_startup"; category = "planner"; group = "cpu_session"; description = "CPU llama-server cold startup"; status = "planned" }
    [pscustomobject]@{ id = "planner.cpu_chat"; category = "planner"; group = "cpu_session"; description = "CPU planner chat latency"; status = "planned" }
    [pscustomobject]@{ id = "planner.cpu_audio_coexist"; category = "resource"; group = "cpu_session"; description = "audio.cpp while CPU llama-server is resident"; status = "planned" }
  )
}

function Read-BenchmarkConfig {
  param([string]$Path)

  if (-not (Test-Path -LiteralPath $Path)) {
    throw "config not found: $Path"
  }
  try {
    return Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
  }
  catch {
    throw "decode config '$Path': $($_.Exception.Message)"
  }
}

function Resolve-EngineCommand {
  param(
    [pscustomobject]$Engine,
    [string]$ConfigPath
  )

  $configDir = Split-Path -Parent $ConfigPath
  $workingDir = if ($Engine.workingDir) {
    if ([System.IO.Path]::IsPathRooted([string]$Engine.workingDir)) {
      [System.IO.Path]::GetFullPath([string]$Engine.workingDir)
    }
    else {
      [System.IO.Path]::GetFullPath((Join-Path $configDir ([string]$Engine.workingDir)))
    }
  }
  else {
    [System.IO.Path]::GetFullPath((Get-Location).Path)
  }

  $command = [string]$Engine.command
  if (-not $command) {
    throw "engine command is required"
  }
  if ([System.IO.Path]::IsPathRooted($command)) {
    $commandPath = [System.IO.Path]::GetFullPath($command)
  }
  elseif ($command.Contains("\") -or $command.Contains("/")) {
    $commandPath = [System.IO.Path]::GetFullPath((Join-Path $workingDir $command))
  }
  else {
    $resolved = Get-Command $command -ErrorAction Stop
    $commandPath = $resolved.Source
  }
  if (-not (Test-Path -LiteralPath $commandPath)) {
    throw "engine command not found: $commandPath"
  }

  return [pscustomobject]@{
    command = $commandPath
    args = @($Engine.args | ForEach-Object { [string]$_ })
    working_dir = $workingDir
  }
}

function Get-GpuSnapshot {
  if ($script:GpuSamplingDisabled) {
    return $null
  }
  $nvidia = Get-Command nvidia-smi -ErrorAction SilentlyContinue
  if (-not $nvidia) {
    return $null
  }
  $process = $null
  $started = $false
  try {
    $arguments = @("--query-gpu=name,memory.total,memory.used", "--format=csv,noheader,nounits")
    $filePath = $nvidia.Source
    if ([System.IO.Path]::GetExtension($filePath) -in @(".cmd", ".bat")) {
      $arguments = @("/d", "/c", $filePath) + $arguments
      $filePath = $env:ComSpec
    }
    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = New-ProcessStartInfo -FilePath $filePath -Arguments $arguments -WorkingDirectory (Get-Location).Path
    if (-not $process.Start()) {
      return $null
    }
    $started = $true
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    if (-not $process.WaitForExit($GpuSnapshotTimeoutMilliseconds)) {
      $process.Kill($true)
      $process.WaitForExit(1000) | Out-Null
      $script:GpuSamplingDisabled = $true
      return $null
    }
    $line = @($stdoutTask.GetAwaiter().GetResult() -split "`r?`n" | Where-Object { $_ } | Select-Object -First 1)
    $stderrTask.GetAwaiter().GetResult() | Out-Null
    if (-not $line) {
      return $null
    }
    $parts = @($line[0] -split "," | ForEach-Object { $_.Trim() })
    if ($parts.Count -lt 3) {
      return $null
    }
    return [pscustomobject]@{
      name = $parts[0]
      total_mb = [int]$parts[1]
      used_mb = [int]$parts[2]
      free_mb = [int]$parts[1] - [int]$parts[2]
    }
  }
  catch {
    return $null
  }
  finally {
    if ($process) {
      if ($started -and -not $process.HasExited) {
        try {
          $process.Kill($true)
          $process.WaitForExit(1000) | Out-Null
        }
        catch {
        }
      }
      $process.Dispose()
    }
  }
}

function New-ProcessStartInfo {
  param(
    [string]$FilePath,
    [string[]]$Arguments,
    [string]$WorkingDirectory
  )

  $info = [System.Diagnostics.ProcessStartInfo]::new()
  $info.FileName = $FilePath
  $info.WorkingDirectory = $WorkingDirectory
  $info.UseShellExecute = $false
  $info.CreateNoWindow = $true
  $info.RedirectStandardOutput = $true
  $info.RedirectStandardError = $true
  foreach ($argument in $Arguments) {
    $info.ArgumentList.Add([string]$argument)
  }
  return $info
}

function Invoke-MeasuredProcess {
  param(
    [string]$FilePath,
    [string[]]$Arguments,
    [string]$WorkingDirectory,
    [int]$PollMilliseconds,
    [int]$TimeoutSeconds
  )

  $process = [System.Diagnostics.Process]::new()
  $process.StartInfo = New-ProcessStartInfo -FilePath $FilePath -Arguments $Arguments -WorkingDirectory $WorkingDirectory
  $before = Get-GpuSnapshot
  $peakUsed = if ($before) { $before.used_mb } else { $null }
  $timer = [System.Diagnostics.Stopwatch]::StartNew()
  if (-not $process.Start()) {
    throw "failed to start process: $FilePath"
  }
  $stdoutTask = $process.StandardOutput.ReadToEndAsync()
  $stderrTask = $process.StandardError.ReadToEndAsync()
  try {
    while (-not $process.WaitForExit($PollMilliseconds)) {
      if ($timer.Elapsed.TotalSeconds -ge $TimeoutSeconds) {
        $process.Kill($true)
        $process.WaitForExit(5000) | Out-Null
        throw "process timed out after $TimeoutSeconds seconds: $FilePath"
      }
      $snapshot = Get-GpuSnapshot
      if ($snapshot -and ($null -eq $peakUsed -or $snapshot.used_mb -gt $peakUsed)) {
        $peakUsed = $snapshot.used_mb
      }
    }
    $timer.Stop()
    $stdout = $stdoutTask.GetAwaiter().GetResult()
    $stderr = $stderrTask.GetAwaiter().GetResult()
    if ($process.ExitCode -ne 0) {
      $detail = ($stderr.Trim(), $stdout.Trim() | Where-Object { $_ }) -join " | "
      throw "process exited $($process.ExitCode): $detail"
    }
    return [pscustomobject]@{
      elapsed_seconds = [math]::Round($timer.Elapsed.TotalSeconds, 3)
      peak_gpu_used_mb = $peakUsed
      baseline_gpu_used_mb = if ($before) { $before.used_mb } else { $null }
      stdout = $stdout
      stderr = $stderr
    }
  }
  finally {
    if (-not $process.HasExited) {
      try {
        $process.Kill($true)
        $process.WaitForExit(5000) | Out-Null
      }
      catch {
      }
    }
    $process.Dispose()
  }
}

function Get-WavInfo {
  param([string]$Path)

  $bytes = [System.IO.File]::ReadAllBytes((Resolve-Path -LiteralPath $Path).Path)
  if ($bytes.Length -lt 44) {
    throw "WAV file is too small: $Path"
  }
  if ([System.Text.Encoding]::ASCII.GetString($bytes, 0, 4) -ne "RIFF" -or
      [System.Text.Encoding]::ASCII.GetString($bytes, 8, 4) -ne "WAVE") {
    throw "WAV file does not have a RIFF/WAVE header: $Path"
  }

  $channels = 0
  $sampleRate = 0
  $bitsPerSample = 0
  $dataBytes = 0
  $offset = 12
  while ($offset + 8 -le $bytes.Length) {
    $chunkID = [System.Text.Encoding]::ASCII.GetString($bytes, $offset, 4)
    $chunkSize = [BitConverter]::ToUInt32($bytes, $offset + 4)
    $dataOffset = $offset + 8
    if ([uint64]$dataOffset + [uint64]$chunkSize -gt [uint64]$bytes.Length) {
      throw "WAV chunk '$chunkID' is truncated: $Path"
    }
    if ($chunkID -eq "fmt " -and $chunkSize -ge 16 -and $dataOffset + 16 -le $bytes.Length) {
      $channels = [BitConverter]::ToUInt16($bytes, $dataOffset + 2)
      $sampleRate = [BitConverter]::ToUInt32($bytes, $dataOffset + 4)
      $bitsPerSample = [BitConverter]::ToUInt16($bytes, $dataOffset + 14)
    }
    elseif ($chunkID -eq "data") {
      if ($chunkSize -gt [int]::MaxValue) {
        throw "WAV data chunk is too large: $Path"
      }
      $dataBytes = [int]$chunkSize
      break
    }
    $offset = $dataOffset + [int]$chunkSize
    if (($chunkSize % 2) -eq 1) {
      $offset++
    }
  }
  if ($channels -le 0 -or $sampleRate -le 0 -or $bitsPerSample -le 0 -or $dataBytes -le 0) {
    throw "WAV file is missing fmt/data metadata: $Path"
  }
  $bytesPerSecond = $sampleRate * $channels * ($bitsPerSample / 8)
  $sampleFrames = $dataBytes / ($channels * ($bitsPerSample / 8))
  return [pscustomobject]@{
    path = [System.IO.Path]::GetFullPath($Path)
    bytes = $bytes.Length
    channels = $channels
    sample_rate = $sampleRate
    bits_per_sample = $bitsPerSample
    samples = [int64]$sampleFrames
    data_bytes = $dataBytes
    duration_seconds = [math]::Round($dataBytes / $bytesPerSecond, 3)
  }
}

function New-CaseResult {
  param(
    [string]$ID,
    [string]$Category,
    [string]$Description,
    [string]$Status,
    [int]$ProcessCount = 0,
    [AllowNull()][object]$ElapsedSeconds,
    [AllowNull()][object]$AudioSeconds,
    [AllowNull()][object]$RealTimeFactor,
    [AllowNull()][object]$PeakGpuUsedMB,
    [object[]]$WavFiles = @(),
    [AllowNull()][string]$Notes,
    [AllowNull()][string]$ErrorMessage
  )

  return [pscustomobject]@{
    id = $ID
    category = $Category
    description = $Description
    status = $Status
    process_count = $ProcessCount
    elapsed_seconds = $ElapsedSeconds
    audio_seconds = $AudioSeconds
    real_time_factor = $RealTimeFactor
    peak_gpu_used_mb = $PeakGpuUsedMB
    wav_files = @($WavFiles)
    notes = $Notes
    error = $ErrorMessage
  }
}

function New-CompletedCase {
  param(
    [string]$ID,
    [string]$Category,
    [string]$Description,
    [int]$ProcessCount,
    [double]$ElapsedSeconds,
    [object[]]$WavFiles,
    [object]$PeakGpuUsedMB,
    [string]$Notes
  )

  $audioSeconds = [math]::Round((($WavFiles | Measure-Object -Property duration_seconds -Sum).Sum), 3)
  $rtf = if ($audioSeconds -gt 0) { [math]::Round($ElapsedSeconds / $audioSeconds, 3) } else { $null }
  return New-CaseResult -ID $ID -Category $Category -Description $Description -Status "complete" `
    -ProcessCount $ProcessCount -ElapsedSeconds ([math]::Round($ElapsedSeconds, 3)) `
    -AudioSeconds $audioSeconds -RealTimeFactor $rtf -PeakGpuUsedMB $PeakGpuUsedMB `
    -WavFiles $WavFiles -Notes $Notes -ErrorMessage $null
}

function New-FailedCase {
  param(
    [pscustomobject]$Planned,
    [string]$Status,
    [string]$ErrorMessage
  )

  return New-CaseResult -ID $Planned.id -Category $Planned.category -Description $Planned.description `
    -Status $Status -ErrorMessage $ErrorMessage
}

function Invoke-AudioCase {
  param(
    [string]$CaseID,
    [string]$ProfileID = $CaseID,
    [pscustomobject]$Command,
    [string[]]$Lines,
    [string]$Directory,
    [int]$Repeats,
    [int]$PollMilliseconds,
    [int]$TimeoutSeconds
  )

  $description = switch ($CaseID) {
    "audio.per_line" { "One audio.cpp process per script line" }
    "audio.batch_session" { "One audio.cpp offline batch session for all lines" }
    "audio.paragraph" { "One process for a paragraph-sized chunk" }
    "audio.chapter" { "One process for a chapter-sized chunk" }
    default { "Audio synthesis under planner coexistence" }
  }
  $caseDir = Join-Path $Directory ($CaseID -replace "\.", "-")
  if (Test-Path -LiteralPath $caseDir) {
    Remove-Item -LiteralPath $caseDir -Recurse -Force
  }
  New-Item -ItemType Directory -Force -Path $caseDir | Out-Null
  $allWavs = [System.Collections.Generic.List[object]]::new()
  $elapsed = 0.0
  $peak = $null
  $processCount = 0

  for ($repeat = 1; $repeat -le $Repeats; $repeat++) {
    if ($ProfileID -eq "audio.per_line") {
      for ($i = 0; $i -lt $Lines.Count; $i++) {
        $outPath = Join-Path $caseDir ("r{0:D2}-line-{1:D2}.wav" -f $repeat, ($i + 1))
        $args = @($Command.args) + @("--text", $Lines[$i], "--out", $outPath)
        $run = Invoke-MeasuredProcess -FilePath $Command.command -Arguments $args -WorkingDirectory $Command.working_dir -PollMilliseconds $PollMilliseconds -TimeoutSeconds $TimeoutSeconds
        $elapsed += $run.elapsed_seconds
        $processCount++
        if ($null -ne $run.peak_gpu_used_mb -and ($null -eq $peak -or $run.peak_gpu_used_mb -gt $peak)) {
          $peak = $run.peak_gpu_used_mb
        }
        $allWavs.Add((Get-WavInfo -Path $outPath))
      }
      continue
    }

    if ($ProfileID -eq "audio.batch_session") {
      $inputPath = Join-Path $caseDir ("r{0:D2}-batch.txt" -f $repeat)
      $outputDir = Join-Path $caseDir ("r{0:D2}-outputs" -f $repeat)
      $manifestPath = Join-Path $caseDir ("r{0:D2}-manifest.json" -f $repeat)
      $Lines | Set-Content -LiteralPath $inputPath -Encoding utf8
      New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
      $args = @($Command.args) + @("--batch-text-file", $inputPath, "--out-dir", $outputDir, "--batch-manifest-out", $manifestPath)
      $run = Invoke-MeasuredProcess -FilePath $Command.command -Arguments $args -WorkingDirectory $Command.working_dir -PollMilliseconds $PollMilliseconds -TimeoutSeconds $TimeoutSeconds
      $elapsed += $run.elapsed_seconds
      $processCount++
      if ($null -ne $run.peak_gpu_used_mb -and ($null -eq $peak -or $run.peak_gpu_used_mb -gt $peak)) {
        $peak = $run.peak_gpu_used_mb
      }
      $batchWavs = @(Get-ChildItem -LiteralPath $outputDir -Filter "*.wav" -File)
      if ($batchWavs.Count -ne $Lines.Count) {
        throw "batch session wrote $($batchWavs.Count) WAV files for $($Lines.Count) input lines"
      }
      if (-not (Test-Path -LiteralPath $manifestPath)) {
        throw "batch session did not write its manifest: $manifestPath"
      }
      try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
      }
      catch {
        throw "batch manifest is invalid JSON: $($_.Exception.Message)"
      }
      if ($null -eq $manifest.requests) {
        throw "batch manifest does not contain line-preserving requests metadata"
      }
      $requests = @($manifest.requests)
      if ($requests.Count -ne $Lines.Count) {
        throw "batch manifest describes $($requests.Count) outputs for $($Lines.Count) input lines"
      }
      $seenRequestIDs = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
      for ($i = 0; $i -lt $requests.Count; $i++) {
        $request = $requests[$i]
        $expectedID = "line_$($i + 1)"
        $requestID = [string]$request.id
        if (-not $seenRequestIDs.Add($requestID)) {
          throw "batch manifest contains duplicate request id: $requestID"
        }
        if ($requestID -ne $expectedID) {
          throw "batch manifest request $($i + 1) expected id '$expectedID', got '$requestID'"
        }
        $wavPath = Join-Path $outputDir "$requestID.wav"
        if (-not (Test-Path -LiteralPath $wavPath)) {
          throw "batch manifest request '$requestID' has no matching WAV: $wavPath"
        }
        $wavInfo = Get-WavInfo -Path $wavPath
        if ([int]$request.sample_rate -ne $wavInfo.sample_rate -or
            [int]$request.channels -ne $wavInfo.channels -or
            [int64]$request.samples -ne $wavInfo.samples) {
          throw "batch manifest metadata does not match WAV '$requestID'"
        }
        $allWavs.Add($wavInfo)
      }
      continue
    }

    $text = if ($ProfileID -eq "audio.chapter") {
      (($Lines -join " ") + " This chapter connects these stages into one continuous explanation. ") * 3
    }
    else {
      $Lines -join " "
    }
    $outPath = Join-Path $caseDir ("r{0:D2}.wav" -f $repeat)
    $args = @($Command.args) + @("--text", $text, "--out", $outPath)
    $run = Invoke-MeasuredProcess -FilePath $Command.command -Arguments $args -WorkingDirectory $Command.working_dir -PollMilliseconds $PollMilliseconds -TimeoutSeconds $TimeoutSeconds
    $elapsed += $run.elapsed_seconds
    $processCount++
    if ($null -ne $run.peak_gpu_used_mb -and ($null -eq $peak -or $run.peak_gpu_used_mb -gt $peak)) {
      $peak = $run.peak_gpu_used_mb
    }
    $allWavs.Add((Get-WavInfo -Path $outPath))
  }

  $completedParams = @{
    ID = $CaseID
    Category = $(if ($CaseID -like "planner.*") { "resource" } else { "audio" })
    Description = $description
    ProcessCount = $processCount
    ElapsedSeconds = $elapsed
    WavFiles = @($allWavs)
    PeakGpuUsedMB = $peak
    Notes = $(if ($ProfileID -eq "audio.batch_session") { "Profile: persistent offline session via --batch-text-file." } else { "Profile: $ProfileID." })
  }
  return New-CompletedCase @completedParams
}

function Get-CpuPlannerArgs {
  param([string[]]$Arguments)

  $copy = @($Arguments)
  $layersFound = $false
  $deviceFound = $false
  for ($i = 0; $i -lt $copy.Count - 1; $i++) {
    if ($copy[$i] -in @("-ngl", "--n-gpu-layers", "--gpu-layers")) {
      $copy[$i + 1] = "0"
      $layersFound = $true
    }
    if ($copy[$i] -in @("-dev", "--device")) {
      $copy[$i + 1] = "none"
      $deviceFound = $true
    }
  }
  if (-not $layersFound) {
    $copy += @("-ngl", "0")
  }
  if (-not $deviceFound) {
    $copy += @("--device", "none")
  }
  if ("--no-op-offload" -notin $copy) {
    $copy += "--no-op-offload"
  }
  if ("--no-kv-offload" -notin $copy) {
    $copy += "--no-kv-offload"
  }
  return ,$copy
}

function Assert-PortFree {
  param(
    [int]$Port,
    [string]$Label
  )

  $connections = Get-NetTCPConnection -LocalPort $Port -ErrorAction SilentlyContinue |
    Where-Object { $_.State -eq "Listen" -or $_.State -eq 2 }
  if ($connections) {
    $owners = ($connections | Select-Object -ExpandProperty OwningProcess -Unique) -join ", "
    throw "$Label port $Port is already in use by process id(s): $owners"
  }
}

function Assert-LoopbackURL {
  param([uri]$URI)

  if ($URI.Scheme -notin @("http", "https")) {
    throw "planner URL must use HTTP or HTTPS: $URI"
  }
  if ($URI.DnsSafeHost -notin @("127.0.0.1", "localhost", "::1")) {
    throw "planner URL must target loopback: $URI"
  }
}

function Start-PlannerServer {
  param(
    [pscustomobject]$Command,
    [string[]]$Arguments,
    [string]$HealthURL,
    [int]$StartupTimeoutSeconds,
    [int]$PollMilliseconds
  )

  $healthURI = [uri]$HealthURL
  Assert-LoopbackURL -URI $healthURI
  Assert-PortFree -Port $healthURI.Port -Label "planner"
  $process = [System.Diagnostics.Process]::new()
  $process.StartInfo = New-ProcessStartInfo -FilePath $Command.command -Arguments $Arguments -WorkingDirectory $Command.working_dir
  $before = Get-GpuSnapshot
  $peak = if ($before) { $before.used_mb } else { $null }
  $timer = [System.Diagnostics.Stopwatch]::StartNew()
  if (-not $process.Start()) {
    throw "failed to start planner: $($Command.command)"
  }
  $stdoutTask = $process.StandardOutput.ReadToEndAsync()
  $stderrTask = $process.StandardError.ReadToEndAsync()
  try {
    while ($timer.Elapsed.TotalSeconds -lt $StartupTimeoutSeconds) {
      if ($process.HasExited) {
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        throw "planner exited before healthy: $stderr $stdout"
      }
      $snapshot = Get-GpuSnapshot
      if ($snapshot -and ($null -eq $peak -or $snapshot.used_mb -gt $peak)) {
        $peak = $snapshot.used_mb
      }
      try {
        $response = Invoke-WebRequest -Uri $HealthURL -Method Get -TimeoutSec 2
        if ($response.StatusCode -eq 200) {
          $timer.Stop()
          return [pscustomobject]@{
            process = $process
            stdout_task = $stdoutTask
            stderr_task = $stderrTask
            startup_seconds = [math]::Round($timer.Elapsed.TotalSeconds, 3)
            peak_gpu_used_mb = $peak
            baseline_gpu_used_mb = if ($before) { $before.used_mb } else { $null }
            health_url = $HealthURL
          }
        }
      }
      catch {
      }
      Start-Sleep -Milliseconds $PollMilliseconds
    }
    throw "planner health check timed out after $StartupTimeoutSeconds seconds: $HealthURL"
  }
  catch {
    if (-not $process.HasExited) {
      Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
      $process.WaitForExit(5000) | Out-Null
    }
    $process.Dispose()
    throw
  }
}

function Stop-PlannerServer {
  param([pscustomobject]$Server)

  if (-not $Server) {
    return
  }
  $process = $Server.process
  try {
    if (-not $process.HasExited) {
      Stop-Process -Id $process.Id -ErrorAction SilentlyContinue
      if (-not $process.WaitForExit(5000)) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        $process.WaitForExit(5000) | Out-Null
      }
    }
  }
  finally {
    $process.Dispose()
  }
}

function Invoke-PlannerChat {
  param(
    [string]$HealthURL,
    [int]$PollMilliseconds,
    [int]$TimeoutSeconds
  )

  $healthURI = [uri]$HealthURL
  $chatURL = [System.UriBuilder]::new($healthURI)
  $chatURL.Path = $healthURI.AbsolutePath.Substring(0, $healthURI.AbsolutePath.Length - "/health".Length) + "/v1/chat/completions"
  $payload = @{
    model = "local"
    messages = @(@{ role = "user"; content = "Reply with exactly the word ready." })
    max_tokens = 8
    temperature = 0
  } | ConvertTo-Json -Depth 6

  $client = [System.Net.Http.HttpClient]::new()
  $client.Timeout = [TimeSpan]::FromSeconds($TimeoutSeconds)
  $content = [System.Net.Http.StringContent]::new($payload, [System.Text.Encoding]::UTF8, "application/json")
  $before = Get-GpuSnapshot
  $peak = if ($before) { $before.used_mb } else { $null }
  $timer = [System.Diagnostics.Stopwatch]::StartNew()
  try {
    $task = $client.PostAsync($chatURL.Uri, $content)
    while (-not $task.IsCompleted) {
      if ($timer.Elapsed.TotalSeconds -ge $TimeoutSeconds) {
        throw "planner chat timed out after $TimeoutSeconds seconds: $($chatURL.Uri)"
      }
      $delay = [System.Threading.Tasks.Task]::Delay($PollMilliseconds)
      $completed = [System.Threading.Tasks.Task]::WhenAny($task, $delay).GetAwaiter().GetResult()
      if ($completed -eq $task) {
        break
      }
      $snapshot = Get-GpuSnapshot
      if ($snapshot -and ($null -eq $peak -or $snapshot.used_mb -gt $peak)) {
        $peak = $snapshot.used_mb
      }
    }
    try {
      $response = $task.GetAwaiter().GetResult()
    }
    catch [System.Threading.Tasks.TaskCanceledException] {
      throw "planner chat timed out after $TimeoutSeconds seconds: $($chatURL.Uri)"
    }
    $body = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
    $timer.Stop()
    if (-not $response.IsSuccessStatusCode) {
      throw "planner chat returned HTTP $([int]$response.StatusCode): $body"
    }
    try {
      $decoded = $body | ConvertFrom-Json
    }
    catch {
      throw "planner chat returned invalid JSON: $($_.Exception.Message)"
    }
    $reply = [string]$decoded.choices[0].message.content
    if ($reply.Trim().ToLowerInvariant() -ne "ready") {
      throw "planner chat did not return the requested sentinel: $reply"
    }
    return [pscustomobject]@{
      elapsed_seconds = [math]::Round($timer.Elapsed.TotalSeconds, 3)
      peak_gpu_used_mb = $peak
      body = $body
      reply = $reply
    }
  }
  finally {
    $content.Dispose()
    $client.Dispose()
  }
}

function New-PlannerCase {
  param(
    [pscustomobject]$Planned,
    [double]$ElapsedSeconds,
    [object]$PeakGpuUsedMB,
    [string]$Notes
  )

  return New-CaseResult -ID $Planned.id -Category $Planned.category -Description $Planned.description `
    -Status "complete" -ProcessCount 1 -ElapsedSeconds ([math]::Round($ElapsedSeconds, 3)) `
    -PeakGpuUsedMB $PeakGpuUsedMB -Notes $Notes
}

function Write-BenchmarkArtifacts {
  param(
    [pscustomobject]$Result,
    [string]$Directory
  )

  New-Item -ItemType Directory -Force -Path $Directory | Out-Null
  $jsonPath = Join-Path $Directory "benchmark.json"
  $markdownPath = Join-Path $Directory "benchmark.md"
  $markerPath = Join-Path $Directory ".benchmark-running"
  $temporaryJSON = Join-Path $Directory (".benchmark-{0}.json.tmp" -f $Result.run_id)
  $temporaryMarkdown = Join-Path $Directory (".benchmark-{0}.md.tmp" -f $Result.run_id)

  $lines = [System.Collections.Generic.List[string]]::new()
  $lines.Add("# ENG-T0 Local Engine Feasibility")
  $lines.Add("")
  $lines.Add("- Run: $($Result.run_id)")
  $lines.Add("- Generated: $($Result.generated_at)")
  $lines.Add("- Mode: $($Result.mode)")
  $lines.Add("- Decision: $($Result.decision.status)")
  $lines.Add("- Audio config: $($Result.inputs.audio_config)")
  $lines.Add("- Combined config: $($Result.inputs.combined_config)")
  $lines.Add("- Workload: $($Result.inputs.workload_path) ($($Result.inputs.workload_lines) lines, $($Result.inputs.workload_words) words)")
  if ($Result.environment.gpu) {
    $lines.Add("- GPU: $($Result.environment.gpu.name) ($($Result.environment.gpu.used_mb)/$($Result.environment.gpu.total_mb) MiB used at start)")
  }
  $lines.Add("")
  $lines.Add("## Results")
  $lines.Add("")
  $lines.Add("| Case | Status | Processes | Wall seconds | Audio seconds | RTF | Peak GPU MiB |")
  $lines.Add("|---|---|---:|---:|---:|---:|---:|")
  foreach ($case in $Result.cases) {
    $lines.Add("| $($case.id) | $($case.status) | $($case.process_count) | $($case.elapsed_seconds) | $($case.audio_seconds) | $($case.real_time_factor) | $($case.peak_gpu_used_mb) |")
    if ($case.error) {
      $lines.Add("")
      $lines.Add("- $($case.id): $($case.error)")
    }
  }
  $lines.Add("")
  $lines.Add("## Decision")
  $lines.Add("")
  $lines.Add("- Status: $($Result.decision.status)")
  $lines.Add("- Synthesis unit: $($Result.decision.synthesis_unit)")
  $lines.Add("- Resource policy: $($Result.decision.resource_policy)")
  $lines.Add("- Observed selected-case RTF: $($Result.decision.observed_real_time_factor)")
  $lines.Add("- RTF target: $($Result.decision.real_time_factor_target)")
  $lines.Add("- Suggested measured RTF target: $($Result.decision.suggested_real_time_factor_target)")
  $lines.Add("- Reason: $($Result.decision.reason)")
  try {
    $Result | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $temporaryJSON -Encoding utf8
    $lines | Set-Content -LiteralPath $temporaryMarkdown -Encoding utf8
    Move-Item -LiteralPath $temporaryJSON -Destination $jsonPath -Force
    Move-Item -LiteralPath $temporaryMarkdown -Destination $markdownPath -Force
    Remove-Item -LiteralPath $markerPath -Force -ErrorAction SilentlyContinue
  }
  finally {
    Remove-Item -LiteralPath $temporaryJSON -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $temporaryMarkdown -Force -ErrorAction SilentlyContinue
  }

  return [pscustomobject]@{
    json = $jsonPath
    markdown = $markdownPath
  }
}

if ($RepeatCount -lt 1) {
  throw "RepeatCount must be at least 1"
}
if ($GpuPollMilliseconds -lt 50) {
  throw "GpuPollMilliseconds must be at least 50"
}
if ($ProcessTimeoutSeconds -lt 1) {
  throw "ProcessTimeoutSeconds must be at least 1"
}

$resolvedAudioConfig = Resolve-InputPath $AudioConfig
$resolvedCombinedConfig = Resolve-InputPath $CombinedConfig
$resolvedWorkloadPath = Resolve-InputPath $WorkloadPath
$resolvedOutDir = Resolve-InputPath $OutDir
$cases = @(New-BenchmarkCases)
if (-not (Test-Path -LiteralPath $resolvedWorkloadPath)) {
  throw "benchmark workload not found: $resolvedWorkloadPath"
}
$lines = @(Get-Content -LiteralPath $resolvedWorkloadPath | Where-Object { $_.Trim() })
if ($lines.Count -lt 2) {
  throw "benchmark workload must contain at least two non-empty lines: $resolvedWorkloadPath"
}
$workloadWordCount = [regex]::Matches(($lines -join " "), "\S+").Count

$result = [pscustomobject]@{
  schema_version = "eng-t0.v1"
  run_id = [guid]::NewGuid().ToString("n")
  generated_at = (Get-Date).ToUniversalTime().ToString("o")
  mode = if ($PlanOnly) { "plan" } else { "run" }
  inputs = [pscustomobject]@{
    audio_config = $resolvedAudioConfig
    combined_config = $resolvedCombinedConfig
    workload_path = $resolvedWorkloadPath
    workload_lines = $lines.Count
    workload_words = $workloadWordCount
    repeat_count = $RepeatCount
    gpu_poll_milliseconds = $GpuPollMilliseconds
    process_timeout_seconds = $ProcessTimeoutSeconds
  }
  policy = [pscustomobject]@{
    maximum_accepted_rtf = $MaximumAcceptedRtf
    rtf_allowance_factor = $RtfAllowanceFactor
    rtf_rounding_step = $RtfRoundingStep
    minimum_gpu_headroom_mib = $MinimumGpuHeadroomMiB
  }
  environment = [pscustomobject]@{}
  capabilities = [pscustomobject]@{
    persistent_tts = $false
  }
  cases = $cases
  decision = [pscustomobject]@{
    status = "pending_measurements"
    resource_policy = $null
    synthesis_unit = $null
    real_time_factor_target = $null
    reason = "Run the benchmark on the target machine before freezing schema v2."
  }
}

New-Item -ItemType Directory -Force -Path $resolvedOutDir | Out-Null
$lockPath = Join-Path $resolvedOutDir ".benchmark.lock"
$lockStream = $null
try {
  try {
    $lockStream = [System.IO.File]::Open(
      $lockPath,
      [System.IO.FileMode]::OpenOrCreate,
      [System.IO.FileAccess]::ReadWrite,
      [System.IO.FileShare]::None
    )
  }
  catch [System.IO.IOException] {
    throw "benchmark output directory is already in use: $resolvedOutDir"
  }
  $lockStream.SetLength(0)
  $lockBytes = [System.Text.Encoding]::ASCII.GetBytes($result.run_id)
  $lockStream.Write($lockBytes, 0, $lockBytes.Length)
  $lockStream.Flush($true)
  $result.run_id | Set-Content -LiteralPath (Join-Path $resolvedOutDir ".benchmark-running") -Encoding ascii

  if ($PlanOnly) {
    $artifacts = Write-BenchmarkArtifacts -Result $result -Directory $resolvedOutDir
    [pscustomobject]@{
      status = "planned"
      cases = $cases.Count
      json = $artifacts.json
      markdown = $artifacts.markdown
    } | ConvertTo-Json
    return
  }

$result.environment = [pscustomobject]@{
  os = [System.Environment]::OSVersion.VersionString
  powershell = $PSVersionTable.PSVersion.ToString()
  gpu = Get-GpuSnapshot
}

$plannedByID = @{}
$caseResults = [ordered]@{}
foreach ($case in $cases) {
  $plannedByID[$case.id] = $case
  $caseResults[$case.id] = $case
}
$audioCaseIDs = @($cases | Where-Object group -eq "audio" | ForEach-Object id)
$gpuSessionCaseIDs = @($cases | Where-Object group -eq "gpu_session" | ForEach-Object id)
$cpuSessionCaseIDs = @($cases | Where-Object group -eq "cpu_session" | ForEach-Object id)
$plannerCaseIDs = @($cases | Where-Object category -ne "audio" | ForEach-Object id)

try {
  $audioConfigObject = Read-BenchmarkConfig -Path $resolvedAudioConfig
  if (-not $audioConfigObject.engines.audio) {
    throw "audio config does not declare engines.audio: $resolvedAudioConfig"
  }
  $audioCommand = Resolve-EngineCommand -Engine $audioConfigObject.engines.audio -ConfigPath $resolvedAudioConfig
  foreach ($id in $audioCaseIDs) {
    try {
      $caseResults[$id] = Invoke-AudioCase -CaseID $id -Command $audioCommand -Lines $lines -Directory $resolvedOutDir -Repeats $RepeatCount -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds
    }
    catch {
      $caseResults[$id] = New-FailedCase -Planned $plannedByID[$id] -Status "failed" -ErrorMessage $_.Exception.Message
    }
  }
}
catch {
  foreach ($id in $audioCaseIDs) {
    $caseResults[$id] = New-FailedCase -Planned $plannedByID[$id] -Status "failed" -ErrorMessage $_.Exception.Message
  }
}

$batchProfile = $caseResults["audio.batch_session"]
$perLineProfile = $caseResults["audio.per_line"]
$batchMeetsTarget = $batchProfile.status -eq "complete" -and $null -ne $batchProfile.real_time_factor -and $batchProfile.real_time_factor -le $MaximumAcceptedRtf
$perLineMeetsTarget = $perLineProfile.status -eq "complete" -and $null -ne $perLineProfile.real_time_factor -and $perLineProfile.real_time_factor -le $MaximumAcceptedRtf
$isolatedSynthesisProfileID = if ($batchMeetsTarget) {
  "audio.batch_session"
}
elseif ($perLineMeetsTarget) {
  "audio.per_line"
}
elseif ($batchProfile.status -eq "complete") {
  "audio.batch_session"
}
elseif ($perLineProfile.status -eq "complete") {
  "audio.per_line"
}
else {
  $null
}

if ($SkipPlanner) {
  foreach ($id in $plannerCaseIDs) {
    $caseResults[$id] = New-FailedCase -Planned $plannedByID[$id] -Status "skipped" -ErrorMessage "Planner measurements skipped by -SkipPlanner."
  }
}
else {
  try {
    $combinedConfigObject = Read-BenchmarkConfig -Path $resolvedCombinedConfig
    if (-not $combinedConfigObject.engines.llama) {
      throw "combined config does not declare engines.llama: $resolvedCombinedConfig"
    }
    $plannerCommand = Resolve-EngineCommand -Engine $combinedConfigObject.engines.llama -ConfigPath $resolvedCombinedConfig
    $plannerArgs = @($plannerCommand.args)
    $healthURL = [string]$combinedConfigObject.engines.llama.healthUrl
    if (-not $healthURL) {
      throw "combined config engines.llama.healthUrl is required"
    }
    $startupTimeout = [int]$combinedConfigObject.engines.llama.startupTimeoutSeconds
    if ($startupTimeout -le 0) {
      $startupTimeout = 120
    }

    $gpuServer = $null
    try {
      $gpuServer = Start-PlannerServer -Command $plannerCommand -Arguments $plannerArgs -HealthURL $healthURL -StartupTimeoutSeconds $startupTimeout -PollMilliseconds $GpuPollMilliseconds
      $caseResults["planner.gpu_startup"] = New-PlannerCase -Planned $plannedByID["planner.gpu_startup"] -ElapsedSeconds $gpuServer.startup_seconds -PeakGpuUsedMB $gpuServer.peak_gpu_used_mb -Notes "GPU args from combined config."
      try {
        $chat = Invoke-PlannerChat -HealthURL $healthURL -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds
        $caseResults["planner.gpu_chat"] = New-PlannerCase -Planned $plannedByID["planner.gpu_chat"] -ElapsedSeconds $chat.elapsed_seconds -PeakGpuUsedMB $chat.peak_gpu_used_mb -Notes "OpenAI-compatible chat completion."
      }
      catch {
        $caseResults["planner.gpu_chat"] = New-FailedCase -Planned $plannedByID["planner.gpu_chat"] -Status "failed" -ErrorMessage $_.Exception.Message
      }
      try {
        if (-not $isolatedSynthesisProfileID) {
          throw "no complete line-preserving synthesis profile is available for coexistence"
        }
        $caseResults["planner.gpu_audio_coexist"] = Invoke-AudioCase -CaseID "planner.gpu_audio_coexist" -ProfileID $isolatedSynthesisProfileID -Command $audioCommand -Lines $lines -Directory $resolvedOutDir -Repeats 1 -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds
        Invoke-PlannerChat -HealthURL $healthURL -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds | Out-Null
        $caseResults["planner.gpu_audio_coexist"].notes = "Narrator completed and the resident GPU planner passed a second sentinel chat."
      }
      catch {
        $caseResults["planner.gpu_audio_coexist"] = New-FailedCase -Planned $plannedByID["planner.gpu_audio_coexist"] -Status "failed" -ErrorMessage $_.Exception.Message
      }
    }
    catch {
      foreach ($id in $gpuSessionCaseIDs) {
        if ($caseResults[$id].status -eq "planned") {
          $caseResults[$id] = New-FailedCase -Planned $plannedByID[$id] -Status "failed" -ErrorMessage $_.Exception.Message
        }
      }
    }
    finally {
      Stop-PlannerServer -Server $gpuServer
      Start-Sleep -Milliseconds 250
    }

    $restartServer = $null
    try {
      $restartServer = Start-PlannerServer -Command $plannerCommand -Arguments $plannerArgs -HealthURL $healthURL -StartupTimeoutSeconds $startupTimeout -PollMilliseconds $GpuPollMilliseconds
      Invoke-PlannerChat -HealthURL $healthURL -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds | Out-Null
      $caseResults["planner.gpu_restart"] = New-PlannerCase -Planned $plannedByID["planner.gpu_restart"] -ElapsedSeconds $restartServer.startup_seconds -PeakGpuUsedMB $restartServer.peak_gpu_used_mb -Notes "Second cold start after owned server stop."
    }
    catch {
      $caseResults["planner.gpu_restart"] = New-FailedCase -Planned $plannedByID["planner.gpu_restart"] -Status "failed" -ErrorMessage $_.Exception.Message
    }
    finally {
      Stop-PlannerServer -Server $restartServer
      Start-Sleep -Milliseconds 250
    }

    $cpuServer = $null
    try {
      $cpuArgs = Get-CpuPlannerArgs -Arguments $plannerArgs
      $cpuServer = Start-PlannerServer -Command $plannerCommand -Arguments $cpuArgs -HealthURL $healthURL -StartupTimeoutSeconds $startupTimeout -PollMilliseconds $GpuPollMilliseconds
      $caseResults["planner.cpu_startup"] = New-PlannerCase -Planned $plannedByID["planner.cpu_startup"] -ElapsedSeconds $cpuServer.startup_seconds -PeakGpuUsedMB $cpuServer.peak_gpu_used_mb -Notes "GPU layer args forced to zero."
      try {
        $chat = Invoke-PlannerChat -HealthURL $healthURL -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds
        $caseResults["planner.cpu_chat"] = New-PlannerCase -Planned $plannedByID["planner.cpu_chat"] -ElapsedSeconds $chat.elapsed_seconds -PeakGpuUsedMB $chat.peak_gpu_used_mb -Notes "CPU planner chat completion."
      }
      catch {
        $caseResults["planner.cpu_chat"] = New-FailedCase -Planned $plannedByID["planner.cpu_chat"] -Status "failed" -ErrorMessage $_.Exception.Message
      }
      try {
        if (-not $isolatedSynthesisProfileID) {
          throw "no complete line-preserving synthesis profile is available for coexistence"
        }
        $caseResults["planner.cpu_audio_coexist"] = Invoke-AudioCase -CaseID "planner.cpu_audio_coexist" -ProfileID $isolatedSynthesisProfileID -Command $audioCommand -Lines $lines -Directory $resolvedOutDir -Repeats 1 -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds
        Invoke-PlannerChat -HealthURL $healthURL -PollMilliseconds $GpuPollMilliseconds -TimeoutSeconds $ProcessTimeoutSeconds | Out-Null
        $caseResults["planner.cpu_audio_coexist"].notes = "Narrator completed and the resident CPU planner passed a second sentinel chat."
      }
      catch {
        $caseResults["planner.cpu_audio_coexist"] = New-FailedCase -Planned $plannedByID["planner.cpu_audio_coexist"] -Status "failed" -ErrorMessage $_.Exception.Message
      }
    }
    catch {
      foreach ($id in $cpuSessionCaseIDs) {
        if ($caseResults[$id].status -eq "planned") {
          $caseResults[$id] = New-FailedCase -Planned $plannedByID[$id] -Status "failed" -ErrorMessage $_.Exception.Message
        }
      }
    }
    finally {
      Stop-PlannerServer -Server $cpuServer
    }
  }
  catch {
    foreach ($id in $plannerCaseIDs) {
      if ($caseResults[$id].status -eq "planned") {
        $caseResults[$id] = New-FailedCase -Planned $plannedByID[$id] -Status "failed" -ErrorMessage $_.Exception.Message
      }
    }
  }
}

$batch = $batchProfile
$perLine = $perLineProfile
$synthesisUnit = switch ($isolatedSynthesisProfileID) {
  "audio.batch_session" { "chapter_batch_session" }
  "audio.per_line" { "per_line_process" }
  default { $null }
}
$selectedAudioCase = if ($isolatedSynthesisProfileID) { $caseResults[$isolatedSynthesisProfileID] } else { $null }
$suggestedRtfTarget = if ($selectedAudioCase -and $null -ne $selectedAudioCase.real_time_factor) {
  $allowedRTF = $selectedAudioCase.real_time_factor * $RtfAllowanceFactor
  $roundedRTF = [math]::Ceiling($allowedRTF / $RtfRoundingStep) * $RtfRoundingStep
  [math]::Round($roundedRTF, 3)
}
else {
  $null
}
$performanceTargetMet = $selectedAudioCase -and $null -ne $selectedAudioCase.real_time_factor -and $selectedAudioCase.real_time_factor -le $MaximumAcceptedRtf
$result.capabilities.persistent_tts = $batch.status -eq "complete"
$result.cases = @($caseResults.Values)
$gpuCoexist = $caseResults["planner.gpu_audio_coexist"]
$cpuCoexist = $caseResults["planner.cpu_audio_coexist"]
$gpuChat = $caseResults["planner.gpu_chat"]
$gpuRestart = $caseResults["planner.gpu_restart"]
$cpuChat = $caseResults["planner.cpu_chat"]
$gpuHeadroom = if ($result.environment.gpu -and $null -ne $gpuCoexist.peak_gpu_used_mb) {
  $result.environment.gpu.total_mb - $gpuCoexist.peak_gpu_used_mb
}
else {
  $null
}
$gpuCoexistMeetsTarget = $gpuCoexist.status -eq "complete" -and $null -ne $gpuCoexist.real_time_factor -and $gpuCoexist.real_time_factor -le $MaximumAcceptedRtf
$cpuCoexistMeetsTarget = $cpuCoexist.status -eq "complete" -and $null -ne $cpuCoexist.real_time_factor -and $cpuCoexist.real_time_factor -le $MaximumAcceptedRtf
$resourcePolicy = if ($gpuChat.status -eq "complete" -and $gpuCoexistMeetsTarget -and $null -ne $gpuHeadroom -and $gpuHeadroom -ge $MinimumGpuHeadroomMiB) {
  "coexist"
}
elseif ($gpuChat.status -eq "complete" -and $gpuRestart.status -eq "complete" -and $selectedAudioCase) {
  "exclusive_restart"
}
elseif ($cpuChat.status -eq "complete" -and $cpuCoexistMeetsTarget) {
  "planner_cpu"
}
else {
  $null
}
$plannerMeasured = $gpuChat.status -eq "complete" -or $cpuChat.status -eq "complete"
$result.decision = [pscustomobject]@{
  status = if ($synthesisUnit -and $resourcePolicy -and $plannerMeasured -and $performanceTargetMet) {
    "selected"
  }
  elseif ($synthesisUnit -and $resourcePolicy -and $plannerMeasured) {
    "performance_target_missed"
  }
  elseif ($synthesisUnit) {
    "partial_measurements"
  }
  else {
    "blocked"
  }
  resource_policy = $resourcePolicy
  resource_synthesis_profile = $isolatedSynthesisProfileID
  gpu_coexist_real_time_factor = $gpuCoexist.real_time_factor
  cpu_coexist_real_time_factor = $cpuCoexist.real_time_factor
  synthesis_unit = $synthesisUnit
  selected_audio_case = if ($selectedAudioCase) { $selectedAudioCase.id } else { $null }
  observed_real_time_factor = if ($selectedAudioCase) { $selectedAudioCase.real_time_factor } else { $null }
  real_time_factor_target = $MaximumAcceptedRtf
  suggested_real_time_factor_target = $suggestedRtfTarget
  reason = if ($synthesisUnit -and $resourcePolicy -and $plannerMeasured -and $performanceTargetMet) {
    "Audio chunking and planner resource profiles completed. Apply the selected policies before schema v2."
  }
  elseif ($synthesisUnit -and $resourcePolicy -and $plannerMeasured) {
    "The selected synthesis profile missed the fixed RTF acceptance target. Review the suggested measured target before schema v2."
  }
  elseif ($synthesisUnit) {
    "Audio chunking measured; planner/resource measurements are incomplete."
  }
  else {
    "No audio synthesis profile completed."
  }
}

$artifacts = Write-BenchmarkArtifacts -Result $result -Directory $resolvedOutDir
[pscustomobject]@{
  status = $result.decision.status
  complete_cases = @($result.cases | Where-Object status -eq "complete").Count
  json = $artifacts.json
  markdown = $artifacts.markdown
} | ConvertTo-Json

  if ($RequireComplete -and $result.decision.status -ne "selected") {
    throw "benchmark is incomplete: $($result.decision.reason)"
  }
}
finally {
  if ($lockStream) {
    $lockStream.Dispose()
    Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
  }
}
