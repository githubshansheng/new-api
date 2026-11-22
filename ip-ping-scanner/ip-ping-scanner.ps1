[CmdletBinding()]
param(
    [string]$PatternFile = "",
    [string[]]$Pattern = @(),
    [ValidateRange(1, 256)]
    [int]$Concurrency = 50,
    [ValidateRange(100, 10000)]
    [int]$TimeoutMs = 800,
    [ValidateRange(1, 100)]
    [int]$PingCount = 5,
    [ValidateRange(1, 1000000)]
    [int]$MaxTargets = 1000000,
    [string]$OutputDirectory = "",
    [switch]$NonInteractive,
    [switch]$PreviewOnly
)

# Windows PowerShell 5.1 is enough; no third-party modules are required.
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$builtInPatterns = @(
    "209.*.76.33"
    "209.*.76.187"
    "209.*.76.51"
    "209.*.76.143"
    "209.*.76.20"
    "209.*.76.37"
    "209.*.76.168"
    "209.*.76.191"
    "209.*.76.218"
    "209.*.76.62"
    "209.*.76.72"
    "209.*.76.214"
    "209.*.76.249"
    "209.*.76.222"
    "209.*.76.213"
    "209.*.76.203"
    "209.*.76.121"
    "209.*.76.149"
    "209.*.76.15"
    "209.*.76.175"
    "209.*.76.234"
    "209.*.76.232"
    "209.*.76.41"
    "209.*.76.21"
    "209.*.76.92"
    "209.*.76.151"
    "209.*.76.130"
    "209.*.76.102"
    "209.*.76.139"
    "209.*.76.43"
    "209.*.76.231"
    "209.*.76.28"
    "209.*.76.119"
    "209.*.76.87"
    "209.*.76.195"
    "209.*.76.205"
    "209.*.76.170"
    "209.*.76.24"
    "209.*.76.54"
    "209.*.76.227"
    "209.*.76.162"
    "209.*.76.240"
    "209.*.76.31"
    "209.*.76.147"
    "209.*.76.153"
    "209.*.76.126"
    "209.*.76.3"
    "209.*.76.112"
    "209.*.76.25"
    "209.*.76.140"
    "209.*.76.63"
    "209.*.76.52"
    "209.*.76.206"
    "209.*.76.152"
    "209.*.76.73"
    "209.*.76.117"
    "209.*.76.167"
    "209.*.76.180"
)

function Resolve-ScannerPath {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    if ([System.IO.Path]::IsPathRooted($Path)) {
        return [System.IO.Path]::GetFullPath($Path)
    }

    return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot $Path))
}

function Get-IpPatterns {
    param(
        [string[]]$DirectPatterns,
        [string]$FilePath,
        [Parameter(Mandatory = $true)]
        [string[]]$EmbeddedPatterns
    )

    $sourceLines = @()
    if ($DirectPatterns.Count -gt 0) {
        $sourceLines = $DirectPatterns
    }
    elseif (-not [string]::IsNullOrWhiteSpace($FilePath)) {
        if (-not (Test-Path -LiteralPath $FilePath -PathType Leaf)) {
            throw "找不到 IP 规则文件：$FilePath"
        }
        $sourceLines = @(Get-Content -LiteralPath $FilePath)
    }
    else {
        $sourceLines = $EmbeddedPatterns
    }

    $patterns = [System.Collections.Generic.List[string]]::new()
    foreach ($sourceLine in $sourceLines) {
        $line = ([string]$sourceLine -split '#', 2)[0].Trim()
        if ($line.Length -eq 0) {
            continue
        }

        foreach ($token in [System.Text.RegularExpressions.Regex]::Split($line, '[,;\s]+')) {
            $candidate = $token.Trim()
            if ($candidate.Length -gt 0) {
                $patterns.Add($candidate)
            }
        }
    }

    if ($patterns.Count -eq 0) {
        throw "没有找到任何 IP 规则。"
    }

    return $patterns.ToArray()
}

