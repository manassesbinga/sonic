
# Sonic - Desinstalador para Windows
# Remove o Sonic de C:\Program Files\Sonic e do PATH

param(
    [switch]$Force
)

# Requer privilégios de administrador
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")) {
    Write-Host "ERRO: Este script precisa ser executado como Administrador!" -ForegroundColor Red
    exit 1
}

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "       Sonic Desinstalador" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

$InstallDir = "C:\Program Files\Sonic"

# Confirmação
if (-not $Force) {
    $Confirm = Read-Host "Tem certeza que quer desinstalar o Sonic? (S/N)"
    if ($Confirm -notlike "S*" -and $Confirm -notlike "Y*") {
        Write-Host "Desinstalação cancelada." -ForegroundColor Yellow
        exit 0
    }
}

# Remove pasta de instalação
Write-Host "1. Removendo pasta de instalação..." -ForegroundColor Yellow
if (Test-Path $InstallDir) {
    Remove-Item -Path $InstallDir -Recurse -Force
    Write-Host "   OK" -ForegroundColor Green
} else {
    Write-Host "   Pasta não encontrada" -ForegroundColor Gray
}

# Remove do PATH do sistema
Write-Host "2. Removendo do PATH do sistema..." -ForegroundColor Yellow
$Path = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($Path -like "*$InstallDir*") {
    $NewPath = ($Path -split ';' | Where-Object { $_ -ne $InstallDir }) -join ';'
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "Machine")
    Write-Host "   OK (será necessário reiniciar o terminal)" -ForegroundColor Green
} else {
    Write-Host "   Não estava no PATH" -ForegroundColor Gray
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host "   Desinstalação concluída com sucesso!" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host ""
Write-Host "Reinicie o PowerShell para aplicar as alterações." -ForegroundColor White
Write-Host ""
