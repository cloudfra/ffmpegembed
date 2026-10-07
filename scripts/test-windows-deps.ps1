# Copyright 2026 Cloudfra
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Tests that this Windows machine has what the embedded ffmpeg.exe/ffprobe.exe
# need to start. It changes nothing; scripts/install-windows-deps.ps1 is the
# script that installs.
#
# Two things are checked, and each failure is reported on its own line:
#   * every system DLL the ffmpeg builds import that a minimal Server Core
#     install lacks is present in System32;
#   * on Server Core, the App Compatibility Feature on Demand is installed.
#
# Usage: scripts/test-windows-deps.ps1
# Exits 0 when every check passes and 1 otherwise.
#
# Runs without elevation, so it works under the GitHub Actions runner service.

[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$Capability = 'ServerCore.AppCompatibility~~~~0.0.1.0'

# System DLLs imported by ffmpeg.exe that are not part of a minimal Server Core
# install. msvfw32.dll is not imported directly; avicap32.dll depends on it.
$RequiredDlls = @(
    'avicap32.dll',
    'msvfw32.dll',
    'd2d1.dll',
    'dwrite.dll',
    'avrt.dll',
    'msimg32.dll',
    'usp10.dll',
    'winmm.dll'
)

$script:Failures = @()

function Write-Result {
    param([string]$Status, [string]$Name, [string]$Detail = '')
    Write-Host ("  [deps] {0,-7} {1} {2}" -f $Status, $Name, $Detail)
}

function Add-Failure {
    param([string]$Message)
    $script:Failures += $Message
    Write-Host "::error::$env:COMPUTERNAME: $Message"
}

# Every required DLL must exist in System32.
function Test-RequiredDlls {
    foreach ($dll in $RequiredDlls) {
        if (Test-Path (Join-Path $env:WINDIR "System32\$dll")) {
            Write-Result 'ok' $dll
        } else {
            Write-Result 'MISSING' $dll
            Add-Failure "missing system DLL $dll (needed by ffmpeg.exe)"
        }
    }
}

# Returns 'Installed' or 'NotInstalled' for the App Compatibility
# Feature on Demand. Get-WindowsCapability gives the definitive answer but
# needs elevation; without it the servicing package list in the registry,
# which is readable by any user, is consulted instead, and failing that the
# presence of programs the feature adds (explorer.exe, mmc.exe).
function Get-AppCompatibilityState {
    try {
        $state = (Get-WindowsCapability -Online -Name $Capability).State
        Write-Result 'info' 'Get-WindowsCapability' "state: $state"
        if ($state -eq 'Installed') { return 'Installed' }
        return 'NotInstalled'
    } catch {
        Write-Result 'info' 'Get-WindowsCapability' "unavailable: $($_.Exception.Message)"
    }

    $servicing = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing'
    if (Test-Path "$servicing\RebootPending") {
        Write-Result 'info' 'servicing' 'REBOOT PENDING: a package install is waiting for a restart'
    }
    $found = @()
    try {
        $found = @(Get-ChildItem "$servicing\Packages" | Where-Object { $_.PSChildName -like '*AppCompat*' })
    } catch {
        Write-Result 'info' 'servicing registry' "unavailable: $($_.Exception.Message)"
    }
    $installed = $false
    foreach ($package in $found) {
        # CurrentState 0x70 (112) is "Installed" in the servicing stack; lower
        # values are staged or pending states that still need a restart.
        $current = (Get-ItemProperty $package.PSPath).CurrentState
        Write-Result 'info' $package.PSChildName "CurrentState: $current"
        if ($current -eq 112) { $installed = $true }
    }
    if ($installed) { return 'Installed' }
    if ($found.Count -gt 0) { return 'NotInstalled' }

    # No package entry was recognized. Fall back to programs the feature adds
    # to Server Core, which a minimal install does not have.
    $markers = @('explorer.exe', 'mmc.exe') | ForEach-Object {
        $path = if ($_ -eq 'explorer.exe') { Join-Path $env:WINDIR $_ } else { Join-Path $env:WINDIR "System32\$_" }
        $present = Test-Path $path
        Write-Result 'info' $_ $(if ($present) { 'present' } else { 'absent' })
        $present
    }
    if ($markers -notcontains $false) { return 'Installed' }
    return 'NotInstalled'
}

# On Server Core the App Compatibility Feature on Demand must be installed.
function Test-AppCompatibility {
    $installationType = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion').InstallationType
    Write-Result 'info' 'installation type' $installationType
    if ($installationType -ne 'Server Core') {
        Write-Result 'skip' $Capability 'not Server Core'
        return
    }
    switch (Get-AppCompatibilityState) {
        'Installed' { Write-Result 'ok' $Capability }
        'NotInstalled' {
            Write-Result 'MISSING' $Capability
            Add-Failure "$Capability is not installed (run scripts/install-windows-deps.ps1 elevated, then reboot)"
        }
    }
}

Write-Host "  [deps] machine: $env:COMPUTERNAME"
Test-AppCompatibility
Test-RequiredDlls

if ($script:Failures.Count -gt 0) {
    Write-Host "  [deps] FAIL: $($script:Failures.Count) problem(s) on $env:COMPUTERNAME"
    exit 1
}
Write-Host '  [deps] PASS'