function Expand-IpPatterns {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Patterns,
        [Parameter(Mandatory = $true)]
        [int]$TargetLimit
    )

    $parsedPatterns = [System.Collections.Generic.List[object]]::new()
    $estimatedTargetCount = [long]0

    foreach ($ipPattern in $Patterns) {
        $parts = @($ipPattern.Split('.'))
        if ($parts.Count -ne 4) {
            throw "IPv4 规则 '$ipPattern' 无效：必须正好包含四个八位组。"
        }

        $octets = [System.Collections.Generic.List[object]]::new()
        $patternTargetCount = [long]1
        foreach ($part in $parts) {
            if ($part -eq '*' -or $part -ieq 'x') {
                if ($patternTargetCount -gt [long]($TargetLimit / 256)) {
                    throw "规则 '$ipPattern' 展开后超过目标数量上限（$TargetLimit）。"
                }
                $patternTargetCount *= 256
                $octets.Add([pscustomobject]@{ Wildcard = $true; Value = 0 })
                continue
            }

            $value = 0
            if (-not [int]::TryParse($part, [ref]$value) -or $value -lt 0 -or $value -gt 255) {
                throw "规则 '$ipPattern' 中的八位组 '$part' 无效，请使用 0–255、* 或 x。"
            }
            $octets.Add([pscustomobject]@{ Wildcard = $false; Value = $value })
        }

        if ($estimatedTargetCount + $patternTargetCount -gt $TargetLimit) {
            throw "全部规则展开后超过目标数量上限（$TargetLimit）。"
        }
        $estimatedTargetCount += $patternTargetCount
        $parsedPatterns.Add($octets.ToArray())
    }

    $seen = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    $targets = [System.Collections.Generic.List[string]]::new()
    foreach ($octets in $parsedPatterns) {
        $partials = [System.Collections.Generic.List[string]]::new()
        $partials.Add("")

        foreach ($octet in $octets) {
            $values = if ($octet.Wildcard) { 0..255 } else { @([int]$octet.Value) }
            $nextPartials = [System.Collections.Generic.List[string]]::new()
            foreach ($partial in $partials) {
                foreach ($value in $values) {
                    if ($partial.Length -eq 0) {
                        $nextPartials.Add([string]$value)
                    }
                    else {
                        $nextPartials.Add("$partial.$value")
                    }
                }
            }
            $partials = $nextPartials
        }

        foreach ($address in $partials) {
            if ($seen.Add($address)) {
                $targets.Add($address)
            }
        }
    }

    return $targets.ToArray()
}

function Read-IntegerSetting {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Label,
        [Parameter(Mandatory = $true)]
        [int]$CurrentValue,
        [Parameter(Mandatory = $true)]
        [int]$Minimum,
        [Parameter(Mandatory = $true)]
        [int]$Maximum
    )

    while ($true) {
        $inputValue = Read-Host "$Label [$CurrentValue]"
        if ([string]::IsNullOrWhiteSpace($inputValue)) {
            return $CurrentValue
        }

        $parsedValue = 0
        if ([int]::TryParse($inputValue, [ref]$parsedValue) -and
            $parsedValue -ge $Minimum -and $parsedValue -le $Maximum) {
            return $parsedValue
        }

        Write-Host "请输入 $Minimum 到 $Maximum 之间的整数。" -ForegroundColor Yellow
    }
}

function Show-TargetPreview {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Patterns,
        [Parameter(Mandatory = $true)]
        [string[]]$Targets
    )

    Write-Host ""
    Write-Host "IP 规则数：$($Patterns.Count)"
    Write-Host "展开后的唯一目标数：$($Targets.Count)"
    Write-Host "通配规则：* 或 x 自动填充为 0–255"

    $previewCount = [Math]::Min(8, $Targets.Count)
    if ($previewCount -gt 0) {
        Write-Host "前 $previewCount 个目标："
        for ($index = 0; $index -lt $previewCount; $index++) {
            Write-Host "  $($Targets[$index])"
        }
    }

    if ($Targets.Count -gt $previewCount) {
        Write-Host "后 $previewCount 个目标："
        for ($index = $Targets.Count - $previewCount; $index -lt $Targets.Count; $index++) {
            Write-Host "  $($Targets[$index])"
        }
    }
    Write-Host ""
}

function Start-PingAttempt {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Entry,
        [Parameter(Mandatory = $true)]
        [int]$PingTimeoutMs
    )

    try {
        $Entry.Task = $Entry.Pinger.SendPingAsync($Entry.Address, $PingTimeoutMs)
        return $true
    }
    catch {
        $Entry.AttemptsCompleted++
        return $false
    }
}

