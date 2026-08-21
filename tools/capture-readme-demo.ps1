param(
    [Parameter(Mandatory = $true)]
    [string]$BootstrapURL,

    [string]$OutputDirectory = "C:\Windows\Temp\floe-readme-demo"
)

$ErrorActionPreference = "Stop"

$null = New-Item -ItemType Directory -Force -Path $OutputDirectory
Get-ChildItem -Path $OutputDirectory -Filter "frame-*.png" -ErrorAction SilentlyContinue | Remove-Item -Force

$targets = Invoke-RestMethod http://127.0.0.1:9228/json/list
$target = $targets | Where-Object { $_.type -eq "page" } | Select-Object -First 1
if (-not $target) { throw "Floe page target not found" }

$socket = [System.Net.WebSockets.ClientWebSocket]::new()
$null = $socket.ConnectAsync([Uri]$target.webSocketDebuggerUrl, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
$script:nextMessageID = 0
$script:frameNumber = 0

function Invoke-CDP([string]$Method, [hashtable]$Params = @{}) {
    $script:nextMessageID++
    $messageID = $script:nextMessageID
    $payload = @{ id = $messageID; method = $Method; params = $Params } | ConvertTo-Json -Compress -Depth 20
    $bytes = [Text.Encoding]::UTF8.GetBytes($payload)
    $socket.SendAsync(
        [ArraySegment[byte]]::new($bytes),
        [Net.WebSockets.WebSocketMessageType]::Text,
        $true,
        [Threading.CancellationToken]::None
    ).GetAwaiter().GetResult()

    while ($true) {
        $memory = [IO.MemoryStream]::new()
        do {
            $buffer = [byte[]]::new(131072)
            $received = $socket.ReceiveAsync(
                [ArraySegment[byte]]::new($buffer),
                [Threading.CancellationToken]::None
            ).GetAwaiter().GetResult()
            $memory.Write($buffer, 0, $received.Count)
        } while (-not $received.EndOfMessage)

        $message = [Text.Encoding]::UTF8.GetString($memory.ToArray()) | ConvertFrom-Json
        if ($message.id -eq $messageID) { return $message }
    }
}

function Invoke-JavaScript([string]$Expression) {
    $result = Invoke-CDP "Runtime.evaluate" @{
        expression = $Expression
        returnByValue = $true
    }
    if ($result.result.exceptionDetails) {
        throw ($result.result.exceptionDetails | ConvertTo-Json -Depth 12)
    }
    return $result.result.result.value
}

function Save-Screenshot([string]$Path) {
    $screenshot = Invoke-CDP "Page.captureScreenshot" @{
        format = "png"
        captureBeyondViewport = $false
    }
    [IO.File]::WriteAllBytes($Path, [Convert]::FromBase64String($screenshot.result.data))
}

function Save-Frames([int]$Count) {
    for ($index = 0; $index -lt $Count; $index++) {
        $path = Join-Path $OutputDirectory ("frame-{0:D3}.png" -f $script:frameNumber)
        Save-Screenshot $path
        $script:frameNumber++
    }
}

$null = Invoke-CDP "Emulation.setDeviceMetricsOverride" @{
    width = 1440
    height = 900
    deviceScaleFactor = 1
    mobile = $false
}
$null = Invoke-CDP "Page.navigate" @{ url = $BootstrapURL }
Start-Sleep -Seconds 8

$seed = @'
(() => {
  const now = Date.now();
  window.demoRefreshTasks = refreshTasks;
  refreshTasks = () => {};
  loadPanel = async () => {};
  loadProviders = async () => {};

  state.panels.left.loadID++;
  state.panels.right.loadID++;
  state.providers = [
    {id:'local', name:'Windows Local', kind:'local', group:'Local', location:'C:/Users/demo', connected:true},
    {id:'build-sftp', name:'Build Server', kind:'sftp', group:'Servers', host:'build.example.com', port:22, user:'builder', auth_method:'key', connected:true},
    {id:'prod-sftp', name:'Production Server', kind:'sftp', group:'Servers', host:'prod.example.com', port:22, user:'deploy', auth_method:'key', connected:true},
    {id:'archive-sftp', name:'Model Archive', kind:'sftp', group:'Servers', host:'archive.example.com', port:22, user:'models', auth_method:'key', connected:true}
  ];

  state.panels.left.tabs = [{provider:'build-sftp', path:'/opt/build/releases'}];
  state.panels.left.active = 'build-sftp';
  state.panels.left.selection = new Set(['/opt/build/releases/model-v4.bin']);
  state.panels.left.entries = [
    {name:'artifacts',path:'/opt/build/releases/artifacts',size:0,modified:new Date(now-7200000).toISOString(),mode:'drwxr-xr-x',is_dir:true,is_link:false},
    {name:'model-v4.bin',path:'/opt/build/releases/model-v4.bin',size:12884901888,modified:new Date(now-420000).toISOString(),mode:'-rw-r--r--',is_dir:false,is_link:false},
    {name:'inference-service.zip',path:'/opt/build/releases/inference-service.zip',size:90177536,modified:new Date(now-660000).toISOString(),mode:'-rw-r--r--',is_dir:false,is_link:false},
    {name:'release-notes.md',path:'/opt/build/releases/release-notes.md',size:4836,modified:new Date(now-900000).toISOString(),mode:'-rw-r--r--',is_dir:false,is_link:false},
    {name:'SHA256SUMS.txt',path:'/opt/build/releases/SHA256SUMS.txt',size:152,modified:new Date(now-900000).toISOString(),mode:'-rw-r--r--',is_dir:false,is_link:false}
  ];

  state.panels.right.tabs = [{provider:'prod-sftp', path:'/srv/models/releases'}];
  state.panels.right.active = 'prod-sftp';
  state.panels.right.selection = new Set();
  state.panels.right.entries = [
    {name:'archive',path:'/srv/models/releases/archive',size:0,modified:new Date(now-86400000).toISOString(),mode:'drwxr-xr-x',is_dir:true,is_link:false},
    {name:'model-v3.bin',path:'/srv/models/releases/model-v3.bin',size:11542724608,modified:new Date(now-604800000).toISOString(),mode:'-rw-r--r--',is_dir:false,is_link:false},
    {name:'release-notes.md',path:'/srv/models/releases/release-notes.md',size:4208,modified:new Date(now-604800000).toISOString(),mode:'-rw-r--r--',is_dir:false,is_link:false}
  ];

  state.tasks = [];
  state.taskStatus.clear();
  state.transferMetrics.clear();
  state.taskFilter = 'queue';
  state.transferTemplates = [];
  state.groupState = {};

  for (const side of ['left', 'right']) {
    panelElements(side).path.value = currentPath(side);
    panelElements(side).count.textContent = state.panels[side].entries.length + ' items';
    renderTabs(side);
    renderPanel(side);
    updateSelectionLabel(side);
  }
  setActivePane('left');
  renderSessionTree();
  setSidebarTab('sessions');
  document.querySelector('#transferQueue').classList.remove('collapsed');
  document.querySelector('#toastStack').replaceChildren();
  renderTaskList();

  window.setDemoTask = (status, transferred, verified, elapsedSeconds) => {
    const createdAt = new Date(Date.now() - elapsedSeconds * 1000).toISOString();
    const updatedAt = new Date().toISOString();
    const task = {
      id:'server-transfer-demo',
      status,
      source_provider:'build-sftp',
      source_path:'/opt/build/releases/model-v4.bin',
      target_provider:'prod-sftp',
      target_path:'/srv/models/releases/model-v4.bin',
      size:12884901888,
      bytes_transferred:transferred,
      bytes_verified:verified,
      bytes_read:transferred,
      bytes_written:transferred,
      created_at:createdAt,
      updated_at:updatedAt
    };
    state.tasks = [task];
    state.taskStatus.set(task.id, status);
    state.transferMetrics.set(task.id, {
      bytes:Math.max(transferred, verified),
      readBytes:transferred,
      writeBytes:transferred,
      sampledAt:Date.now(),
      readSpeed:650 * 1024 * 1024,
      writeSpeed:610 * 1024 * 1024
    });
    document.querySelector('#queueCount').textContent = ['running','verifying','paused'].includes(status) ? '1' : '0';
    document.querySelector('#successCount').textContent = status === 'completed' ? '1' : '0';
    document.querySelector('#failedCount').textContent = '0';
    state.taskFilter = status === 'completed' ? 'success' : 'queue';
    renderTaskList();
  };
  return true;
})()
'@

$null = Invoke-JavaScript $seed
Start-Sleep -Milliseconds 500

Save-Frames 3

$null = Invoke-JavaScript @'
document.querySelector('#rightPane').classList.add('drop-target'); true
'@
Save-Frames 1

$null = Invoke-JavaScript @'
(() => {
  document.querySelector('#rightPane').classList.remove('drop-target');
  transferConflictOptions(
    {name:'model-v4.bin',path:'/opt/build/releases/model-v4.bin',size:12884901888,modified:new Date().toISOString()},
    {payload:{
      source:{name:'model-v4.bin',path:'/opt/build/releases/model-v4.bin',size:12884901888,modified:new Date().toISOString()},
      target:{name:'model-v4.bin',path:'/srv/models/releases/model-v4.bin',size:11542724608,modified:new Date(Date.now()-604800000).toISOString()},
      source_path:'/opt/build/releases/model-v4.bin',
      target_path:'/srv/models/releases/model-v4.bin'
    }}
  );
  return true;
})()
'@
Start-Sleep -Milliseconds 250
Save-Frames 2

$null = Invoke-JavaScript @'
closeTransferOptions({conflict_policy:'overwrite'}); setDemoTask('running', 1932735283, 0, 4); true
'@
Save-Frames 2

$null = Invoke-JavaScript @'
setDemoTask('running', 6442450944, 0, 10); true
'@
Save-Screenshot (Join-Path $OutputDirectory "floe-server-to-server.png")
Save-Frames 2

$null = Invoke-JavaScript @'
setDemoTask('verifying', 12884901888, 7516192768, 18); true
'@
Save-Frames 2

$null = Invoke-JavaScript @'
(() => {
  setDemoTask('completed', 12884901888, 12884901888, 22);
  state.panels.right.entries.splice(1, 0, {
    name:'model-v4.bin',path:'/srv/models/releases/model-v4.bin',size:12884901888,
    modified:new Date().toISOString(),mode:'-rw-r--r--',is_dir:false,is_link:false
  });
  panelElements('right').count.textContent = state.panels.right.entries.length + ' items';
  renderPanel('right');
  toast('\u4f20\u8f93\u5b8c\u6210\u5e76\u5df2\u6821\u9a8c');
  return true;
})()
'@
Save-Frames 4

$socket.Dispose()
Write-Host "Captured $script:frameNumber frames in $OutputDirectory"
