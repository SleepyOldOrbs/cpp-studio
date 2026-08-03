param(
  [string]$Config = ".\config.dramabox-local.example.json",
  [string]$Fixture = ".\testdata\benchmark\dramabox-factual.txt",
  [string]$OutDir = ".\out\dramabox-benchmark",
  [string]$GatewayUrl = "http://127.0.0.1:8765",
  [ValidateSet("cpu", "cuda")][string]$Backend = "cpu",
  [string]$VoiceId = "",
  [switch]$PlanOnly,
  [switch]$IncludeCuda,
  [int]$PollSeconds = 2,
  [int]$TimeoutMinutes = 180
)

$ErrorActionPreference = "Stop"

function Resolve-BenchmarkPath {
  param([string]$Path)
  if ([System.IO.Path]::IsPathRooted($Path)) {
    return [System.IO.Path]::GetFullPath($Path)
  }
  return [System.IO.Path]::GetFullPath((Join-Path (Get-Location) $Path))
}

function Get-TextSha256Hex {
  param([string]$Text)
  $sha = [System.Security.Cryptography.SHA256]::Create()
  try {
    $bytes = [System.Text.UTF8Encoding]::new($false).GetBytes($Text)
    return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
  }
  finally { $sha.Dispose() }
}

function New-DramaBoxCases {
  param([bool]$HasVoice, [bool]$WantsCuda)
  return @(
    [pscustomobject]@{ id = "plan.config"; required = $true; enabled = $true; description = "Plan-only config and fixture validation" }
    [pscustomobject]@{ id = "cpu.cold_text"; required = $true; enabled = $true; description = "Fresh-server cold factual paragraph" }
    [pscustomobject]@{ id = "cpu.warm_text"; required = $true; enabled = $true; description = "Same request against the warm runtime" }
    [pscustomobject]@{ id = "cpu.long_form"; required = $true; enabled = $true; description = "Multi-section factual excerpt" }
    [pscustomobject]@{ id = "voice.clone"; required = $false; enabled = $HasVoice; description = "Authorized stored reference" }
    [pscustomobject]@{ id = "cpu.mem_saver_off"; required = $true; enabled = $true; description = "Fresh server with mem_saver=false" }
    [pscustomobject]@{ id = "cpu.mem_saver_on"; required = $true; enabled = $true; description = "Fresh server with mem_saver=true" }
    [pscustomobject]@{ id = "cuda.explicit"; required = $false; enabled = $WantsCuda; description = "CUDA run only when explicitly requested" }
    [pscustomobject]@{ id = "recovery.cancel_restart"; required = $true; enabled = $true; description = "Cancellation and process-restart recovery" }
    [pscustomobject]@{ id = "fidelity.asr"; required = $true; enabled = $true; description = "ASR fidelity report for generated output" }
  )
}

$configPath = Resolve-BenchmarkPath $Config
$fixturePath = Resolve-BenchmarkPath $Fixture
$outPath = Resolve-BenchmarkPath $OutDir
if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) {
  throw "DramaBox benchmark config not found: $configPath"
}
if (-not (Test-Path -LiteralPath $fixturePath -PathType Leaf)) {
  throw "DramaBox benchmark fixture not found: $fixturePath"
}
try {
  $configObject = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
}
catch {
  throw "Decode DramaBox benchmark config: $($_.Exception.Message)"
}
if (-not $configObject.engines.dramabox) {
  throw "DramaBox benchmark config must declare engines.dramabox"
}
$fixtureText = (Get-Content -LiteralPath $fixturePath -Raw).Replace("`r`n", "`n").TrimEnd() + "`n"
if (($fixtureText -split "\s+" | Where-Object { $_ }).Count -lt 50) {
  throw "DramaBox benchmark fixture must contain at least 50 words"
}

$gateway = [Uri]$GatewayUrl
if ($gateway.Scheme -ne "http" -or $gateway.Host -notin @("127.0.0.1", "localhost", "::1")) {
  throw "DramaBox benchmark gateway must be a loopback HTTP URL"
}
if ($Backend -eq "cuda" -and -not $IncludeCuda) {
  throw "CUDA benchmarking requires explicit -IncludeCuda"
}

