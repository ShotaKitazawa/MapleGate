; Built by build-installer.sh, which passes VERSION, OUT, ICON and the folder of files to ship (STAGE).
Unicode true
!include "LogicLib.nsh"

Name "MapleGate"
OutFile "${OUT}"
InstallDir "$PROGRAMFILES64\MapleGate"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
Icon "${ICON}"
UninstallIcon "${ICON}"

!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\MapleGate"
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

; stopping the service takes the tunnel down and removes its routes, so a running tunnel is cut by an update, as on macOS
Function StopEverything
  nsExec::Exec 'taskkill /IM maplegate.exe /F'
  nsExec::Exec 'sc stop MapleGate'
  Sleep 3000
FunctionEnd

Section "Install"
  SetShellVarContext all
  Call StopEverything
  SetOutPath "$INSTDIR"
  File "${STAGE}\maplegate.exe"
  File "${STAGE}\maplegated.exe"
  File "${STAGE}\wintun.dll"
  File "${STAGE}\MapleGate.ico"
  File "${STAGE}\LICENSE-wintun.txt"

  ; the settings are for the whole machine and an update keeps the ones already there
  CreateDirectory "$APPDATA\MapleGate"
  ${IfNot} ${FileExists} "$APPDATA\MapleGate\config.json"
    File "/oname=$APPDATA\MapleGate\config.json" "${STAGE}\config.json"
  ${EndIf}

  nsExec::Exec 'sc query MapleGate'
  Pop $0
  ${If} $0 != 0
    nsExec::Exec 'sc create MapleGate binPath= "\"$INSTDIR\maplegated.exe\"" start= auto DisplayName= "MapleGate"'
    nsExec::Exec 'sc description MapleGate "Holds the MapleGate tunnel for the tray app"'
    nsExec::Exec 'sc failure MapleGate reset= 86400 actions= restart/5000/restart/5000/restart/5000'
  ${EndIf}
  nsExec::Exec 'sc start MapleGate'

  WriteRegStr HKLM "${RUN_KEY}" "MapleGate" '"$INSTDIR\maplegate.exe"'
  CreateShortCut "$SMPROGRAMS\MapleGate.lnk" "$INSTDIR\maplegate.exe" "" "$INSTDIR\MapleGate.ico"

  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayName" "MapleGate"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "Publisher" "MapleGate"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\MapleGate.ico"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoRepair" 1

  ; started through explorer so it runs as the signed-in person and not as the elevated installer
  Exec '"$WINDIR\explorer.exe" "$INSTDIR\maplegate.exe"'
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  Call un.StopEverything
  nsExec::Exec 'sc delete MapleGate'
  DeleteRegValue HKLM "${RUN_KEY}" "MapleGate"
  DeleteRegKey HKLM "${UNINSTALL_KEY}"
  Delete "$SMPROGRAMS\MapleGate.lnk"
  RMDir /r "$APPDATA\MapleGate"
  RMDir /r "$INSTDIR"
SectionEnd

Function un.StopEverything
  nsExec::Exec 'taskkill /IM maplegate.exe /F'
  nsExec::Exec 'sc stop MapleGate'
  Sleep 3000
FunctionEnd
