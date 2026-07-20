@echo off
REM emcp shim: Windows equivalent of the `emcp` alias on macOS/Linux.
REM Put this dir (erun-mcp) on PATH; typing `emcp ...` forwards to run.ps1,
REM which rebuilds emcp from source and runs the latest build. ASCII-only.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1" %*
exit /b %ERRORLEVEL%
