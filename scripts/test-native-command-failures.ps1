$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$failureTestRoot = Join-Path ([IO.Path]::GetTempPath()) ('cpp-studio-native-failure-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $failureTestRoot 'scripts') -Force | Out-Null
$testShell = (Get-Process -Id $PID).Path
$calls = [Collections.Generic.List[string]]::new()
$failAt = ''

function Invoke-StubNative([string]$Label) {
  $calls.Add($Label)
  if ($Label -eq $failAt) { & $testShell -NoProfile -Command 'exit 7' }
  else { & $testShell -NoProfile -Command 'exit 0' }
}
function gofmt { Invoke-StubNative 'gofmt' }
function node { Invoke-StubNative 'node' }
function go {
  if ($args[0] -eq 'env') {
    Invoke-StubNative 'env'
    if ($args[1] -eq 'GOHOSTOS') { 'windows' } else { 'amd64' }
  } elseif ($args[0] -eq 'run') {
    Invoke-StubNative ('run:' + [IO.Path]::GetFileName(($args | Where-Object { $_ -like '*.json' })))
  } else { Invoke-StubNative $args[0] }
}

# The real gate runs against inert downstream scripts in a temporary cwd.
foreach ($name in @('test-benchmark-story-local.ps1', 'smoke-demo-ui.ps1')) {
  Set-Content -LiteralPath (Join-Path $failureTestRoot "scripts/$name") -Value '# inert downstream stage'
}
Push-Location $failureTestRoot
try {
  foreach ($failAt in @('gofmt', 'test', 'vet', 'node', 'run:config.ci.json', 'run:config.smoke.json')) {
    $calls.Clear(); $rejected = $false
    try { & (Join-Path $repositoryRoot 'scripts/verify.ps1') } catch {
      if ($_.Exception.Message -notmatch 'failed.*7') { throw }
      $rejected = $true
    }
    if (-not $rejected -or $calls[$calls.Count - 1] -ne $failAt) {
      throw "verification did not stop at $failAt (calls: $calls)"
    }
  }
  Copy-Item -LiteralPath (Join-Path $repositoryRoot 'scripts/package-release.ps1') -Destination (Join-Path $failureTestRoot 'scripts/package-release.ps1')
  $failAt = 'build'; $calls.Clear(); $rejected = $false
  try { & (Join-Path $failureTestRoot 'scripts/package-release.ps1') -Runtime windows-amd64 } catch {
    if ($_.Exception.Message -notmatch 'failed.*7') { throw }
    $rejected = $true
  }
  if (-not $rejected -or @($calls | Where-Object { $_ -eq 'build' }).Count -ne 1) {
    throw 'packaging continued after a failed build'
  }
  'Native failure checks passed: verification stages and release build stop immediately.'
} finally { Pop-Location }

# Expected failing child processes must not become the CI step's exit status.
exit 0
