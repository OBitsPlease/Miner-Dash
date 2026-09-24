@echo off
net session >nul 2>&1
if %errorlevel% neq 0 (
  powershell.exe -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
  exit /b
)
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0setup-cloudflare-tunnel.ps1"
if %errorlevel% neq 0 (
  echo.
  echo Miner Dash remote access setup failed.
)
echo.
pause
