
# Sonic - Instalador Completo para Windows
# Baixa (se necessário) + instala o Sonic em C:\Program Files\Sonic

param(
    [switch]$Force,
    [string]$Version = "local",
    [switch]$NoDownload
)

# Requer privilégios de administrador
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")) {
    Write-Host "ERRO: Este script precisa ser executado como Administrador!" -ForegroundColor Red
    Write-Host "Abra o PowerShell como Administrador e execute novamente." -ForegroundColor Yellow
    exit 1
}

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "         Sonic Instalador Completo" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Caminhos
$InstallDir = "C:\Program Files\Sonic"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent (Split-Path -Parent $ScriptDir)
$ExeName = "sonic-windows-amd64.exe"
$LocalExe = Join-Path $RepoRoot "dist\$ExeName"
$TargetExe = Join-Path $InstallDir "sonic.exe"

# Detectar silenciosamente se o WSL 2 está disponível no host Windows
$Windir = $env:SystemRoot
if (-not $Windir) { $Windir = "C:\Windows" }
$WslExe = Join-Path $Windir "System32\wsl.exe"
$HasWsl = Test-Path $WslExe
$SourceLinuxExe = $null

# Passo 1: Baixar ou usar binário local
if ($Version -eq "local" -or $NoDownload) {
    Write-Host "1. Usando binário local..." -ForegroundColor Yellow
    if (-not (Test-Path $LocalExe)) {
        Write-Host "   ERRO: Binário não encontrado em: $LocalExe" -ForegroundColor Red
        Write-Host "   Execute '.\scripts\build-windows.ps1' primeiro." -ForegroundColor Yellow
        exit 1
    }
    $SourceExe = $LocalExe
    Write-Host "   OK: $LocalExe" -ForegroundColor Green

    # Se o WSL estiver presente, tenta compilar silenciosamente para Linux
    if ($HasWsl) {
        $LinuxLocalPath = Join-Path $RepoRoot "dist\sonic-linux-amd64"
        try {
            $env:GOOS = "linux"
            $env:GOARCH = "amd64"
            # Compilação silenciosa
            & go build -o $LinuxLocalPath "$RepoRoot\cmd\main.go" *>$null
            if (Test-Path $LinuxLocalPath) {
                $SourceLinuxExe = $LinuxLocalPath
            }
        } catch {
            # Falha silenciosa
        } finally {
            $env:GOOS = $null
            $env:GOARCH = $null
        }
    }
} else {
    Write-Host "1. Baixando versão $Version..." -ForegroundColor Yellow
    $GitHubRepo = "manassesbinga/sonic"
    $ReleaseUrl = if ($Version -eq "latest") {
        "https://api.github.com/repos/$GitHubRepo/releases/latest"
    } else {
        "https://api.github.com/repos/$GitHubRepo/releases/tags/$Version"
    }
    try {
        $Release = Invoke-RestMethod -Uri $ReleaseUrl -ErrorAction Stop
        $Asset = $Release.assets | Where-Object { $_.name -like "*windows*amd64*" } | Select-Object -First 1
        if (-not $Asset) {
            Write-Host "   ERRO: Nenhum arquivo encontrado para Windows amd64" -ForegroundColor Red
            exit 1
        }
        $TempDir = Join-Path $env:TEMP "sonic-install"
        if (-not (Test-Path $TempDir)) { New-Item -ItemType Directory -Path $TempDir -Force | Out-Null }
        $TempExe = Join-Path $TempDir $ExeName
        Write-Host "   Baixando: $($Asset.name)..." -ForegroundColor Gray
        Invoke-WebRequest -Uri $Asset.browser_download_url -OutFile $TempExe -ErrorAction Stop
        $SourceExe = $TempExe
        Write-Host "   OK" -ForegroundColor Green

        # Tentar baixar silenciosamente o binário do Linux se o WSL estiver disponível
        if ($HasWsl) {
            $AssetLinux = $Release.assets | Where-Object { $_.name -like "*linux*amd64*" } | Select-Object -First 1
            if ($AssetLinux) {
                $TempLinuxExe = Join-Path $TempDir "sonic-linux-amd64"
                Invoke-WebRequest -Uri $AssetLinux.browser_download_url -OutFile $TempLinuxExe -ErrorAction SilentlyContinue *>$null
                if (Test-Path $TempLinuxExe) {
                    $SourceLinuxExe = $TempLinuxExe
                }
            }
        }
    } catch {
        Write-Host "   ERRO: Falha ao baixar a versão $Version" -ForegroundColor Red
        exit 1
    }
}

# Passo 2: Instalar
Write-Host ""
Write-Host "2. Instalando..." -ForegroundColor Yellow
if (Test-Path $InstallDir) {
    if (-not $Force) {
        Write-Host "   A pasta $InstallDir já existe. Use -Force para sobrescrever." -ForegroundColor Red
        exit 1
    }
    Write-Host "   Limpando pasta existente..." -ForegroundColor Gray
    Remove-Item -Path "$InstallDir\*" -Recurse -Force
} else {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

Copy-Item -Path $SourceExe -Destination $TargetExe -Force
Write-Host "   Binário copiado" -ForegroundColor Green

$GuiaPath = Join-Path $RepoRoot "GUIA_DE_USO.txt"
if (Test-Path $GuiaPath) {
    Copy-Item -Path $GuiaPath -Destination $InstallDir -Force
    Write-Host "   Guia de uso copiado" -ForegroundColor Green
}

Write-Host "3. Adicionando ao PATH do sistema..." -ForegroundColor Yellow
$Path = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($Path -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$Path;$InstallDir", "Machine")
    Write-Host "   OK (será necessário reiniciar o terminal)" -ForegroundColor Green
} else {
    Write-Host "   Já está no PATH" -ForegroundColor Gray
}

# Instalação silenciosa no WSL 2 se disponível
if ($HasWsl -and $SourceLinuxExe -and (Test-Path $SourceLinuxExe)) {
    try {
        $WslSourcePath = $SourceLinuxExe.Replace("\", "/").Replace("C:", "/mnt/c")
        & wsl -u root -- sh -c "mkdir -p /usr/local/bin && cp `"$WslSourcePath`" /usr/local/bin/sonic && chmod +x /usr/local/bin/sonic" *>$null
    } catch {
        # Falha silenciosa
    }
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host "     Instalação concluída com sucesso!" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host ""
Write-Host "Pasta de instalação: $InstallDir" -ForegroundColor White
Write-Host "Arquivo executável: $TargetExe" -ForegroundColor White
Write-Host ""
Write-Host "Para começar:" -ForegroundColor Cyan
Write-Host "1. Feche e reabra o PowerShell para atualizar o PATH" -ForegroundColor White
Write-Host "2. Execute 'sonic --help' para ver os comandos" -ForegroundColor White
if ($HasWsl -and $SourceLinuxExe -and (Test-Path $SourceLinuxExe)) {
    Write-Host "3. [WSL 2] O Sonic Linux foi integrado ao WSL 2. Para rodar com eBPF, execute: 'wsl sudo sonic'" -ForegroundColor Gray
}
Write-Host ""
