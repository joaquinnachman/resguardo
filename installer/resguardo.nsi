; Instalador de Resguardo para Windows (NSIS 3, se compila en Linux con makensis).
;   makensis -DVERSION=1.2.0 -DSRC=<carpeta con Resguardo.exe y LEEME.txt> -DOUT=<instalador.exe> installer/resguardo.nsi
;
; Se instala sólo para el usuario, en %LOCALAPPDATA%\Programs\Resguardo, sin
; pedir permisos de administrador: así Resguardo puede reemplazarse a sí mismo
; cuando se actualiza solo.

Target amd64-unicode ; Resguardo es sólo de 64 bits
ManifestDPIAware true
SetCompressor /SOLID lzma
RequestExecutionLevel user

!include "MUI2.nsh"
!include "FileFunc.nsh"

!ifndef VERSION
  !error "Falta -DVERSION"
!endif
!ifndef SRC
  !error "Falta -DSRC"
!endif
!ifndef OUT
  !define OUT "Resguardo-${VERSION}-Instalador-Windows.exe"
!endif
!ifndef ICON
  !define ICON "../icons/app.ico"
!endif
!define UNKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Resguardo"
!define RUNKEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define UNINST "Desinstalar Resguardo.exe"

Name "Resguardo"
Caption "Instalar Resguardo ${VERSION}"
UninstallCaption "Desinstalar Resguardo"
OutFile "${OUT}"
InstallDir "$LOCALAPPDATA\Programs\Resguardo"
BrandingText "Resguardo ${VERSION} · Claraboya Estudio"
ShowInstDetails nevershow
ShowUninstDetails nevershow

VIProductVersion "${VERSION}.0"
VIFileVersion "${VERSION}.0"
VIAddVersionKey /LANG=1034 "ProductName" "Resguardo"
VIAddVersionKey /LANG=1034 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1034 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1034 "FileDescription" "Instalador de Resguardo"
VIAddVersionKey /LANG=1034 "CompanyName" "Claraboya Estudio"
VIAddVersionKey /LANG=1034 "LegalCopyright" "Joaquín Nachman · Claraboya Estudio"

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_WELCOMEFINISHPAGE_BITMAP "lateral.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "lateral.bmp"
!define MUI_ABORTWARNING
!define MUI_ABORTWARNING_TEXT "¿Cancelar la instalación de Resguardo?"

!define MUI_WELCOMEPAGE_TITLE "Instalar Resguardo ${VERSION}"
!define MUI_WELCOMEPAGE_TEXT "Respaldos para músicos y productores.$\r$\n$\r$\nSe instala sólo para tu usuario y no pide permisos de administrador. Después se actualiza solo.$\r$\n$\r$\nSi Resguardo está abierto, se cierra para poder instalarlo. Tus tareas, opciones e historial no se tocan.$\r$\n$\r$\nTocá Instalar para seguir."
!define MUI_PAGE_CUSTOMFUNCTION_SHOW WelcomeShow
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_TITLE "Resguardo quedó instalado"
!define MUI_FINISHPAGE_TEXT "Lo encontrás en el menú Inicio y en el escritorio. Mientras está abierto queda un ícono en la bandeja, al lado del reloj."
!define MUI_FINISHPAGE_RUN "$INSTDIR\Resguardo.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Abrir Resguardo"
!insertmacro MUI_PAGE_FINISH

!define MUI_WELCOMEPAGE_TITLE "Desinstalar Resguardo"
!define MUI_WELCOMEPAGE_TEXT "Se va a quitar Resguardo de esta computadora.$\r$\n$\r$\nTus respaldos no se tocan: quedan donde están. Al final te pregunta si querés borrar también tus tareas y opciones.$\r$\n$\r$\nTocá Desinstalar para seguir."
!define MUI_PAGE_CUSTOMFUNCTION_SHOW un.WelcomeShow
!insertmacro MUI_UNPAGE_WELCOME
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "Spanish"

