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

# <note type="context">
# This is now a report-only Windows dependency PROBE, not an installer.
# </note>
#
# WHY THIS CHANGED
# ----------------
# The previous version called `Add-WindowsCapability -Online` to install the
# `ServerCore.AppCompatibility` Feature-on-Demand. On our self-hosted Windows
# Server Core runners that HANGS for ~11 minutes and then fails with
# "##[error]The operation was canceled." because the online capability source
# is unreachable/slow in that CI window. That aborts the job BEFORE `make test`
# ever runs, so we never even see the real ffmpeg failure.
#
# So this script no longer installs anything. It PROBES and REPORTS, then exits
# 0, so the pipeline proceeds to Lint/Test and surfaces the true failure
# (typically the loader abort with exit 0xC0000135, STATUS_DLL_NOT_FOUND):
#
#   * machine identity + Windows installation type + build (Desktop vs Core)
#   * whether the runner is elevated
#   * Constrained Language Mode + execution policy (why a PS script might be blocked)
#   * the `ServerCore.AppCompatibility` FOD STATE (read-only, no install)
#   * the REAL import-DLL list for the actual embedded ffmpeg.exe / ffprobe.exe,
#     parsed straight from the PE import directory (no objdump, no external tools)
#   * for each imported DLL: present/missing in System32 / the exe dir / PATH
#   * a LIVE `ffmpeg -version` launch and a decode of the exact NTSTATUS / exit code
#   * Defender, WDAC/DeviceGuard, AppLocker policy, PATH, TEMP writability, 8.3 names
#
# It is safe to run non-elevated (it only reads) and safe to re-run. It ALWAYS
# exits 0 so it never gates the build; the actionable fix (an elevated, offline
# FOD install or DLL placement) is deliberately deferred to a separate script.

[CmdletBinding()]
param()

# This is a diagnostic: never let one failing probe abort the report.
$ErrorActionPreference = 'Continue'

# --- Output helpers -----------------------------------------------------------
function Write-Probe([string]$msg) { Write-Host "  [probe] $msg" }
function Write-ProbeWarn([string]$msg) {
    Write-Host "  [probe] $msg"
    Write-Host "::warning::$env:COMPUTERNAME  $msg"
}

# --- Byte-reader helpers (pure PowerShell PE parsing; PowerShell 5.1-safe) ----
function Read-Bytes([System.IO.Stream]$s, [long]$offset, [int]$count) {
    if ($count -le 0) { return ,(@()) }
    $old = $s.Position
    $s.Position = $offset
    $buf = New-Object -TypeName 'System.Byte[]' -ArgumentList $count
    $n = 0
    while ($n -lt $count) {
        $r = $s.Read($buf, $n, $count - $n)
        if ($r -le 0) { break }
        $n += $r
    }
    $s.Position = $old
    if ($n -ne $count) {
        $c = New-Object -TypeName 'System.Byte[]' -ArgumentList $n
        [Array]::Copy($buf, 0, $c, 0, $n)
        return ,($c)
    }
    return ,($buf)
}
function Read-U16([byte[]]$b, [int]$o) { [BitConverter]::ToUInt16($b, $o) }
function Read-U32([byte[]]$b, [int]$o) { [BitConverter]::ToUInt32($b, $o) }

