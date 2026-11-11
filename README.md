# Resguardo

Respaldos para músicos y productores, para Windows y Mac. Hace lo mismo que Cobian Backup (tareas completas, incrementales y diferenciales, ZIP/7z, cifrado, FTP/SFTP, eventos, espejo, correo) y además entiende proyectos de Ableton Live, Cubase y Pro Tools: guarda una versión cada vez que guardás o cerrás el DAW, incluye los samples que están fuera de la carpeta del proyecto y te deja volver a cualquier versión.

## Descargas

- [Windows](https://github.com/joaquinnachman/resguardo/raw/main/descargas/Resguardo-Windows.zip)
- [Mac (Apple Silicon e Intel)](https://github.com/joaquinnachman/resguardo/raw/main/descargas/Resguardo-Mac.zip)

Las instrucciones de instalación están en el `LEEME.txt` de cada zip. Desde la versión 1.1.0, Resguardo se actualiza solo.

## Compilar

Requiere Go 1.24 o más nuevo y Python 3.

```
go mod tidy
./build.sh
```

Deja en `dist/` el zip de Windows y el de Mac. Pruebas: `go test ./...`

## Créditos

7-Zip de Igor Pavlov (LGPL) se incluye para los archivos .7z. La lectura de sesiones de Pro Tools se basa en [ptformat](https://github.com/zamaudio/ptformat) de Damien Zammit (LGPL). Tipografía Archivo (Omnibus-Type, SIL Open Font License).
