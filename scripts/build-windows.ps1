
# Sonic - Build Script para Windows
# Compila o Sonic para Windows e gera o binário otimizado

param(
    [switch]$Release,
    [switch]$NoInstall
)

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "         Sonic Build (Windows)" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Caminhos
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir
$OutputDir = Join-Path $RepoRoot "dist"
$ExeName = "sonic-windows-amd64.exe"
$OutputPath = Join-Path $OutputDir $ExeName

# Cria pasta dist se não existir
if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
}

# Flags de compilação
$LdFlags = ""
if ($Release) {
    $LdFlags = "-ldflags='-s -w'"
}

Write-Host "1. Compilando Sonic..." -ForegroundColor Yellow
Push-Location $RepoRoot
try {
    $Cmd = "go build $LdFlags -o `"$OutputPath`" ."
    Write-Host "   Executando: $Cmd" -ForegroundColor Gray
    Invoke-Expression $Cmd
    if ($LASTEXITCODE -ne 0) {
        Write-Host "   ERRO: Falha na compilação!" -ForegroundColor Red
        exit 1
    }
    Write-Host "   OK!" -ForegroundColor Green
} finally {
    Pop-Location
}

# Obtém tamanho do arquivo
$FileSize = (Get-Item $OutputPath).Length / 1MB
Write-Host ""
Write-Host "2. Binário gerado:" -ForegroundColor Yellow
Write-Host "   Caminho: $OutputPath" -ForegroundColor White
Write-Host "   Tamanho: $([math]::Round($FileSize, 2)) MB" -ForegroundColor White

# Instalação automática, se solicitado
if (-not $NoInstall) {
    Write-Host ""
    Write-Host "3. Iniciando instalação..." -ForegroundColor Yellow
    $InstallerPath = Join-Path $ScriptDir "install.ps1"
    if (Test-Path $InstallerPath) {
        & powershell.exe -ExecutionPolicy Bypass -File $InstallerPath -Force
    } else {
        Write-Host "   Aviso: Instalador não encontrado" -ForegroundColor Gray
    }
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host "          Build concluído!" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host ""