# Parse the PE import directory of $path and emit the imported DLL names
# (lowercased, de-duplicated, sorted) as pipeline items. Emits NOTHING on any
# problem (missing file, bad magic, no import dir). The caller collects the
# items with @(...) so an empty result is a true 0-element array. The stream is
# opened and closed inside this function.
function Get-PeImportedDlls([string]$path) {
    $out = [System.Collections.Generic.List[string]]::new()
    if (-not (Test-Path -LiteralPath $path)) { return }   # no file -> nothing
    $s = [System.IO.File]::OpenRead($path)
    try {
        $b = Read-Bytes $s 0 2
        if ($b.Count -lt 2 -or $b[0] -ne 0x4D -or $b[1] -ne 0x5A) { return }  # not MZ
        $peOff = [long](Read-U32 (Read-Bytes $s 0x3C 4) 0)
        $sig = Read-Bytes $s $peOff 4
        if ($sig.Count -lt 2 -or $sig[0] -ne 0x50 -or $sig[1] -ne 0x45) { return }  # not PE
        $coff = Read-Bytes $s ($peOff + 4) 20
        $numSections = [long](Read-U16 $coff 6)
        $optSize = [long](Read-U16 $coff 16)
        $optStart = [long]($peOff + 4 + 20)
        $magic = Read-U16 (Read-Bytes $s $optStart 2) 0
        # Data-directory array base: +0x70 for PE32+ (0x20b), +0x60 for PE32.
        $ddBase = [long]($optStart + $(if ($magic -eq 0x20b) { 112 } else { 96 }))
        # Import Directory is IMAGE_DATA_DIRECTORY index 1.
        $impRVA = [long](Read-U32 (Read-Bytes $s ($ddBase + 8) 8) 0)
        if ($impRVA -eq 0) { return }

        # Section table (40-byte headers) for RVA -> file-offset conversion.
        $secBase = [long]($optStart + $optSize)
        $secs = @()
        for ($i = 0; $i -lt $numSections; $i++) {
            $sh = Read-Bytes $s ([long]($secBase + ($i * 40))) 40
            $vsize = [long](Read-U32 $sh 8)
            $vaddr = [long](Read-U32 $sh 12)
            $rawSize = [long](Read-U32 $sh 16)
            $rawPtr = [long](Read-U32 $sh 20)
            $secs += ,@($vaddr, [math]::Max($vsize, $rawSize), $rawPtr)
        }

        function Rva2Off([long]$rva) {
            foreach ($sec in $secs) {
                if ($rva -ge $sec[0] -and $rva -lt ($sec[0] + $sec[1])) {
                    return [long]($sec[2] + ($rva - $sec[0]))
                }
            }
            return $rva
        }

        $off = Rva2Off $impRVA
        $desc = 0
        while ($desc -lt 100000) {            # safety cap on descriptor count
            $d = Read-Bytes $s $off 20
            if ($d.Count -lt 20) { break }
            $thunk = Read-U32 $d 0
            $nameRVA = Read-U32 $d 12
            $fstThunk = Read-U32 $d 16
            if ($thunk -eq 0 -and $nameRVA -eq 0 -and $fstThunk -eq 0) { break }  # terminator
            if ($nameRVA -eq 0) { break }
            $rawName = Read-Bytes $s (Rva2Off $nameRVA) 256
            if ($rawName.Count -gt 0) {
                $nul = [Array]::IndexOf($rawName, [byte]0)
                if ($nul -lt 0) { $nul = $rawName.Count }
                $name = [System.Text.Encoding]::ASCII.GetString($rawName, 0, $nul)
                if ($name) { $out.Add($name.ToLowerInvariant()) }
            }
            $off += 20
            $desc += 1
        }
    }
    catch {
        Write-ProbeWarn ("PE import parse error for $(Split-Path -Leaf $path): $($_.Exception.Message)")
    }
    finally {
        $s.Dispose()
    }
    # De-dup (case-insensitive) + sort, then emit as pipeline items.
    $seen = [System.Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $clean = [System.Collections.Generic.List[string]]::new()
    foreach ($x in $out) { if ($seen.Add($x)) { $clean.Add($x) } }
    $clean.Sort()
    return $clean.ToArray()
}

# Where does this DLL resolve from, given the exe location and system dirs?
function Test-DllLocation([string]$dll, [string]$exeDir, [string]$windir) {
    $candidates = @(
        (Join-Path $exeDir $dll),
        (Join-Path "$windir\System32" $dll),
        (Join-Path "$windir\SysWOW64" $dll),
        (Join-Path "$windir" $dll)
    )
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c) { return $c }
    }
    # PATH (last-resort search; skip system dirs already above)
    $pathDirs = ($env:PATH -split ';' | Where-Object { $_ })
    foreach ($pd in $pathDirs) {
        $c = Join-Path $pd $dll
        if (Test-Path -LiteralPath $c) { return $c }
    }
    return $null
}

