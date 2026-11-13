# Resguardo: notas para trabajar en este repositorio

App de respaldos para músicos (Go + interfaz HTML embebida en `ui/`). La interfaz y los mensajes están en español rioplatense.

## Compilar en el entorno de Claude

El acceso a `proxy.golang.org` y a `golang.org/x` puede estar bloqueado. Si es así, clonar los módulos `golang.org/x/*` desde sus espejos de GitHub (`github.com/golang/sys`, `crypto`, `net`, `text`, `term`, `sync`, en versiones compatibles con Go 1.24) y armar un `go.local.mod` con `replace` hacia esas copias (está en `.gitignore`). Después:

```
export GOPROXY=direct GOSUMDB=off GOTOOLCHAIN=local GOFLAGS="-mod=mod -modfile=go.local.mod"
go test ./... && ./build.sh
```

El instalador de Windows sale de `installer/resguardo.nsi` con NSIS (`apt-get install nsis`; `build.sh` lo arma si encuentra `makensis`). Es de 64 bits e instala por usuario en `%LOCALAPPDATA%\Programs\Resguardo`, para que la actualización automática pueda reemplazar el .exe. Se puede probar con Wine 64 bits (`/usr/lib/wine/wine64`, con Xvfb y xdotool para tocar botones).

`rsrc_windows_amd64.syso` (ícono, manifiesto y versión del .exe) sale de `winres/winres.json` con go-winres; al cambiar de versión hay que regenerarlo.

## Publicar una versión nueva (la app se actualiza sola)

1. Subir `AppVersion` en `model.go`, las versiones de `winres/winres.json` (y regenerar el .syso) y la primera línea de `LEEME.txt`.
2. `go test ./... && ./build.sh`
3. Escribir las novedades (una por línea, en lenguaje de usuario) en un archivo de texto.
4. `go run ./tools/publicar -clave <archivo con la clave> -notas <novedades.txt>`
   Copia los zips y el instalador a `descargas/` (con la versión en el nombre y con nombre fijo para el README), borra los de versiones anteriores y escribe `version.json` firmado. Las actualizaciones usan el zip; el instalador es para instalar la primera vez.
5. Commit y push a `main`.

Reglas:
- La clave privada de firma (Ed25519) NUNCA va al repositorio. Está en el documento `claude/resguardo.md` del proyecto "Nerd" de Joaquín. La pública está en `update.go`.
- Nunca volver a publicar una versión con el mismo número y otros archivos: siempre un número nuevo.
- La app consulta `https://raw.githubusercontent.com/joaquinnachman/resguardo/main/version.json` al abrirse y cada 6 horas. La instala cuando no hay respaldos en curso ni un DAW abierto, y si la versión nueva no arranca en 45 s vuelve sola a la anterior.
- Probar actualizaciones sin publicar: variable de entorno `RESGUARDO_ACTUALIZACIONES` con otra dirección de `version.json` (igual se exige la firma).
- Importar de Cobian (`importar.go`): el formato de Cobian 11 se dedujo de listas de ejemplo; si Joaquín pasa su `MainList.lst` real, usarla para ajustar (días de la semana, eventos, FTP) y agregarla a las pruebas sin datos personales.
