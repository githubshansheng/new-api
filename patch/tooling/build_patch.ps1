[CmdletBinding()]
param(
    [switch]$SkipFrontendBuild
)

$ErrorActionPreference = "Stop"
$PatchId = "new-api-20260716"
$PatchDate = "2026-07-16"
$BaselineCommit = "7c28993f6bd9e92616f3f578212577f8b7c40b45"
$RepositoryRoot = [System.IO.Path]::GetFullPath(
    (Join-Path $PSScriptRoot "..\..")
)
$DateRoot = Join-Path $RepositoryRoot "patch\$PatchDate"
$PackageDir = Join-Path $DateRoot $PatchId
$ArchivePath = Join-Path $DateRoot "$PatchId-linux-windows-amd64-arm64.tar.gz"
$ArchiveChecksumPath = "$ArchivePath.sha256"
$LegacyArchivePath = Join-Path $DateRoot "$PatchId-linux-amd64-arm64.tar.gz"
$ExcludeFile = Join-Path $RepositoryRoot "patch\PATCH_EXCLUDE_WHITELIST.txt"
$ExceptionFile = Join-Path $RepositoryRoot "patch\PATCH_INCLUDE_EXCEPTIONS.txt"
$ArchiveBuilder = Join-Path $RepositoryRoot "patch\tooling\create_patch_archive.py"
$BuildRoot = Join-Path $RepositoryRoot ".cache\patch-build\$PatchId"
$TemporaryIndex = Join-Path $BuildRoot "index"
$TemporaryObjects = Join-Path $BuildRoot "objects"

function Read-PathList {
    param([string]$Path)

    return @(
        Get-Content -LiteralPath $Path -Encoding UTF8 |
            ForEach-Object { $_.Trim() } |
            Where-Object { $_ -and -not $_.StartsWith("#") }
    )
}

function Invoke-Checked {
    param(
        [string]$Command,
        [string[]]$Arguments,
        [string]$WorkingDirectory = $RepositoryRoot
    )

    Push-Location $WorkingDirectory
    try {
        & $Command @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Command failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

function Write-Utf8File {
    param(
        [string]$Path,
        [string[]]$Lines
    )

    $content = ""
    if ($Lines.Count -gt 0) {
        $content = ($Lines -join "`n") + "`n"
    }
    [System.IO.File]::WriteAllText(
        $Path,
        $content,
        [System.Text.UTF8Encoding]::new($false)
    )
}

foreach ($command in @("git", "go", "python")) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "Required command not found: $command"
    }
}
$BunCommand = Get-Command bun -ErrorAction SilentlyContinue
$RsbuildCommand = Join-Path $RepositoryRoot "web\node_modules\.bin\rsbuild.exe"
if (-not $SkipFrontendBuild -and -not $BunCommand -and
    -not (Test-Path -LiteralPath $RsbuildCommand)) {
    throw "Bun is unavailable and the existing Rsbuild executable was not found"
}

$dateRootFull = [System.IO.Path]::GetFullPath($DateRoot)
$packageFull = [System.IO.Path]::GetFullPath($PackageDir)
$buildRootFull = [System.IO.Path]::GetFullPath($BuildRoot)
$temporaryObjectsFull = [System.IO.Path]::GetFullPath($TemporaryObjects)
if (-not $packageFull.StartsWith(
    $dateRootFull + [System.IO.Path]::DirectorySeparatorChar,
    [System.StringComparison]::OrdinalIgnoreCase
)) {
    throw "Refusing to replace package directory outside the date root: $packageFull"
}
if (-not $temporaryObjectsFull.StartsWith(
    $buildRootFull + [System.IO.Path]::DirectorySeparatorChar,
    [System.StringComparison]::OrdinalIgnoreCase
)) {
    throw "Refusing to replace temporary objects outside the build root"
}