# Decode an ffmpeg exit code / error message into a human-readable cause.
function Get-ExitCodeText([int]$code, [string]$msg) {
    $u = [uint32][long]$code
    $key = ('0x{0:X8}' -f $u)
    $map = [ordered]@{
        '0x00000000' = 'OK: process ran and exited 0 (ffmpeg started successfully).'
        '0xC0000135' = 'STATUS_DLL_NOT_FOUND: a required dependent DLL is missing on this machine.'
        '0xC000007B' = 'STATUS_INVALID_IMAGE_FORMAT: bitness mismatch (32-bit DLL in a 64-bit process) or corrupt image.'
        '0xC0000139' = 'STATUS_ENTRYPOINT_NOT_FOUND: the DLL is present but lacks an expected entry point (wrong version).'
        '0xC0000142' = 'STATUS_DLL_INIT_FAILED: a DLL loaded but its DllInitialize failed (incompatible version).'
        '0xC000005B' = 'STATUS_NO_DLL_SEARCH_FAILED: the loader could not satisfy a DLL dependency (modern loader path).'
        '0x8007007E' = 'ERROR_MOD_NOT_FOUND (126): CreateProcess failed, a required module is not found.'
        '0x8007003E' = 'ERROR_FILE_NOT_FOUND (62): the executable itself was not found at the given path.'
        '0x800700C1' = 'ERROR_BAD_EXE_FORMAT (193): the image is not a runnable PE (bitness/size mismatch).'
    }
    if ($map.Contains($key)) { return $map[$key] }
    if ($msg -and $msg -match 'could not be found') {
        return "CreateProcess module-not-found (matches STATUS_DLL_NOT_FOUND / a missing dependent DLL): $msg"
    }
    if ($msg) { return "unrecognized exit code ($key): $msg" }
    return "unrecognized exit code: $key ($code)"
}

# --- Report header -----------------------------------------------------------
Write-Probe "================= Windows ffmpeg dependency probe (report-only) ================="
$os = $env:OS
$computerName = $env:COMPUTERNAME
Write-Probe "machine: $computerName   ($os)"

$installationType = 'unknown'
try {
    $installationType = (Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction Stop).InstallationType
} catch { }
Write-Probe "installation type: $installationType"
if ($installationType -eq 'Server Core') {
    Write-ProbeWarn "Server Core confirmed: desktop/multimedia DLLs are NOT part of the base image. They are provided by the ServerCore.AppCompatibility FOD (see below)."
}

# Build + arch.
try {
    $cim = Get-CimInstance -ClassName 'Win32_OperatingSystem' -ErrorAction Stop
    Write-Probe "OS: $($cim.Caption)   build=$($cim.Version)   arch=$($cim.OSArchitecture)"
} catch { Write-Probe 'OS build/arch: (unavailable)' }

