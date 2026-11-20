[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$protocolRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../plugin-packages'))
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

<# 与 build-packages.sh 一样只打包公开协议文件，支持未安装 zip 的 Windows。 #>
foreach ($manifest in Get-ChildItem -LiteralPath $protocolRoot -Filter manifest.json -Recurse -File) {
    $packageRoot = $manifest.Directory.FullName
    if ($manifest.Directory.Parent.FullName -ne $protocolRoot) { continue }
    $outputPath = Join-Path $protocolRoot ($manifest.Directory.Name + '.beeftv-plugin')
    $stream = [System.IO.File]::Open($outputPath, [System.IO.FileMode]::Create)
    $archive = [System.IO.Compression.ZipArchive]::new($stream, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($name in @('manifest.json', 'README.md', 'LICENSE', 'docs', 'assets')) {
            $source = Join-Path $packageRoot $name
            if (-not (Test-Path -LiteralPath $source)) { continue }
            foreach ($file in Get-ChildItem -LiteralPath $source -Recurse -File) {
                $relative = $file.FullName.Substring($packageRoot.Length + 1).Replace('\', '/')
                [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $file.FullName, $relative, [System.IO.Compression.CompressionLevel]::Optimal) | Out-Null
            }
        }
    } finally { $archive.Dispose() }
}
