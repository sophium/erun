@echo off
REM erun shim: Windows equivalent of the `erun` alias on macOS/Linux.
REM Put this dir (erun-cli) on PATH; typing `erun ...` forwards to run.ps1,
REM which rebuilds from source and runs the latest build. ASCII-only on purpose.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1" %*
exit /b %ERRORLEVEL%