# Elevation.
$isAdmin = $false
try {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $isAdmin = ([Security.Principal.WindowsPrincipal]$identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    Write-Probe "identity: $($identity.Name)   elevated: $isAdmin"
    if ($isAdmin -and $installationType -eq 'Server Core') {
        Write-ProbeWarn "Elevated on Server Core: the deferred admin fix (offline FOD or DLL placement) COULD be applied here, but this probe does not."
    }
} catch { Write-Probe 'identity/elevation: (unavailable)' }

# Language mode + execution policy (a blocked PS script would surface here).
$lm = $ExecutionContext.SessionState.LanguageMode
$ep = (Get-ExecutionPolicy -Scope Process)
Write-Probe "language mode: $lm   execution policy (Process): $ep"
if ($lm -ine 'FullLanguage') {
    Write-ProbeWarn "Not FullLanguage — AppLocker/WDAC may be restricting PowerShell; some of this probe's richer checks were intentionally avoided."
}

# --- FOD state (READ-ONLY; never installs) -----------------------------------
Write-Probe "----- Feature-on-Demand state (read-only; no Add-WindowsCapability here) -----"
$fodName = 'ServerCore.AppCompatibility~~~~0.0.1.0'
$capState = 'unavailable'
try {
    $cap = Get-WindowsCapability -Online -Name $fodName -ErrorAction Stop
    $capState = ($cap | Select-Object -First 1).State
    Write-Probe "  ${fodName} state: $capState"
} catch {
    Write-Probe "  ${fodName} state: (query failed: $($_.Exception.Message))"
}
# Note: only READ, never ADD. The install (deferred) must be OFFLINE to avoid the
# ~11-minute hang seen on these runners with -Online.
switch ($capState) {
    'Installed' { Write-Probe '  FOD is Installed (should be fine if the DLLs landed).' }
    'InstalledPending' { Write-ProbeWarn '  FOD is InstalledPending: install finished but a reboot is required for the DLLs to be active.' }
    'NotPresent' { if ($installationType -eq 'Server Core') { Write-ProbeWarn '  FOD is NOT present on this Server Core machine: the App-compat DLLs are unavailable until an OFFLINE install is applied.' } }
    'InstallPending' { Write-ProbeWarn '  FOD is InstallPending (an install is in flight); needs a reboot, not re-install.' }
    default { Write-Probe "  FOD state: $capState" }
}

# --- Locate the embedded ffmpeg/ffprobe (host) -------------------------------
Write-Probe "----- embedded binaries (host) -----"
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$embedRoot = Join-Path $repoRoot 'internal\embedded\bin'
function Find-HostExe([string]$name) {
    foreach ($d in @('windows_amd64', 'windows_arm64', 'windows_386', 'windows_arm')) {
        $p = Join-Path $embedRoot "$d\$name"
        if (Test-Path -LiteralPath $p) { return $p }
    }
    return $null
}
$ffmpeg = Find-HostExe 'ffmpeg.exe'
$ffprobe = Find-HostExe 'ffprobe.exe'
if (-not $ffmpeg) {
    Write-ProbeWarn "host ffmpeg.exe NOT found under internal/embedded/bin/win*/ (did `make ffembed-host` run? expected windows_amd64/ffmpeg.exe)."
} else {
    $sz = (Get-Item -LiteralPath $ffmpeg).Length
    Write-Probe "ffmpeg : $ffmpeg  ($sz bytes)"
}
if ($ffprobe) { Write-Probe "ffprobe: $ffprobe" }
else { Write-Probe 'ffprobe: (not found -- will skip its checks)' }

# --- PE import table of the REAL binaries ------------------------------------
$allImports = [System.Collections.Generic.List[string]]::new()
foreach ($exe in @($ffmpeg, $ffprobe)) {
    if (-not $exe) { continue }
    $dls = @(Get-PeImportedDlls $exe)
    Write-Probe "  $(Split-Path -Leaf $exe) imports $($dls.Count) DLL(s):"
    foreach ($dl in $dls) {
        if (-not $allImports.Contains($dl)) { $allImports.Add($dl) }
        Write-Probe "      - $dl"
    }
}
if (-not $ffmpeg) { Write-Probe '  (no host ffmpeg to parse imports from)' }

# --- Per-DLL presence in the real search-order locations ---------------------
if ($allImports.Count -gt 0) {
    Write-Probe "----- imported-DLL presence (System32 / exe dir / SysWOW64 / PATH) -----"
    $missing = @()
    foreach ($dl in ($allImports | Sort-Object)) {
        $loc = $null
        if ($ffmpeg) { $loc = Test-DllLocation $dl (Split-Path -Parent $ffmpeg) $env:WINDIR }
        if ($loc) {
            Write-Probe ("  {0,-20} present  -> {1}" -f $dl, $loc)
        }
        else {
            Write-ProbeWarn ("  {0,-20} MISSING (not in System32, exe dir, SysWOW64, or PATH)" -f $dl)
            $missing += $dl
        }
    }
    if ($missing.Count -gt 0) {
        Write-ProbeWarn "MISSING ffmpeg-imported DLLs: $($missing -join ', ')"
        Write-Probe "  => every `make test` that launches ffmpeg will abort in the Windows loader with 0xC0000135 (STATUS_DLL_NOT_FOUND)."
        Write-Probe "  => an OFFLINE install of ServerCore.AppCompatibility (or these DLLs placed next to the exe / in System32) is required — see the deferred admin fix."
    }
    else {
        Write-Probe '  all imported DLLs resolve on this machine.'
    }
}

# --- LIVE `ffmpeg -version` launch + decoded NTSTATUS ------------------------
if ($ffmpeg) {
    Write-Probe "----- live launch: `\"$(Split-Path $ffmpeg)\" -version -----"
    $errMsg = $null
    $code = [int]0
    $outText = @()
    $prevEAP = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Stop'
        $out = (& $ffmpeg -version) 2>&1
        $code = [int]$LASTEXITCODE
        $outText = @($out | Select-Object -First 3)
    }
    catch {
        $code = [int]$LASTEXITCODE
        $errMsg = $_.Exception.Message
        $outText = @($_.ToString())
    }
    finally { $ErrorActionPreference = $prevEAP }

    $u = [uint32][long]$code
    Write-Probe ("  exit code: {0}  (unsigned 0x{1:X8}; signed {2})" -f $code, $u, $LASTEXITCODE)
    Write-Probe "  cause    : $(Get-ExitCodeText $code $errMsg)"
    foreach ($l in $outText) { Write-Probe "  | $l" }
    if ($u -ne 0 -and $u -ne 0xC0000135) { Write-ProbeWarn 'non-zero exit; the cause line above says WHY.' }
    elseif ($u -eq 0xC0000135) { Write-ProbeWarn 'confirmed: ffmpeg aborts in the Windows loader (a required DLL is missing).' }
    elseif ($u -eq 0) { Write-Probe 'ffmpeg -version succeeded: the loader is happy on this machine.' }
    else { Write-Probe 'ffmpeg did not run cleanly; see cause.' }
}