New-Item -ItemType Directory -Force -Path $outPath | Out-Null
$lockPath = Join-Path $outPath ".benchmark-running"
$lock = $null
try {
  try {
    $lock = [System.IO.File]::Open($lockPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
  }
  catch {
    throw "DramaBox benchmark output directory is already in use: $outPath"
  }
  $runId = "dramabox-{0}-{1}" -f ([DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ")), ([Guid]::NewGuid().ToString("N").Substring(0, 8))
  $cases = New-DramaBoxCases -HasVoice ($VoiceId -ne "") -WantsCuda ($IncludeCuda.IsPresent)
  $plan = [ordered]@{
    schemaVersion = "dramabox-benchmark.v1"
    runId = $runId
    mode = if ($PlanOnly) { "plan" } else { "run" }
    status = if ($PlanOnly) { "planned" } else { "starting" }
    createdAt = [DateTime]::UtcNow.ToString("o")
    backend = $Backend
    configPath = $configPath
    fixturePath = $fixturePath
    fixtureSha256 = Get-TextSha256Hex $fixtureText
    fixtureWords = ($fixtureText -split "\s+" | Where-Object { $_ }).Count
    voiceId = $VoiceId
    cases = $cases
    requiredMetrics = @(
      "runtimeIdentity", "modelIdentity", "backend", "device", "threads", "options", "seed",
      "coldLoadMs", "queueWaitMs", "synthesisMs", "verificationMs", "assemblyMs", "totalMs",
      "audioDurationSeconds", "realTimeFactor", "peakVramMiB", "residentVramMiB", "cpuPercent",
      "processMemoryMiB", "wavFormat", "outputBytes", "fidelity", "projectedChapterSeconds"
    )
    labels = [ordered]@{ interactive = "RTF <= 1"; batchUsable = "1 < RTF <= 5"; overnight = "RTF > 5"; failed = "load, timeout, OOM, invalid audio, or incomplete workload" }
    disclaimer = "Technical timing and fidelity evidence does not certify subjective voice quality. Local generation consumes compute and storage."
  }
  $planPath = Join-Path $outPath "benchmark-plan.json"
  $plan | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $planPath -Encoding utf8
  if ($PlanOnly) {
    $plan | ConvertTo-Json -Depth 10
    return
  }

  $request = [ordered]@{
    backend = $Backend
    includeCuda = $IncludeCuda.IsPresent
    voiceId = $VoiceId
  } | ConvertTo-Json -Depth 8
  $created = Invoke-RestMethod -Method Post -Uri "$($gateway.AbsoluteUri.TrimEnd('/'))/v1/audiobooks/benchmark" -ContentType "application/json" -Body $request
  if (-not $created.id) {
    throw "Benchmark API did not return a job id"
  }
  $deadline = [DateTime]::UtcNow.AddMinutes($TimeoutMinutes)
  do {
    Start-Sleep -Seconds ([Math]::Max(1, $PollSeconds))
    $job = Invoke-RestMethod -Method Get -Uri "$($gateway.AbsoluteUri.TrimEnd('/'))/v1/jobs/$($created.id)"
    if ($job.status -in @("failed", "cancelled")) {
      try {
        $failedResult = Invoke-RestMethod -Method Get -Uri "$($gateway.AbsoluteUri.TrimEnd('/'))/v1/audiobooks/benchmark/results/$($created.id)"
        $failedResult | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $outPath "benchmark.json") -Encoding utf8
      }
      catch {
      }
      throw "DramaBox benchmark job $($job.status): $($job.error)"
    }
    if ([DateTime]::UtcNow -ge $deadline) {
      throw "DramaBox benchmark timed out after $TimeoutMinutes minutes; job $($created.id) remains inspectable"
    }
  } while ($job.status -ne "complete")
  $result = Invoke-RestMethod -Method Get -Uri "$($gateway.AbsoluteUri.TrimEnd('/'))/v1/audiobooks/benchmark/results/$($created.id)"
  if ($result.fixtureSha256 -ne $plan.fixtureSha256) {
    throw "Server benchmark fixture does not match the checked-in fixture; refresh before comparing results"
  }
  $result | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $outPath "benchmark.json") -Encoding utf8
  $result | ConvertTo-Json -Depth 20
}
finally {
  if ($lock) { $lock.Dispose() }
  if (Test-Path -LiteralPath $lockPath) { Remove-Item -LiteralPath $lockPath -Force }
}
