param(
    [int]$Port = 8777
)

$staticRoot = Join-Path $PSScriptRoot '..\internal\demo\static'
Write-Host "Story Builder prototype: http://127.0.0.1:$Port/story-builder-prototype.html?variant=A"
python -m http.server $Port --bind 127.0.0.1 --directory $staticRoot