New-Item -ItemType Directory -Force -Path $DateRoot, $BuildRoot | Out-Null
if (Test-Path -LiteralPath $PackageDir) {
    Remove-Item -LiteralPath $PackageDir -Recurse -Force
}
foreach ($path in @(
    $ArchivePath,
    $ArchiveChecksumPath,
    $LegacyArchivePath,
    "$LegacyArchivePath.sha256"
)) {
    if (Test-Path -LiteralPath $path) {
        Remove-Item -LiteralPath $path -Force
    }
}
if (Test-Path -LiteralPath $TemporaryIndex) {
    Remove-Item -LiteralPath $TemporaryIndex -Force
}
if (Test-Path -LiteralPath $TemporaryObjects) {
    Remove-Item -LiteralPath $TemporaryObjects -Recurse -Force
}
New-Item -ItemType Directory -Force -Path `
    $PackageDir, `
    (Join-Path $PackageDir "assets"), `
    (Join-Path $PackageDir "DDL"), `
    (Join-Path $PackageDir "bin\linux-amd64"), `
    (Join-Path $PackageDir "bin\linux-arm64"), `
    (Join-Path $PackageDir "bin\windows-amd64"), `
    (Join-Path $PackageDir "bin\windows-arm64"), `
    $TemporaryObjects | Out-Null

Invoke-Checked git @("cat-file", "-e", "$BaselineCommit^{commit}")
$gitDirectory = (
    & git -C $RepositoryRoot rev-parse --path-format=absolute --git-dir
).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Unable to resolve Git directory"
}

$savedIndex = $env:GIT_INDEX_FILE
$savedObjectDirectory = $env:GIT_OBJECT_DIRECTORY
$savedAlternateObjects = $env:GIT_ALTERNATE_OBJECT_DIRECTORIES
try {
    $env:GIT_INDEX_FILE = $TemporaryIndex
    $env:GIT_OBJECT_DIRECTORY = $TemporaryObjects
    $env:GIT_ALTERNATE_OBJECT_DIRECTORIES = Join-Path $gitDirectory "objects"

    Invoke-Checked git @("-C", $RepositoryRoot, "read-tree", $BaselineCommit)

    Invoke-Checked git @("-C", $RepositoryRoot, "add", "-A", "--", ".")
    $excludedPathspecs = @(
        foreach ($pattern in Read-PathList $ExcludeFile) {
            ":(glob)$pattern"
        }
    )
    if ($excludedPathspecs.Count -gt 0) {
        Invoke-Checked git (
            @(
                "-C", $RepositoryRoot,
                "reset", "--quiet", $BaselineCommit, "--"
            ) + $excludedPathspecs
        )
    }

    $exceptions = Read-PathList $ExceptionFile
    if ($exceptions.Count -gt 0) {
        Invoke-Checked git (
            @("-C", $RepositoryRoot, "add", "-f", "--") + $exceptions
        )
    }
    Invoke-Checked git @(
        "-C", $RepositoryRoot,
        "update-index", "--chmod=+x", "--", "new-api.sh"
    )

    $sourcePatch = Join-Path $PackageDir "source.patch"
    Invoke-Checked git @(
        "-C", $RepositoryRoot,
        "diff", "--cached", "--binary", "--full-index", "--no-ext-diff",
        "--src-prefix=a/", "--dst-prefix=b/",
        "--output=$sourcePatch",
        $BaselineCommit, "--"
    )

    $included = @(
        & git -C $RepositoryRoot diff --cached --name-only $BaselineCommit -- |
            Where-Object { $_ } |
            Sort-Object -Unique
    )
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to enumerate included source files"
    }
}
finally {
    $env:GIT_INDEX_FILE = $savedIndex
    $env:GIT_OBJECT_DIRECTORY = $savedObjectDirectory
    $env:GIT_ALTERNATE_OBJECT_DIRECTORIES = $savedAlternateObjects
    if (Test-Path -LiteralPath $TemporaryIndex) {
        Remove-Item -LiteralPath $TemporaryIndex -Force
    }
    if (Test-Path -LiteralPath $TemporaryObjects) {
        if (-not $temporaryObjectsFull.StartsWith(
            $buildRootFull + [System.IO.Path]::DirectorySeparatorChar,
            [System.StringComparison]::OrdinalIgnoreCase
        )) {
            throw "Refusing to remove temporary objects outside the build root"
        }
        Remove-Item -LiteralPath $TemporaryObjects -Recurse -Force
    }
}

