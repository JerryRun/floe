param(
    [Parameter(Mandatory = $true)]
    [string]$ServerExecutable,

    [string]$EdgeExecutable = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",

    [int]$DebugPort = 9228
)

$ErrorActionPreference = "Stop"
$standardOutput = "C:\Windows\Temp\floe-readme-demo-url.txt"
$standardError = "C:\Windows\Temp\floe-readme-demo-error.txt"
$browserProfile = "C:\Windows\Temp\floe-readme-demo-edge"

Get-Process "floe-readme-demo-server" -ErrorAction SilentlyContinue | Stop-Process -Force
Get-CimInstance Win32_Process |
    Where-Object { $_.CommandLine -like "*--remote-debugging-port=$DebugPort*" } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }

Remove-Item $standardOutput, $standardError -ErrorAction SilentlyContinue
$null = New-Item -ItemType Directory -Force -Path $browserProfile

$serverOptions = @{
    FilePath = $ServerExecutable
    RedirectStandardOutput = $standardOutput
    RedirectStandardError = $standardError
    WindowStyle = "Hidden"
    PassThru = $true
}
$server = Start-Process @serverOptions

$bootstrapURL = ""
for ($attempt = 0; $attempt -lt 30; $attempt++) {
    Start-Sleep -Milliseconds 200
    if (Test-Path $standardOutput) {
        $firstLine = Get-Content $standardOutput -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($firstLine) {
            $bootstrapURL = $firstLine.Trim()
            if ($bootstrapURL) { break }
        }
    }
    if ($server.HasExited) {
        $errorText = Get-Content $standardError -Raw -ErrorAction SilentlyContinue
        throw "Floe demo server exited before startup: $errorText"
    }
}
if (-not $bootstrapURL) {
    $errorText = Get-Content $standardError -Raw -ErrorAction SilentlyContinue
    throw "Timed out waiting for Floe demo server URL: $errorText"
}

$arguments = @(
    "--remote-debugging-port=$DebugPort",
    "--user-data-dir=$browserProfile",
    "--no-first-run",
    "--new-window",
    $bootstrapURL
)
$null = Start-Process -FilePath $EdgeExecutable -ArgumentList $arguments

for ($attempt = 0; $attempt -lt 30; $attempt++) {
    Start-Sleep -Milliseconds 250
    try {
        $targets = Invoke-RestMethod "http://127.0.0.1:$DebugPort/json/list"
        if ($targets | Where-Object { $_.type -eq "page" }) {
            Write-Output $bootstrapURL
            exit 0
        }
    } catch {
        if ($attempt -eq 29) { throw }
    }
}

throw "Timed out waiting for Edge remote debugging port $DebugPort"
