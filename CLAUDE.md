# Resguardo: notas para trabajar en este repositorio

App de respaldos para músicos (Go + interfaz HTML embebida en `ui/`). La interfaz y los mensajes están en español rioplatense.

## Compilar en el entorno de Claude

El acceso a `proxy.golang.org` y a `golang.org/x` puede estar bloqueado. Si es así, clonar los módulos `golang.org/x/*` desde sus espejos de GitHub (`github.com/golang/sys`, `crypto`, `net`, `text`, `term`, `sync`, en versiones compatibles con Go 1.24) y armar un `go.local.mod` con `replace` hacia esas copias (está en `.gitignore`). Después:

```
export GOPROXY=direct GOSUMDB=off GOTOOLCHAIN=local GOFLAGS="-mod=mod -modfile=go.local.mod"
go test ./... && ./build.sh
```

El instalador de Windows sale de `installer/resguardo.nsi` con NSIS (`apt-get install nsis`; `build.sh` lo arma si encuentra `makensis`). Es de 64 bits e instala por usuario en `%LOCALAPPDATA%\Programs\Resguardo`, para que la actualización automática pueda reemplazar el .exe. Se puede probar con Wine 64 bits (`/usr/lib/wine/wine64`, con Xvfb y xdotool para tocar botones). Wine se cae con "free(): invalid pointer" si hereda el entorno completo de la sesión de Claude: hay que arrancarlo con entorno limpio, por ejemplo `env -i HOME=/root PATH=/usr/bin:/bin DISPLAY=:99 WINEPREFIX=... WINEDEBUG=-all /usr/lib/wine/wine64 ...`.

`rsrc_windows_amd64.syso` (ícono, manifiesto y versión del .exe) sale de `winres/winres.json` con go-winres; al cambiar de versión hay que regenerarlo.

## Publicar una versión nueva (la app se actualiza sola)

1. Subir `AppVersion` en `model.go`, las versiones de `winres/winres.json` (y regenerar el .syso) y la primera línea de `LEEME.txt`, y sumar la versión a `HISTORIA` en `ui/app.js` (pantalla "Acerca de").
2. `go test ./... && ./build.sh`
3. Escribir las novedades (una por línea, en lenguaje de usuario) en un archivo de texto.
4. `go run ./tools/publicar -clave <archivo con la clave> -notas <novedades.txt>`
   Copia los zips y el instalador a `descargas/` (con la versión en el nombre y con nombre fijo para el README), borra los de versiones anteriores y escribe `version.json` firmado. Las actualizaciones usan el zip; el instalador es para instalar la primera vez.
5. Commit y push a `main`.

Reglas:
- La clave privada de firma (Ed25519) NUNCA va al repositorio. Está en el documento `claude/resguardo.md` del proyecto "Nerd" de Joaquín. La pública está en `update.go`.
- Nunca volver a publicar una versión con el mismo número y otros archivos: siempre un número nuevo.
- La app consulta `https://raw.githubusercontent.com/joaquinnachman/resguardo/main/version.json` al abrirse, al abrir la ventana (si la última búsqueda tiene más de 10 min) y cada hora. La instala cuando no hay respaldos en curso ni un DAW abierto, y si la versión nueva no arranca en 45 s vuelve sola a la anterior.
- Probar actualizaciones sin publicar: variable de entorno `RESGUARDO_ACTUALIZACIONES` con otra dirección de `version.json` (igual se exige la firma).
- Importar de Cobian (`importar.go`): formato de Cobian 11 confirmado con la lista real de Joaquín (prueba `TestImportCobian11Real`, con nombres cambiados): filtros 1 = carpeta y 3 = máscara; "Create separated backups=false" con un origen y "Always create top directory=false" = lo de adentro del origen directo en el destino (modo directo + `contentsOnly`). Falta ver: `cbSettings.ini` (opciones generales), eventos, FTP y el orden de los días cuando no son los 7.
- La interfaz no puede tener scripts en línea (CSP `script-src 'self'` en `securityHeaders`).
- Al mandarle archivos a Joaquín por el chat, los `.exe` van dentro de un `.zip` (el chat no deja bajar `.exe`), y él no puede subir `.lst` de Cobian tal cual (UTF-16): pedírselos en `.zip` o guardados como UTF-8.

## Hecho en 1.3.0

Un solo tipo de copia ("Copia de carpetas", `completo` + `Direct`, compara fecha y tamaño; `simplifyTask` migra completo/incremental/diferencial al cargar, al guardar y al importar). Puntos de restauración opcionales (`restorePoints`, `_Resguardo versiones/<AAAA-MM-DD HH.MM.SS>`, últimos 5, también para lo que quita el espejo). Vista previa al ejecutar a mano (`PreviewTask` en `preview.go`, ruta `preview`). Logo A (se genera con rsvg-convert + PIL; `ui/icono-claro.png` para la apariencia clara). Cobian sólo aparece en Importar desde Cobian. La apariencia se elige en Opciones → General → Tema.

## Pendiente para la próxima versión (no publicar hasta que Joaquín lo pida)

1. Ícono viejo en la barra de tareas de Windows después de actualizar a 1.3.0 (la ventana ya muestra el nuevo): es la caché de íconos de Windows, que guarda el ícono del .exe y de los accesos directos (anclado, escritorio, Inicio). Ya está hecho en el código, sin publicar: `refreshShellIcons()` en `platform_windows.go` (la primera vez que arranca cada versión avisa con SHChangeNotify y corre `ie4uinit.exe -show`; marca en `iconos-version` dentro de la carpeta de datos) y SHChangeNotify en el instalador. Probado bajo Wine que no rompe nada; falta ver en el Windows de Joaquín que el ícono cambie. Publicar con número nuevo (1.3.1), nunca rearmar la 1.3.0.
