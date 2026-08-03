param()

$ErrorActionPreference = "Stop"
$benchmark = Join-Path $PSScriptRoot "benchmark-dramabox-audiobook.ps1"
$fixture = Join-Path (Split-Path $PSScriptRoot -Parent) "testdata\benchmark\dramabox-factual.txt"
$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) "cpp-studio-dramabox-benchmark-$PID"
$config = Join-Path $testRoot "config.json"
$outDir = Join-Path $testRoot "out"
New-Item -ItemType Directory -Force -Path $testRoot | Out-Null
try {
  @{ gateway = @{ host = "127.0.0.1"; port = 8765 }; engines = @{ dramabox = @{ command = "fixture-dramabox"; mode = "subprocess" } } } |
    ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $config -Encoding utf8
  & $benchmark -Config $config -Fixture $fixture -OutDir $outDir -PlanOnly | Out-Null
  $planPath = Join-Path $outDir "benchmark-plan.json"
  if (-not (Test-Path -LiteralPath $planPath)) { throw "plan-only did not publish benchmark-plan.json" }
  if (Test-Path -LiteralPath (Join-Path $outDir ".benchmark-running")) { throw "plan-only left its lock marker" }
  $plan = Get-Content -LiteralPath $planPath -Raw | ConvertFrom-Json
  if ($plan.schemaVersion -ne "dramabox-benchmark.v1" -or $plan.mode -ne "plan" -or $plan.status -ne "planned") {
    throw "unexpected plan identity: $($plan | ConvertTo-Json -Compress)"
  }
  $ids = @($plan.cases | ForEach-Object id)
  foreach ($required in @("plan.config", "cpu.cold_text", "cpu.warm_text", "cpu.long_form", "voice.clone", "cpu.mem_saver_off", "cpu.mem_saver_on", "cuda.explicit", "recovery.cancel_restart", "fidelity.asr")) {
    if ($required -notin $ids) { throw "missing benchmark case $required" }
  }
  if (($plan.requiredMetrics -notcontains "realTimeFactor") -or ($plan.requiredMetrics -notcontains "projectedChapterSeconds")) {
    throw "plan omitted required measurement fields"
  }
  $cudaRejected = $false
  try { & $benchmark -Config $config -Fixture $fixture -OutDir (Join-Path $testRoot "cuda") -Backend cuda -PlanOnly | Out-Null } catch { $cudaRejected = $_.Exception.Message -like "*requires explicit -IncludeCuda*" }
  if (-not $cudaRejected) { throw "implicit CUDA benchmark was not rejected" }
  $remoteRejected = $false
  try { & $benchmark -Config $config -Fixture $fixture -OutDir (Join-Path $testRoot "remote") -GatewayUrl "http://192.0.2.1:8765" -PlanOnly | Out-Null } catch { $remoteRejected = $_.Exception.Message -like "*loopback*" }
  if (-not $remoteRejected) { throw "non-loopback benchmark gateway was not rejected" }
  [pscustomobject]@{ status = "passed"; cases = $ids.Count; plan = $planPath } | ConvertTo-Json
}
finally {
  Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
