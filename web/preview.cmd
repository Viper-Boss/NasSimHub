@echo off
setlocal EnableExtensions DisableDelayedExpansion
cd /d "%~dp0"
rem 家卡 HomeSIM 本地副本启动脚本（端口 5174，避免与 Codex 原目录的 5173 冲突）
set "HOMESIM_NODE="
for /f "delims=" %%N in ('where.exe node.exe 2^>nul') do if not defined HOMESIM_NODE call :try_node "%%N"
if not defined HOMESIM_NODE call :try_node "%USERPROFILE%\.zcode\workspace\default\.tools\node\node.exe"
if not defined HOMESIM_NODE call :try_node "%USERPROFILE%\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe"
if not defined HOMESIM_NODE call :try_node "%ProgramFiles%\nodejs\node.exe"
if not defined HOMESIM_NODE goto :missing_node
for %%N in ("%HOMESIM_NODE%") do set "PATH=%%~dpN;%PATH%"
echo Node.js: "%HOMESIM_NODE%"
"%HOMESIM_NODE%" --version
if not exist "node_modules\vite\bin\vite.js" (
  echo Dependencies are missing. Run npm ci in this web directory first.
  goto :failed
)
set "VITE_MODEMDECK_FIXTURE=1"
echo HomeSIM demo (local copy): no hardware is connected. Press Ctrl+C to stop.
"%HOMESIM_NODE%" "node_modules\vite\bin\vite.js" --host 127.0.0.1 --port 5174 --strictPort --open /messages
if errorlevel 1 goto :failed
exit /b 0

:missing_node
echo No compatible Node.js runtime found.
goto :failed

:failed
if /i not "%~1"=="--check" pause
exit /b 1

:try_node
if not exist "%~1" exit /b 0
"%HOMESIM_NODE%" --version >nul 2>&1
set "HOMESIM_NODE=%~1"
exit /b 0