function Invoke-PingSweep {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Targets,
        [Parameter(Mandatory = $true)]
        [int]$MaxConcurrency,
        [Parameter(Mandatory = $true)]
        [int]$PingTimeoutMs,
        [Parameter(Mandatory = $true)]
        [int]$PacketsPerTarget,
        [Parameter(Mandatory = $true)]
        [string]$ResultRoot,
        [Parameter(Mandatory = $true)]
        [int]$PatternCount
    )

    if ($Targets.Count -eq 0) {
        throw "没有可以扫描的目标。"
    }

    $runName = "ping_scan_$(Get-Date -Format 'yyyyMMdd_HHmmss_fff')"
    $runDirectory = Join-Path $ResultRoot $runName
    $null = New-Item -ItemType Directory -Path $runDirectory -Force

    $aliveTextPath = Join-Path $runDirectory "alive.txt"
    $aliveCsvPath = Join-Path $runDirectory "alive.csv"
    $summaryPath = Join-Path $runDirectory "summary.txt"
    $utf8NoBom = [System.Text.UTF8Encoding]::new($false)
    $aliveTextWriter = $null
    $aliveCsvWriter = $null
    $active = [System.Collections.Generic.List[object]]::new()
    $pending = [System.Collections.Generic.Queue[string]]::new()
    foreach ($target in $Targets) {
        $pending.Enqueue($target)
    }

    $startedAt = Get-Date
    $processed = 0
    $aliveCount = 0
    $totalProbes = [long]$Targets.Count * $PacketsPerTarget

    Write-Host ""
    Write-Host "开始扫描 $($Targets.Count) 个目标，每个目标 $PacketsPerTarget 个 Ping 包（共 $totalProbes 次探测），并发数 $MaxConcurrency……"
    Write-Host "结果实时保存到：$runDirectory"
    Write-Host "按 Ctrl+C 可停止扫描，已经写入的结果会保留。"
    Write-Host ""

    try {
        $aliveTextWriter = [System.IO.StreamWriter]::new($aliveTextPath, $false, $utf8NoBom)
        $aliveCsvWriter = [System.IO.StreamWriter]::new($aliveCsvPath, $false, $utf8NoBom)
        $aliveTextWriter.AutoFlush = $true
        $aliveCsvWriter.AutoFlush = $true
        $aliveCsvWriter.WriteLine("ip,packets_sent,packets_received,packet_loss_percent,min_ms,avg_ms,max_ms,reply_address,scanned_at")

        while ($pending.Count -gt 0 -or $active.Count -gt 0) {
            while ($pending.Count -gt 0 -and $active.Count -lt $MaxConcurrency) {
                $address = $pending.Dequeue()
                $pinger = [System.Net.NetworkInformation.Ping]::new()
                $entry = [pscustomobject]@{
                    Address           = $address
                    Pinger            = $pinger
                    Task              = $null
                    AttemptsCompleted = 0
                    RepliesReceived   = 0
                    LatencyTotal      = [long]0
                    MinimumLatency    = [long]::MaxValue
                    MaximumLatency    = [long]0
                    ReplyAddress      = ""
                }

                $attemptStarted = $false
                while (-not $attemptStarted -and $entry.AttemptsCompleted -lt $PacketsPerTarget) {
                    $attemptStarted = Start-PingAttempt -Entry $entry -PingTimeoutMs $PingTimeoutMs
                }

                if ($attemptStarted) {
                    $active.Add($entry)
                }
                else {
                    $pinger.Dispose()
                    $processed++
                }
            }

            if ($active.Count -eq 0) {
                continue
            }

            [System.Threading.Tasks.Task[]]$taskArray = @(
                foreach ($entry in $active) {
                    [System.Threading.Tasks.Task]$entry.Task
                }
            )
            $completedIndex = [System.Threading.Tasks.Task]::WaitAny($taskArray)
            $completed = $active[$completedIndex]

            try {
                $awaiter = $completed.Task.GetAwaiter()
                $reply = $awaiter.GetResult()
                if ($reply.Status -eq [System.Net.NetworkInformation.IPStatus]::Success) {
                    $completed.RepliesReceived++
                    $completed.LatencyTotal += $reply.RoundtripTime
                    if ($reply.RoundtripTime -lt $completed.MinimumLatency) {
                        $completed.MinimumLatency = $reply.RoundtripTime
                    }
                    if ($reply.RoundtripTime -gt $completed.MaximumLatency) {
                        $completed.MaximumLatency = $reply.RoundtripTime
                    }
                    $completed.ReplyAddress = $reply.Address.ToString()
                }
            }
            catch {
                # A failed probe still counts as one completed packet attempt.
            }
            finally {
                $completed.AttemptsCompleted++
            }

            $attemptStarted = $false
            while (-not $attemptStarted -and $completed.AttemptsCompleted -lt $PacketsPerTarget) {
                $attemptStarted = Start-PingAttempt -Entry $completed -PingTimeoutMs $PingTimeoutMs
            }
            if ($attemptStarted) {
                continue
            }

            if ($completed.RepliesReceived -gt 0) {
                $aliveCount++
                $scannedAt = Get-Date -Format "yyyy-MM-ddTHH:mm:ss.fffK"
                $packetLoss = (($PacketsPerTarget - $completed.RepliesReceived) * 100.0) / $PacketsPerTarget
                $averageLatency = $completed.LatencyTotal / [double]$completed.RepliesReceived
                $invariantCulture = [System.Globalization.CultureInfo]::InvariantCulture
                $packetLossText = $packetLoss.ToString("0.##", $invariantCulture)
                $averageLatencyText = $averageLatency.ToString("0.##", $invariantCulture)
                $aliveTextWriter.WriteLine($completed.Address)
                $aliveCsvWriter.WriteLine(
                    "$($completed.Address),$PacketsPerTarget,$($completed.RepliesReceived),$packetLossText,$($completed.MinimumLatency),$averageLatencyText,$($completed.MaximumLatency),$($completed.ReplyAddress),$scannedAt"
                )
                Write-Host (
                    "[在线] {0,-15} 响应 {1}/{2}，平均 {3} ms，丢包率 {4}%" -f `
                        $completed.Address,
                        $completed.RepliesReceived,
                        $PacketsPerTarget,
                        $averageLatencyText,
                        $packetLossText
                ) -ForegroundColor Green
            }

            $completed.Pinger.Dispose()
            $active.RemoveAt($completedIndex)
            $processed++

            $percentComplete = [int](($processed * 100.0) / $Targets.Count)
            Write-Progress `
                -Activity "正在 Ping 展开后的 IP 目标" `
                -Status "已完成 $processed / $($Targets.Count)，可达 $aliveCount" `
                -PercentComplete $percentComplete
        }
    }
    finally {
        foreach ($entry in $active) {
            try {
                $entry.Pinger.Dispose()
            }
            catch {
                # Best-effort cleanup during cancellation.
            }
        }
        if ($null -ne $aliveTextWriter) {
            $aliveTextWriter.Dispose()
        }
        if ($null -ne $aliveCsvWriter) {
            $aliveCsvWriter.Dispose()
        }
        Write-Progress -Activity "正在 Ping 展开后的 IP 目标" -Completed
    }

    $finishedAt = Get-Date
    $duration = $finishedAt - $startedAt
    $summaryLines = @(
        "开始时间：$($startedAt.ToString('o'))"
        "完成时间：$($finishedAt.ToString('o'))"
        "扫描耗时：$($duration.ToString())"
        "IP 规则数：$PatternCount"
        "展开后的唯一目标数：$($Targets.Count)"
        "可达目标数：$aliveCount"
        "无响应或出错数：$($Targets.Count - $aliveCount)"
        "每个目标的 Ping 包数：$PacketsPerTarget"
        "单包超时（毫秒）：$PingTimeoutMs"
        "并发数：$MaxConcurrency"
        "可达结果 TXT：$aliveTextPath"
        "可达结果 CSV：$aliveCsvPath"
    )
    [System.IO.File]::WriteAllLines($summaryPath, $summaryLines, $utf8NoBom)

    Write-Host ""
    Write-Host "扫描完成，可达目标：$aliveCount / $($Targets.Count)" -ForegroundColor Cyan
    Write-Host "TXT: $aliveTextPath"
    Write-Host "CSV: $aliveCsvPath"
    Write-Host "扫描摘要：$summaryPath"
    Write-Host "提示：没有 ICMP 响应并不一定表示主机离线。" -ForegroundColor Yellow
    Write-Host ""
}

try {
    if (-not [string]::IsNullOrWhiteSpace($PatternFile)) {
        $PatternFile = Resolve-ScannerPath -Path $PatternFile
    }

    if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
        $OutputDirectory = Join-Path $PSScriptRoot "results"
    }
    else {
        $OutputDirectory = Resolve-ScannerPath -Path $OutputDirectory
    }

    if ($PreviewOnly -or $NonInteractive) {
        $patterns = @(
            Get-IpPatterns `
                -DirectPatterns $Pattern `
                -FilePath $PatternFile `
                -EmbeddedPatterns $builtInPatterns
        )
        $targets = @(Expand-IpPatterns -Patterns $patterns -TargetLimit $MaxTargets)

        if ($PreviewOnly) {
            Show-TargetPreview -Patterns $patterns -Targets $targets
            exit 0
        }

        Invoke-PingSweep `
            -Targets $targets `
            -MaxConcurrency $Concurrency `
            -PingTimeoutMs $TimeoutMs `
            -PacketsPerTarget $PingCount `
            -ResultRoot $OutputDirectory `
            -PatternCount $patterns.Count
        exit 0
    }

    while ($true) {
        $patternSource = if ($Pattern.Count -gt 0) {
            "命令行传入的规则"
        }
        elseif ([string]::IsNullOrWhiteSpace($PatternFile)) {
            "脚本内置的截图规则"
        }
        else {
            $PatternFile
        }

        Write-Host ""
        Write-Host "============================================================" -ForegroundColor Cyan
        Write-Host " IP 通配规则 Ping 扫描器"
        Write-Host "============================================================" -ForegroundColor Cyan
        Write-Host " * 或 x 会自动填充为 0–255。"
        Write-Host " IP 规则来源：$patternSource"
        Write-Host " 并发数：$Concurrency"
        Write-Host " Ping 次数：每个目标 $PingCount 个包"
        Write-Host " 单包超时：$TimeoutMs 毫秒"
        Write-Host " 输出目录：$OutputDirectory"
        Write-Host "------------------------------------------------------------"
        Write-Host " 1. 开始扫描并导出可达 IP"
        Write-Host " 2. 预览通配符展开结果"
        Write-Host " 3. 修改扫描设置"
        Write-Host " 4. 退出"
        Write-Host "------------------------------------------------------------"

        $choice = Read-Host "请选择操作"
        if ($null -eq $choice) {
            exit 0
        }
        $choice = $choice.Trim()
        switch ($choice) {
            "1" {
                $patterns = @(
                    Get-IpPatterns `
                        -DirectPatterns $Pattern `
                        -FilePath $PatternFile `
                        -EmbeddedPatterns $builtInPatterns
                )
                $targets = @(Expand-IpPatterns -Patterns $patterns -TargetLimit $MaxTargets)
                Show-TargetPreview -Patterns $patterns -Targets $targets
                $confirmation = (Read-Host "请仅扫描已获授权的目标，是否继续？[y/N]").Trim()
                if ($confirmation -notmatch '^(?i:y|yes|是|确认)$') {
                    Write-Host "已取消扫描。"
                    continue
                }

                Invoke-PingSweep `
                    -Targets $targets `
                    -MaxConcurrency $Concurrency `
                    -PingTimeoutMs $TimeoutMs `
                    -PacketsPerTarget $PingCount `
                    -ResultRoot $OutputDirectory `
                    -PatternCount $patterns.Count
                $null = Read-Host "按 Enter 键返回菜单"
            }
            "2" {
                $patterns = @(
                    Get-IpPatterns `
                        -DirectPatterns $Pattern `
                        -FilePath $PatternFile `
                        -EmbeddedPatterns $builtInPatterns
                )
                $targets = @(Expand-IpPatterns -Patterns $patterns -TargetLimit $MaxTargets)
                Show-TargetPreview -Patterns $patterns -Targets $targets
                $null = Read-Host "按 Enter 键返回菜单"
            }
            "3" {
                $Concurrency = Read-IntegerSetting `
                    -Label "并发数" `
                    -CurrentValue $Concurrency `
                    -Minimum 1 `
                    -Maximum 256
                $PingCount = Read-IntegerSetting `
                    -Label "每个目标发送的 Ping 包数" `
                    -CurrentValue $PingCount `
                    -Minimum 1 `
                    -Maximum 100
                $TimeoutMs = Read-IntegerSetting `
                    -Label "单个 Ping 包的超时时间（毫秒）" `
                    -CurrentValue $TimeoutMs `
                    -Minimum 100 `
                    -Maximum 10000

                if ($Pattern.Count -eq 0) {
                    $newPatternFile = Read-Host "可选 IP 规则文件 [$patternSource]（输入“内置”恢复截图规则）"
                    if ($newPatternFile -ieq "BUILTIN" -or $newPatternFile -eq "内置") {
                        $PatternFile = ""
                    }
                    elseif (-not [string]::IsNullOrWhiteSpace($newPatternFile)) {
                        $PatternFile = Resolve-ScannerPath -Path $newPatternFile
                    }
                }

                $newOutputDirectory = Read-Host "输出目录 [$OutputDirectory]"
                if (-not [string]::IsNullOrWhiteSpace($newOutputDirectory)) {
                    $OutputDirectory = Resolve-ScannerPath -Path $newOutputDirectory
                }
            }
            "4" {
                exit 0
            }
            default {
                Write-Host "请输入 1、2、3 或 4。" -ForegroundColor Yellow
            }
        }
    }
}
catch {
    Write-Host ""
    Write-Host "错误：$($_.Exception.Message)" -ForegroundColor Red
    if (-not $NonInteractive -and -not $PreviewOnly) {
        $null = Read-Host "按 Enter 键退出"
    }
    exit 1
}
