# Checks a running Drydock window the way assistive technology sees it (Windows UI Automation), with no debugging switch in the
# app: the release workflow runs it against the shipped drydock.exe exactly as a person would get it. It waits for the process's
# top-level window, then for every name in -Names somewhere in its accessibility tree (WebView2 exposes the page's landmarks,
# headings and labels), and saves a screenshot of the screen.
#
#   window-check.ps1 -ProcessId 1234 -Names 'Main' -Screenshot out\shipped.png [-TimeoutSec 90]
#
# Prints how long the window and each name took (milliseconds from the start of this script) and exits 1 when one is not there.
param(
  [Parameter(Mandatory)] [int] $ProcessId,
  [Parameter(Mandatory)] [string[]] $Names,
  [string] $Screenshot = '',
  [int] $TimeoutSec = 90
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes, System.Windows.Forms, System.Drawing
$ae = [System.Windows.Automation.AutomationElement]
$t = [Diagnostics.Stopwatch]::StartNew()
$deadline = (Get-Date).AddSeconds($TimeoutSec)

function Save-Screen([string] $path) {
  if (-not $path) { return }
  New-Item -ItemType Directory -Force (Split-Path $path) | Out-Null
  $b = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
  $bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($b.Location, [System.Drawing.Point]::Empty, $b.Size)
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
  $g.Dispose(); $bmp.Dispose()
}

$byPid = New-Object System.Windows.Automation.PropertyCondition($ae::ProcessIdProperty, $ProcessId)
$win = $null
while (-not $win) {
  if ((Get-Process -Id $ProcessId -ErrorAction SilentlyContinue) -eq $null) { throw "process $ProcessId exited before its window opened" }
  $win = $ae::RootElement.FindFirst([System.Windows.Automation.TreeScope]::Children, $byPid)
  if (-not $win) {
    if ((Get-Date) -gt $deadline) { Save-Screen $Screenshot; throw "process $ProcessId has no window after $TimeoutSec s" }
    Start-Sleep -Milliseconds 250
  }
}
"window '$($win.Current.Name)' after $($t.ElapsedMilliseconds) ms"
try { $win.SetFocus() } catch { }

foreach ($n in $Names) {
  $cond = New-Object System.Windows.Automation.PropertyCondition($ae::NameProperty, $n)
  $el = $null
  while (-not $el) {
    $el = $win.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $cond)
    if (-not $el) {
      if ((Get-Date) -gt $deadline) {
        Save-Screen $Screenshot
        throw "the window shows no element named '$n' after $TimeoutSec s"
      }
      Start-Sleep -Milliseconds 500
    }
  }
  "'$n' ($($el.Current.LocalizedControlType)) after $($t.ElapsedMilliseconds) ms"
}
Save-Screen $Screenshot
