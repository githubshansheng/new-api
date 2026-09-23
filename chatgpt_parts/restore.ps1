param(
    [string]$RepoPath = (Split-Path -Parent $PSScriptRoot),
    [string]$CodexHome = (Join-Path $env:USERPROFILE ".codex")
)

$ErrorActionPreference = "Stop"
$partsRoot = Join-Path $RepoPath "chatgpt_parts"
$indexPath = Join-Path $RepoPath "chatgpt_index.tsv"

if (-not (Test-Path -LiteralPath $indexPath)) {
    throw "Missing index file: $indexPath"
}

$index = Import-Csv -LiteralPath $indexPath -Delimiter "`t"
$groups = $index | Group-Object {
    $_.path -replace "\.part\d+$", ""
}

foreach ($group in $groups) {
    $logicalName = $group.Name
    $parts = $group.Group |
        ForEach-Object {
            [pscustomobject]@{
                Row = $_
                PartPath = Join-Path $partsRoot $_.path
                PartNumber = [int]($_.path -replace "^.*\.part", "")
            }
        } |
        Sort-Object PartNumber

    for ($i = 0; $i -lt $parts.Count; $i++) {
        $expectedNumber = $i + 1
        if ($parts[$i].PartNumber -ne $expectedNumber) {
            throw "Missing part ${logicalName}: expected part${expectedNumber}"
        }

        $part = $parts[$i]
        if (-not (Test-Path -LiteralPath $part.PartPath -PathType Leaf)) {
            throw "Missing part file: $($part.PartPath)"
        }

        $file = Get-Item -LiteralPath $part.PartPath
        if ($file.Length -ne [long]$part.Row.size) {
            throw "Size mismatch for $($part.Row.path)"
        }

        $hash = (Get-FileHash -LiteralPath $part.PartPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($hash -ne $part.Row.sha256) {
            throw "SHA-256 mismatch for $($part.Row.path)"
        }
    }

    $jsonlPath = Join-Path $partsRoot $logicalName
    $gzDirectory = Split-Path -Parent $jsonlPath
    New-Item -Path $gzDirectory -ItemType Directory -Force | Out-Null

    $headerStream = [System.IO.File]::OpenRead($parts[0].PartPath)
    try {
        $header = [byte[]]::new(3)
        $headerLength = $headerStream.Read($header, 0, $header.Length)
    }
    finally {
        $headerStream.Dispose()
    }
    $isGzip = $headerLength -eq 3 -and $header[0] -eq 0x1f -and $header[1] -eq 0x8b -and $header[2] -eq 0x08

    if ($isGzip) {
        $gzPath = "$jsonlPath.gz"
        $gzStream = [System.IO.File]::Create($gzPath)
        try {
            foreach ($part in $parts) {
                $partStream = [System.IO.File]::OpenRead($part.PartPath)
                try {
                    $partStream.CopyTo($gzStream)
                }
                finally {
                    $partStream.Dispose()
                }
            }
        }
        finally {
            $gzStream.Dispose()
        }

        try {
            $gzip = [System.IO.File]::OpenRead($gzPath)
            try {
                $decompressed = New-Object System.IO.Compression.GzipStream($gzip, [System.IO.Compression.CompressionMode]::Decompress)
                $output = [System.IO.File]::Create($jsonlPath)
                try {
                    $decompressed.CopyTo($output)
                }
                finally {
                    $output.Dispose()
                }
            }
            finally {
                $gzip.Dispose()
            }
        }
        finally {
            [System.IO.File]::Delete($gzPath)
        }
    }
    else {
        $output = [System.IO.File]::Create($jsonlPath)
        try {
            foreach ($part in $parts) {
                $partStream = [System.IO.File]::OpenRead($part.PartPath)
                try {
                    $partStream.CopyTo($output)
                }
                finally {
                    $partStream.Dispose()
                }
            }
        }
        finally {
            $output.Dispose()
        }
    }

    $relative = $logicalName
    if ($relative.StartsWith("archived_sessions\", [System.StringComparison]::OrdinalIgnoreCase)) {
        $target = Join-Path (Join-Path $CodexHome "archived_sessions") $relative.Substring(18)
    }
    elseif ($relative.StartsWith("sessions\", [System.StringComparison]::OrdinalIgnoreCase)) {
        $target = Join-Path (Join-Path $CodexHome "sessions") $relative.Substring(9)
    }
    elseif ($relative -eq "session_index.jsonl") {
        $target = Join-Path $CodexHome "session_index.jsonl"
    }
    else {
        throw "Unexpected logical file: $relative"
    }

    $targetDirectory = Split-Path -Parent $target
    New-Item -Path $targetDirectory -ItemType Directory -Force | Out-Null
    Copy-Item -LiteralPath $jsonlPath -Destination $target -Force

    [System.IO.File]::Delete($jsonlPath)
}

Write-Host "Restored Codex sessions to $CodexHome"