$savedErrorActionPreference = $ErrorActionPreference
$ErrorActionPreference = "Continue"
try {
    $trackedCandidates = @(
        & git -C $RepositoryRoot diff --name-only $BaselineCommit -- 2>$null
    )
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to enumerate tracked source changes"
    }
    $untrackedCandidates = @(
        & git -C $RepositoryRoot ls-files --others --exclude-standard 2>$null
    )
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to enumerate untracked source changes"
    }
}
finally {
    $ErrorActionPreference = $savedErrorActionPreference
}
$candidates = @($trackedCandidates + $untrackedCandidates) |
    Where-Object { $_ } |
    Sort-Object -Unique
$includedSet = [System.Collections.Generic.HashSet[string]]::new(
    [System.StringComparer]::Ordinal
)
foreach ($path in $included) {
    [void]$includedSet.Add($path)
}
$excluded = @(
    foreach ($path in $candidates) {
        if (-not $includedSet.Contains($path)) {
            $path
        }
    }
)
Write-Utf8File (Join-Path $PackageDir "FILES_INCLUDED.txt") $included
Write-Utf8File (Join-Path $PackageDir "FILES_EXCLUDED.txt") $excluded

Copy-Item -LiteralPath (Join-Path $DateRoot "patchctl.sh") `
    -Destination (Join-Path $PackageDir "patchctl.sh")
Copy-Item -LiteralPath (Join-Path $DateRoot "manifest.env") `
    -Destination (Join-Path $PackageDir "manifest.env")
Copy-Item -LiteralPath (Join-Path $DateRoot "README.md") `
    -Destination (Join-Path $PackageDir "README.md")
Copy-Item -LiteralPath (Join-Path $DateRoot "USAGE.md") `
    -Destination (Join-Path $PackageDir "USAGE.md")
Copy-Item -LiteralPath (Join-Path $RepositoryRoot "new-api.sh") `
    -Destination (Join-Path $PackageDir "assets\new-api.sh")
Copy-Item -LiteralPath (Join-Path $RepositoryRoot "new-api.ps1") `
    -Destination (Join-Path $PackageDir "assets\new-api.ps1")
Copy-Item -Path (Join-Path $RepositoryRoot "patch\DDL\*") `
    -Destination (Join-Path $PackageDir "DDL")

if (-not $SkipFrontendBuild) {
    $savedDisableEslint = $env:DISABLE_ESLINT_PLUGIN
    $savedVersion = $env:VITE_REACT_APP_VERSION
    try {
        if ($BunCommand) {
            Invoke-Checked bun @("install", "--frozen-lockfile") `
                (Join-Path $RepositoryRoot "web")
        }
        $env:DISABLE_ESLINT_PLUGIN = "true"
        $env:VITE_REACT_APP_VERSION = $PatchId
        if ($BunCommand) {
            Invoke-Checked bun @("run", "build") `
                (Join-Path $RepositoryRoot "web\default")
        }
        else {
            Invoke-Checked $RsbuildCommand @("build") `
                (Join-Path $RepositoryRoot "web\default")
        }
        $env:DISABLE_ESLINT_PLUGIN = $savedDisableEslint
        if ($BunCommand) {
            Invoke-Checked bun @("run", "build") `
                (Join-Path $RepositoryRoot "web\classic")
        }
        else {
            Invoke-Checked $RsbuildCommand @("build") `
                (Join-Path $RepositoryRoot "web\classic")
        }
    }
    finally {
        $env:DISABLE_ESLINT_PLUGIN = $savedDisableEslint
        $env:VITE_REACT_APP_VERSION = $savedVersion
    }
}

