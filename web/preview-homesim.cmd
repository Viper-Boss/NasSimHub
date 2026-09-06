@echo off
setlocal EnableExtensions DisableDelayedExpansion
cd /d "%~dp0"
set "HOMESIM_NODE="
rem Explorer may not inherit the runtime PATH available in the development app.
for /f "delims=" %%N in ('where.exe node.exe 2^>nul') do if not defined HOMESIM_NODE call :try_node "%%N"
if not defined HOMESIM_NODE call :try_node "%USERPROFILE%\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe"
if not defined HOMESIM_NODE call :try_node "%ProgramFiles%\nodejs\node.exe"
if not defined HOMESIM_NODE call :try_node "%LOCALAPPDATA%\Programs\nodejs\node.exe"
if not defined HOMESIM_NODE goto :missing_node
for %%N in ("%HOMESIM_NODE%") do set "PATH=%%~dpN;%PATH%"
echo Node.js: "%HOMESIM_NODE%"
"%HOMESIM_NODE%" --version
if not exist "node_modules\vite\bin\vite.js" (
  echo Dependencies are missing. Run npm ci in this web directory first.
  goto :failed
)
if /i "%~1"=="--check" goto :check
set "VITE_MODEMDECK_FIXTURE=1"
echo HomeSIM demo: no hardware is connected. Press Ctrl+C to stop.
"%HOMESIM_NODE%" "node_modules\vite\bin\vite.js" --host 127.0.0.1 --port 5173 --strictPort --open /messages
if errorlevel 1 goto :failed
exit /b 0

:check
"%HOMESIM_NODE%" "node_modules\vite\bin\vite.js" --version
if errorlevel 1 goto :failed
echo HomeSIM launcher check passed.
exit /b 0

:missing_node
echo No compatible Node.js runtime found. Node.js 24 or newer is required.
goto :failed

:failed
if /i not "%~1"=="--check" pause
exit /b 1

:try_node
if not exist "%~1" exit /b 0
"%~1" -e "process.exit(Number(process.versions.node.split('.')[0]) >= 24 ? 0 : 1)" >nul 2>&1
if errorlevel 1 exit /b 0
set "HOMESIM_NODE=%~1"
exit /b 0
