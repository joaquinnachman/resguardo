# Resguardo

Respaldos para músicos y productores, para Windows y Mac. Hace lo mismo que Cobian Backup (tareas completas, incrementales y diferenciales, ZIP/7z, cifrado, FTP/SFTP, eventos, espejo, correo) y además entiende proyectos de Ableton Live, Cubase y Pro Tools: guarda una versión cada vez que guardás o cerrás el DAW, incluye los samples que están fuera de la carpeta del proyecto y te deja volver a cualquier versión.

## Descargas

- [Windows (instalador)](https://github.com/joaquinnachman/resguardo/raw/main/descargas/Resguardo-Instalador-Windows.exe) · [zip sin instalador](https://github.com/joaquinnachman/resguardo/raw/main/descargas/Resguardo-Windows.zip)
- [Mac (Apple Silicon e Intel)](https://github.com/joaquinnachman/resguardo/raw/main/descargas/Resguardo-Mac.zip)

El instalador de Windows instala sólo para tu usuario, sin pedir permisos de administrador. Las instrucciones están en `LEEME.txt`. Desde la versión 1.1.0, Resguardo se actualiza solo.

Las tareas de Cobian Backup 11 y Cobian Reflector se importan desde Herramientas → Importar desde Cobian.

## Compilar

Requiere Go 1.24 o más nuevo, Python 3 y, para el instalador de Windows, NSIS 3 (`makensis`, paquete `nsis`).

```
go mod tidy
./build.sh
```

Deja en `dist/` el instalador y el zip de Windows y el zip de Mac. Pruebas: `go test ./...`

## Créditos

7-Zip de Igor Pavlov (LGPL) se incluye para los archivos .7z. La lectura de sesiones de Pro Tools se basa en [ptformat](https://github.com/zamaudio/ptformat) de Damien Zammit (LGPL). Tipografía Archivo (Omnibus-Type, SIL Open Font License). El instalador de Windows se arma con [NSIS](https://nsis.sourceforge.io). Para entender el formato de las listas de Cobian Reflector fue útil [backuper](https://github.com/Star69995/backuper).
