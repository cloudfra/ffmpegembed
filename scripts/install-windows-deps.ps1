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

# Installs the Windows packages the embedded ffmpeg.exe/ffprobe.exe need.
#
# The BtbN win64 builds bundle their codec libraries but still hard-import
# desktop/multimedia DLLs (avicap32.dll for vfwcap, d2d1.dll, dwrite.dll, ...)
# that Windows Server Core does not ship. Without them the process dies in the
# loader with exit status 0xc0000135 (STATUS_DLL_NOT_FOUND) before ffmpeg runs.
#
# Usage: scripts/install-windows-deps.ps1
#
# Each dependency is an Install-* function that installs its package only when
# it is not already there and returns $true when a reboot is required to
# finish. The script never reboots the machine itself; it prints a
# "REBOOT REQUIRED" notice and leaves that to the operator. It then reports
# which of the DLLs ffmpeg imports are present, so successive runs show whether
# the machine is getting closer to being able to run ffmpeg.
#
# Safe to re-run: installed packages are skipped. Needs an elevated shell.

[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

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

$InstallationType = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion').InstallationType

# Server Core App Compatibility Feature on Demand: a subset of the Desktop
# Experience binaries for Server Core. Returns $true if a reboot is required.
function Install-ServerCoreAppCompatibility {
    $name = 'ServerCore.AppCompatibility~~~~0.0.1.0'
    if ($InstallationType -ne 'Server Core') {
        Write-Host "  [deps] $name skipped: not Server Core"
        return $false
    }
    $state = (Get-WindowsCapability -Online -Name $name).State
    Write-Host "  [deps] $name state: $state"
    if ($state -eq 'Installed') {
        return $false
    }
    if ($state -eq 'InstallPending') {
        return $true
    }
    Write-Host "  [deps] installing $name"
    $result = Add-WindowsCapability -Online -Name $name
    return [bool]$result.RestartNeeded
}

function Write-DllReport {
    $missing = @()
    foreach ($dll in $RequiredDlls) {
        $present = Test-Path (Join-Path $env:WINDIR "System32\$dll")
        if (-not $present) {
            $missing += $dll
        }
        Write-Host ("  [deps] {0,-14} {1}" -f $dll, $(if ($present) { 'present' } else { 'MISSING' }))
    }
    if ($missing.Count -gt 0) {
        Write-Host "::warning::$env:COMPUTERNAME is missing ffmpeg system DLLs: $($missing -join ', ')"
    }
}

Write-Host "  [deps] machine: $env:COMPUTERNAME"
Write-Host "  [deps] Windows installation type: $InstallationType"

$rebootRequired = $false
if (Install-ServerCoreAppCompatibility) { $rebootRequired = $true }

Write-DllReport

if ($rebootRequired) {
    Write-Host "::warning::REBOOT REQUIRED on $env:COMPUTERNAME to finish installing ffmpeg system dependencies"
    Write-Host "  [deps] REBOOT REQUIRED on $env:COMPUTERNAME (not rebooting automatically)"
} else {
    Write-Host '  [deps] no reboot required'
}