$savedGoos = $env:GOOS
$savedGoarch = $env:GOARCH
$savedCgo = $env:CGO_ENABLED
$savedGoCache = $env:GOCACHE
try {
    $env:CGO_ENABLED = "0"
    $env:GOCACHE = Join-Path $RepositoryRoot ".cache\go-build"
    New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null
    foreach ($targetOS in @("linux", "windows")) {
        $env:GOOS = $targetOS
        $extension = if ($targetOS -eq "windows") { ".exe" } else { "" }
        foreach ($architecture in @("amd64", "arm64")) {
            $env:GOARCH = $architecture
            $binaryDirectory = Join-Path $PackageDir "bin\$targetOS-$architecture"
            Invoke-Checked go @(
                "build",
                "-trimpath",
                "-ldflags",
                "-s -w -X github.com/QuantumNous/new-api/common.Version=$PatchId",
                "-o",
                (Join-Path $binaryDirectory "new-api$extension"),
                "."
            )
            Invoke-Checked go @(
                "build",
                "-trimpath",
                "-ldflags",
                "-s -w",
                "-o",
                (Join-Path $binaryDirectory "patchdb$extension"),
                ".\patch\tooling\patchdb"
            )
        }
    }
}
finally {
    $env:GOOS = $savedGoos
    $env:GOARCH = $savedGoarch
    $env:CGO_ENABLED = $savedCgo
    $env:GOCACHE = $savedGoCache
}

$requiredFiles = @(
    "patchctl.sh",
    "manifest.env",
    "README.md",
    "USAGE.md",
    "source.patch",
    "FILES_INCLUDED.txt",
    "FILES_EXCLUDED.txt",
    "assets\new-api.sh",
    "assets\new-api.ps1",
    "DDL\liandong-payment.mysql.sql",
    "DDL\liandong-payment.postgresql.sql",
    "DDL\liandong-payment.sqlite.sql",
    "DDL\liandong-payment.sqlite-fresh.sql",
    "bin\linux-amd64\new-api",
    "bin\linux-amd64\patchdb",
    "bin\linux-arm64\new-api",
    "bin\linux-arm64\patchdb",
    "bin\windows-amd64\new-api.exe",
    "bin\windows-amd64\patchdb.exe",
    "bin\windows-arm64\new-api.exe",
    "bin\windows-arm64\patchdb.exe"
)
foreach ($relativePath in $requiredFiles) {
    if (-not (Test-Path -LiteralPath (Join-Path $PackageDir $relativePath))) {
        throw "Package is incomplete: $relativePath"
    }
}

$checksumLines = @(
    Get-ChildItem -LiteralPath $PackageDir -Recurse -File |
        Where-Object { $_.Name -ne "SHA256SUMS" } |
        ForEach-Object {
            $relative = $_.FullName.Substring(
                $PackageDir.Length + 1
            ).Replace("\", "/")
            $hash = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash
            "$($hash.ToLowerInvariant())  $relative"
        } |
        Sort-Object
)
Write-Utf8File (Join-Path $PackageDir "SHA256SUMS") $checksumLines

Invoke-Checked python @(
    $ArchiveBuilder,
    "--package-dir",
    $PackageDir,
    "--output",
    $ArchivePath,
    "--archive-root",
    $PatchId
)
$archiveHash = (
    Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256
).Hash.ToLowerInvariant()
Write-Utf8File $ArchiveChecksumPath @(
    "$archiveHash  $([System.IO.Path]::GetFileName($ArchivePath))"
)

Write-Host "Package: $PackageDir"
Write-Host "Archive: $ArchivePath"
Write-Host "SHA-256: $archiveHash"