# --- Security / environment that can independently block a launch ------------
Write-Probe "----- environment & security -----"
# PATH sanity.
$paths = ($env:PATH -split ';' | Where-Object { $_ })
Write-Probe "  PATH entries: $($paths.Count)   (System32 in PATH: $([bool]($paths | Where-Object { $_ -like '*\System32' })))"
# TEMP / TMP writability (ffmpeg + the Go test harness write to temp).
$tmp = $env:TEMP; if (-not $tmp) { $tmp = $env:TMP }
$tmpOk = $false; $tmpNote = '(not set)'
if ($tmp -and (Test-Path -LiteralPath $tmp)) {
    $probeFile = Join-Path $tmp ("ffmpegprobe_" + [Guid]::NewGuid() + ".tmp")
    try { Set-Content -LiteralPath $probeFile -Value 'probe' -ErrorAction Stop; $tmpOk = $true; Remove-Item -LiteralPath $probeFile -Force -ErrorAction SilentlyContinue }
    catch { $tmpOk = $false; $tmpNote = $_.Exception.Message; Remove-Item -LiteralPath $probeFile -Force -ErrorAction SilentlyContinue }
    Write-Probe "  TEMP: $tmp  writable: $tmpOk"
}
else { Write-ProbeWarn "  TEMP/TMP not resolvable ($tmpNote)" }
# 8.3 short-name creation (matters for long repo paths on Windows).
$e83 = try { (& fsutil 8dot3name query 'C:\') 2>$null } catch { @() }
Write-Probe "  8.3 names   : $($e83 -join ' | ')"
# Defender.
try {
    $mp = Get-MpComputerStatus -ErrorAction Stop
    Write-Probe "  Defender    : AMEnabledStatus=$($mp.AMServiceEnabled) AntivirusEnabled=$($mp.AntivirusEnabled) RealTime=$($mp.RealTimeProtectionEnabled)"
} catch { Write-Probe '  Defender    : (n/a or query failed)' }
# WDAC / DeviceGuard.
try {
    $dg = Get-CimInstance -ClassName 'Win32_DeviceGuard' -ErrorAction Stop
    Write-Probe "  WDAC        : VBS=$($dg.VirtualizationBasedSecurityStatus) SecureBoot=$($dg.SecureBootEnabledInfo) PolicyTip=$($dg.SecurityFeatureTip)"
} catch { Write-Probe '  WDAC        : (n/a)' }
# AppLocker local policy (a blocking policy would surface as non-empty).
try {
    $ap = Get-AppLockerPolicy -Local -ErrorAction Stop
    if ($ap -eq $null) { Write-Probe '  AppLocker   : no local policy loaded' }
    else {
        Write-Probe "  AppLocker   : local policy present (Rules: $((($ap | Select-Object -ExpandProperty Rules) -join ',')))"
    }
} catch { Write-Probe '  AppLocker   : (not available)' }

# --- Verdict -----------------------------------------------------------------
Write-Probe "================= probe verdict ================="
if (-not $ffmpeg) {
    Write-Probe 'Host ffmpeg not present on disk: the pipeline issue is likely `make ffembed-host` (download) or the download path, not the OS.'
}
elseif ($missing.Count -gt 0) {
    Write-Probe "ffmpeg CANNOT run here: $($missing.Count) imported DLL(s) missing. Fix = offline ServerCore.AppCompatibility install or DLL placement (elevated). This probe is intentionally not applying it."
}
else {
    Write-Probe "ffmpeg CAN run here: all imported DLLs resolve."
}
Write-Probe "================= end of probe ================="

# Report-only: never gate the build. Exit 0 so CI reaches `make test` and shows
# the real failure (or passes).
exit 0
