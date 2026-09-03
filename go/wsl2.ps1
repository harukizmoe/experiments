#requires -Version 5.1

<#
.SYNOPSIS
    启动、停止并查看常驻的 WSL2 发行版。

.EXAMPLE
    .\wsl2.ps1 start
    .\wsl2.ps1 status
    .\wsl2.ps1 stop
    .\wsl2.ps1 stop -All
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'status')]
    [string]$Operation = 'start',

    [string]$Distro = 'archlinux',

    [switch]$All
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$WslExe = Join-Path $env:WINDIR 'System32\wsl.exe'
$WindowsPowerShellExe = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
$TaskName = "WSL2-$Distro-KeepAlive"

function Invoke-Wsl {
    param(
        [Parameter(Mandatory)]
        [string[]]$Arguments
    )

    & $WslExe @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "wsl.exe $($Arguments -join ' ') 执行失败，退出码：$LASTEXITCODE"
    }
}

function Assert-Wsl2Distro {
    $rows = @(& $WslExe --list --verbose 2>$null)
    $exitCode = $LASTEXITCODE

    if ($exitCode -ne 0) {
        throw '无法读取 WSL 发行版列表。请确认 WSL 已安装。'
    }

    # PowerShell 5.1 读取 wsl.exe 输出时可能保留 UTF-16LE 的 NUL 字符。
    $rows = @($rows | ForEach-Object { $_.Replace(([char]0).ToString(), '').Trim() } | Where-Object { $_ })

    $escapedDistro = [regex]::Escape($Distro)
    $distroRow = $rows | Where-Object {
        $_ -match "^\s*\*?\s*$escapedDistro\s+\S+\s+2\s*$"
    }

    if ($null -eq $distroRow) {
        throw "未找到 WSL2 发行版 '$Distro'。请运行 wsl -l -v 检查名称和版本。"
    }
}

function Get-KeepAliveTask {
    Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
}

function Register-KeepAliveTask {
    $user = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
    $safeDistro = $Distro.Replace("'", "''")
    $safeWslExe = $WslExe.Replace("'", "''")
    $command = "& '$safeWslExe' --distribution '$safeDistro' --exec /usr/bin/setsid /usr/bin/sleep infinity"

    # setsid 让 Linux 常驻进程脱离 Windows Terminal 的伪终端。
    $action = New-ScheduledTaskAction `
        -Execute $WindowsPowerShellExe `
        -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -Command `"$command`""
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
    $principal = New-ScheduledTaskPrincipal `
        -UserId $user `
        -LogonType Interactive `
        -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -MultipleInstances IgnoreNew `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries

    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -Principal $principal `
        -Settings $settings `
        -Description "Keep WSL2 distribution '$Distro' running" `
        -Force | Out-Null
}

function Start-KeepAlive {
    Assert-Wsl2Distro

    $task = Get-KeepAliveTask
    if ($null -ne $task -and $task.State -eq 'Running') {
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    }

    # 每次 start 都刷新任务定义，确保旧版本的终端绑定动作被替换。
    Register-KeepAliveTask
    Enable-ScheduledTask -TaskName $TaskName | Out-Null

    $task = Get-KeepAliveTask
    if ($task.State -ne 'Running') {
        Start-ScheduledTask -TaskName $TaskName
    }

    Write-Host "已启动 '$Distro' 的 WSL2 常驻任务：$TaskName"
    Write-Host '关闭终端后，WSL2 会继续运行。使用 status 查看状态。'
}

function Stop-KeepAlive {
    $task = Get-KeepAliveTask
    if ($null -ne $task) {
        Disable-ScheduledTask -TaskName $TaskName | Out-Null
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    }

    if ($All) {
        Invoke-Wsl -Arguments @('--shutdown')
        Write-Host '已关闭全部 WSL2 发行版及 WSL2 虚拟机。'
    } else {
        Assert-Wsl2Distro
        Invoke-Wsl -Arguments @('--terminate', $Distro)
        Write-Host "已关闭 WSL2 发行版 '$Distro'。"
    }
}

function Show-Status {
    Invoke-Wsl -Arguments @('--list', '--verbose')

    $task = Get-KeepAliveTask
    if ($null -eq $task) {
        Write-Host '常驻任务：未创建'
    } else {
        Write-Host "常驻任务：$($task.State)（$TaskName）"
    }
}

try {
    switch ($Operation) {
        'start'  { Start-KeepAlive }
        'stop'   { Stop-KeepAlive }
        'status' { Show-Status }
    }
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
