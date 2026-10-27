Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
!include "WinVer.nsh"

Name "MouseBridge ${VERSION}"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\MouseBridge"
InstallDirRegKey HKCU "Software\MouseBridge" "InstallLocation"
RequestExecutionLevel user
SetCompressor /SOLID lzma
SetCompressorDictSize 32
VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=2052 "ProductName" "MouseBridge"
VIAddVersionKey /LANG=2052 "FileDescription" "MouseBridge 局域网互控安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "LegalCopyright" "MouseBridge contributors"

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\MouseBridge.exe"
!define MUI_FINISHPAGE_RUN_TEXT "启动 MouseBridge"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

!macro RequireClosed
  closed_retry:
  FindWindow $0 "MouseBridgeDesktop"
  IntCmp $0 0 closed_done
  MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION "请先退出正在运行的 MouseBridge，再点击“确定”。退出时程序会释放鼠标并断开共享。" IDOK closed_retry
  Abort
  closed_done:
!macroend

Function .onInit
  SetShellVarContext current
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_OK|MB_ICONSTOP "MouseBridge 需要 Windows 10 或更新版本。"
    Abort
  ${EndIf}
  ${IfNot} ${IsNativeAMD64}
  ${AndIfNot} ${IsNativeARM64}
    MessageBox MB_OK|MB_ICONSTOP "当前安装程序支持 Windows x64 和 ARM64。"
    Abort
  ${EndIf}
  !insertmacro RequireClosed
FunctionEnd

Function un.onInit
  SetShellVarContext current
  !insertmacro RequireClosed
FunctionEnd

Section "MouseBridge" MainSection
  SetOutPath "$INSTDIR"
  ${If} ${IsNativeARM64}
    File /oname=MouseBridge.exe "${PAYLOAD}/arm64/MouseBridge.exe"
    File /oname=mousebridge-core.exe "${PAYLOAD}/arm64/mousebridge-core.exe"
    File /oname=BUILD-INFO.json "${PAYLOAD}/arm64/BUILD-INFO.json"
    File /oname=SHA256SUMS "${PAYLOAD}/arm64/SHA256SUMS"
  ${Else}
    File /oname=MouseBridge.exe "${PAYLOAD}/amd64/MouseBridge.exe"
    File /oname=mousebridge-core.exe "${PAYLOAD}/amd64/mousebridge-core.exe"
    File /oname=BUILD-INFO.json "${PAYLOAD}/amd64/BUILD-INFO.json"
    File /oname=SHA256SUMS "${PAYLOAD}/amd64/SHA256SUMS"
  ${EndIf}
  File /oname=使用说明.txt "${PAYLOAD}/WINDOWS_APP.txt"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateDirectory "$SMPROGRAMS\MouseBridge"
  CreateShortcut "$SMPROGRAMS\MouseBridge\MouseBridge.lnk" "$INSTDIR\MouseBridge.exe"
  CreateShortcut "$SMPROGRAMS\MouseBridge\卸载 MouseBridge.lnk" "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\MouseBridge" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "DisplayName" "MouseBridge"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "Publisher" "MouseBridge"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "DisplayIcon" "$INSTDIR\MouseBridge.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge" "NoRepair" 1
SectionEnd

Section "Uninstall"
  Delete "$INSTDIR\MouseBridge.exe"
  Delete "$INSTDIR\mousebridge-core.exe"
  Delete "$INSTDIR\BUILD-INFO.json"
  Delete "$INSTDIR\SHA256SUMS"
  Delete "$INSTDIR\使用说明.txt"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\MouseBridge\MouseBridge.lnk"
  Delete "$SMPROGRAMS\MouseBridge\卸载 MouseBridge.lnk"
  RMDir "$SMPROGRAMS\MouseBridge"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MouseBridge"
  DeleteRegKey HKCU "Software\MouseBridge"
SectionEnd
