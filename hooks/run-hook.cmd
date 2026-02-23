: << 'CMDBLOCK'
@echo off
"C:\Program Files\Git\bin\bash.exe" -l -c "\"$(cygpath -u \"%CLAUDE_PLUGIN_ROOT%\")/hooks/%1.sh\""
exit /b
CMDBLOCK

"${CLAUDE_PLUGIN_ROOT}/hooks/$1.sh"
