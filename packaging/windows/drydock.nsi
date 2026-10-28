; Drydock's Windows installer (decision D8, docs/packaging.md section 2): NSIS, per user, no administrator rights.
;
; makensis /DVERSION=0.1.0-beta.1 /DVIVERSION=0.1.0.0 /DSRC=<staged folder> /DOUTFILE=<installer.exe> packaging\windows\drydock.nsi
;
; SRC holds what the download carries: drydock.exe, tools\ (decision P77), the egress proxies for the sandbox, LICENSE and
; THIRD_PARTY_NOTICES.md. The release workflow (.github/workflows/release.yml) stages it, builds this, installs the result on
; the runner and uninstalls it again.
;
; Where things go:
;   program   %LOCALAPPDATA%\Programs\Drydock  (user-writable, so the updater can swap drydock.exe in place: section 4)
;   data      %LOCALAPPDATA%\Drydock           (never written by the installer; the uninstaller leaves it unless asked)
;   registry  HKCU only (the Apps & features entry); nothing under HKLM
;
; Silent use (the workflow, IT scripts): drydock-setup.exe /S [/D=<folder>]; uninstall.exe /S keeps the data.

Unicode true
ManifestDPIAware true
RequestExecutionLevel user
SetCompressor /SOLID lzma

!ifndef VERSION
  !error "VERSION is required (/DVERSION=1.2.3)"
!endif
!ifndef VIVERSION
  !error "VIVERSION is required, four numbers (/DVIVERSION=1.2.3.0)"
!endif
!ifndef SRC
  !error "SRC is required: the staged folder with drydock.exe"
!endif
!ifndef OUTFILE
  !define OUTFILE "drydock-setup.exe"
!endif

!define APP "Drydock"
!define PUBLISHER "Drydock"
!define EXE "drydock.exe"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Drydock"
; the Evergreen WebView2 runtime's client id (Microsoft Learn, "Detect if a WebView2 Runtime is already installed")
!define WEBVIEW2_GUID "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

Name "${APP}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\${APP}"
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation"
BrandingText "${APP} ${VERSION}"

VIProductVersion "${VIVERSION}"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "${APP} installer"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "LegalCopyright" "Copyright (c) 2026, Kyle Maguire"

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"

!define MUI_ICON "..\..\build\windows\icon.ico"
!define MUI_UNICON "..\..\build\windows\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Start ${APP}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${SRC}\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

; WebView2 draws the window. It ships with Windows 11 and nearly every Windows 10; without it Drydock cannot open, so the
; installer says so instead of installing something that will not start. Nothing is downloaded on the person's behalf (D9).
Function CheckWebView2
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  ${EndIf}
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    MessageBox MB_OK|MB_ICONSTOP "${APP} needs the Microsoft Edge WebView2 Runtime, which is not installed.$\r$\n$\r$\nGet the Evergreen Runtime from https://developer.microsoft.com/microsoft-edge/webview2/ and run this installer again." /SD IDOK
    SetErrorLevel 3
    Quit
  ${EndIf}
FunctionEnd

; A running Drydock holds drydock.exe open. The installer never kills it (a run may be in the middle of something): it asks the
; person to close it, and a silent install fails with exit code 4 instead of half-installing.
Function CloseRunningApp
  ${IfNot} ${FileExists} "$INSTDIR\${EXE}"
    Return
  ${EndIf}
  ; Windows lets a running program's file be renamed, but not opened for writing: that is the test (a rename passed, and the
  ; silent install then left the old program in place and said it had succeeded; release run 2, 2026-10-27)
  retry:
    ClearErrors
    FileOpen $0 "$INSTDIR\${EXE}" a
    ${If} ${Errors}
      MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "${APP} is running. Close it, then choose Retry." /SD IDCANCEL IDRETRY retry
      SetErrorLevel 4
      Quit
    ${EndIf}
    FileClose $0
FunctionEnd

Function .onInit
  Call CheckWebView2
FunctionEnd

Section "Drydock" SecMain
  SectionIn RO
  Call CloseRunningApp
  SetOutPath "$INSTDIR"
  File "${SRC}\${EXE}"
  File "${SRC}\LICENSE"
  File "${SRC}\THIRD_PARTY_NOTICES.md"
  File /nonfatal "${SRC}\drydock-egress-linux-amd64"
  File /nonfatal "${SRC}\drydock-egress-linux-arm64"
  ; the bundled tools are replaced whole, so a tool dropped from a release does not linger
  RMDir /r "$INSTDIR\tools"
  SetOutPath "$INSTDIR\tools"
  File /nonfatal /r "${SRC}\tools\*.*"
  SetOutPath "$INSTDIR"

  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; per user: the current user's Start menu, never All Users
  SetShellVarContext current
  CreateShortCut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\${EXE}" 0

  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${APP}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${EXE},0"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Function un.onInit
  SetShellVarContext current
FunctionEnd

Section "Uninstall"
  ; the uninstaller cannot remove a running program either; same rule as installing
  retry:
    ClearErrors
    Delete "$INSTDIR\${EXE}"
    ${If} ${Errors}
      MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "${APP} is running. Close it, then choose Retry." /SD IDCANCEL IDRETRY retry
      SetErrorLevel 4
      Quit
    ${EndIf}
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\THIRD_PARTY_NOTICES.md"
  Delete "$INSTDIR\drydock-egress-linux-amd64"
  Delete "$INSTDIR\drydock-egress-linux-arm64"
  ; the updater keeps the replaced executable beside the new one; it is program files, not data
  Delete "$INSTDIR\${EXE}.old"
  RMDir /r "$INSTDIR\tools"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APP}.lnk"
  DeleteRegKey HKCU "${UNINST_KEY}"

  ; Drydock never deletes data (brief section 21): the data folder stays unless the person says otherwise here. The default
  ; answer, and the only answer in a silent uninstall, is No.
  ${If} ${FileExists} "$LOCALAPPDATA\${APP}\*.*"
    MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "Also delete your ${APP} data?$\r$\n$\r$\n$LOCALAPPDATA\${APP} holds your projects, chats, backups and settings. Your repositories are not in it and are never touched.$\r$\n$\r$\nChoose No to keep it (you can install ${APP} again and carry on)." /SD IDNO IDNO keep
    RMDir /r "$LOCALAPPDATA\${APP}"
  keep:
  ${EndIf}
SectionEnd
