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

## Hecho en 1.3.1

Caché de íconos de Windows refrescada al arrancar cada versión nueva (`refreshShellIcons`, falta confirmar en el Windows de Joaquín que la barra de tareas cambie). Disco entero como origen = "sólo lo de adentro" (`sourceIsDriveRoot`). Nunca se leen ni copian `$RECYCLE.BIN`/`RECYCLER`/`RECYCLED`/`System Volume Information` (`isNeverCopyDir`) ni `pagefile.sys`/`hiberfil.sys`/`swapfile.sys`/`DumpStack.log(.tmp)` (`isNeverCopyFile`), en ningún nivel; otras cosas de sistema sólo en la raíz de un disco (`isSystemRootItem`).

## Pendiente para la próxima versión (no publicar hasta que Joaquín lo pida)

1. Filtros: al elegir una carpeta o un archivo con "Buscar…" se agrega directo a la lista, sin tocar "Agregar" (pedido de Joaquín; para sacarla está "Quitar"). Hecho en `filterList` (ui/app.js), sin publicar: "Buscar…" elige carpeta salvo que el tipo sea Archivo, no repite entradas y el selector recuerda el último tipo usado (`lastFilterKind`). Probado con Playwright.
2. Doble aviso siempre antes de borrar (pedido de Joaquín): `confirmSheet(título, cuerpo, botón, danger, sure)` con `sure` pregunta después "¿Estás seguro?" (texto general o armado en el momento). Se usa al borrar una tarea, vaciar el historial, borrar un respaldo, borrar los registros y borrar una carpeta en Herramientas. Todo borrado nuevo tiene que pasar `sure`. Hecho, sin publicar; probado con Playwright (Cancelar en el segundo aviso no borra nada).
3. Pedido de Joaquín (sin hacer todavía): casilla en la tarea "Al conectar el disco" para cuando se enchufa un disco externo que es origen o destino de la tarea, con qué hacer: copiar sola, comparar y mostrar la vista previa, o preguntar (aviso con "Copiar ahora" / "Ver qué cambió" / "Ahora no"). Ideas: reconocer el disco por su número de serie de volumen (Windows) o UUID (Mac), no por la letra, que puede cambiar; detectarlo con WM_DEVICECHANGE (Windows) o mirando /Volumes (Mac), o revisando cada pocos segundos qué discos hay; no repetir si ya se copió hace poco (ej. una vez cada X horas como mínimo); respetar "no con un DAW abierto" si así está configurado. Antes de hacerlo, mostrarle a Joaquín cómo quedaría la opción.
