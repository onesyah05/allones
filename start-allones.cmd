@echo off
setlocal
cd /d "%~dp0"
set "ALLONES_HOME=%~dp0.test-e2e\home"
set "ALLONES_HOST=127.0.0.1"
set "ALLONES_PORT=8788"
"%~dp0allones.exe" serve