; La página de bienvenida va directo a instalar: el botón dice "Instalar".
Function WelcomeShow
  GetDlgItem $0 $HWNDPARENT 1
  SendMessage $0 ${WM_SETTEXT} 0 "STR:&Instalar"
FunctionEnd
Function un.WelcomeShow
  GetDlgItem $0 $HWNDPARENT 1
  SendMessage $0 ${WM_SETTEXT} 0 "STR:&Desinstalar"
FunctionEnd

Section "Resguardo"
  SetShellVarContext current

  ; Cerrar Resguardo si está abierto (con el programa nuevo, que sabe pedirle
  ; a cualquier versión anterior que se cierre).
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "/oname=resguardo-cerrar.exe" "${SRC}/Resguardo.exe"
  nsExec::Exec '"$PLUGINSDIR\resguardo-cerrar.exe" --cerrar'
  Pop $0
  SetOutPath "$TEMP"
  Delete "$PLUGINSDIR\resguardo-cerrar.exe"

  SetOutPath "$INSTDIR"
  ; Si algo todavía lo tiene abierto, se aparta (Windows deja renombrar un
  ; programa en uso); Resguardo borra estos restos solo.
  IfFileExists "$INSTDIR\Resguardo.exe" 0 copiar
    System::Call 'kernel32::GetTickCount()i.r1'
    Rename "$INSTDIR\Resguardo.exe" "$INSTDIR\Resguardo.exe.viejo-$1"
  copiar:
  File "${SRC}/Resguardo.exe"
  File "${SRC}/LEEME.txt"
  WriteUninstaller "$INSTDIR\${UNINST}"

  CreateShortCut "$SMPROGRAMS\Resguardo.lnk" "$INSTDIR\Resguardo.exe" "" "$INSTDIR\Resguardo.exe" 0 SW_SHOWNORMAL "" "Respaldos para músicos y productores"
  CreateShortCut "$DESKTOP\Resguardo.lnk" "$INSTDIR\Resguardo.exe" "" "$INSTDIR\Resguardo.exe" 0 SW_SHOWNORMAL "" "Respaldos para músicos y productores"

  WriteRegStr HKCU "${UNKEY}" "DisplayName" "Resguardo"
  WriteRegStr HKCU "${UNKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNKEY}" "Publisher" "Claraboya Estudio"
  WriteRegStr HKCU "${UNKEY}" "DisplayIcon" "$INSTDIR\Resguardo.exe,0"
  WriteRegStr HKCU "${UNKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNKEY}" "UninstallString" '"$INSTDIR\${UNINST}"'
  WriteRegStr HKCU "${UNKEY}" "QuietUninstallString" '"$INSTDIR\${UNINST}" /S'
  WriteRegStr HKCU "${UNKEY}" "URLInfoAbout" "https://github.com/joaquinnachman/resguardo"
  WriteRegDWORD HKCU "${UNKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNKEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNKEY}" "EstimatedSize" "$0"
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  IfFileExists "$INSTDIR\Resguardo.exe" 0 +3
    nsExec::Exec '"$INSTDIR\Resguardo.exe" --cerrar'
    Pop $0

  Delete "$INSTDIR\Resguardo.exe"
  Delete "$INSTDIR\Resguardo.exe.*"
  Delete "$INSTDIR\LEEME.txt"
  Delete "$INSTDIR\${UNINST}"
  RMDir "$INSTDIR"

  Delete "$SMPROGRAMS\Resguardo.lnk"
  Delete "$DESKTOP\Resguardo.lnk"
  DeleteRegValue HKCU "${RUNKEY}" "Resguardo"
  DeleteRegKey HKCU "${UNKEY}"

  MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "¿Borrar también tus tareas, opciones e historial de Resguardo?$\r$\n$\r$\nTus respaldos no se tocan: quedan donde están." /SD IDNO IDNO conservar
    RMDir /r "$APPDATA\Resguardo"
  conservar:
SectionEnd
