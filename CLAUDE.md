# autohost-agent

Agente que corre en cada nodo Linux (systemd, normalmente como root) y obedece a la API (ver `../CLAUDE.md`).
Go 1.25, módulo `autohost-agent`. Todo lo que llega de la API se trata como **entrada no confiable**: el
agente es la última barrera antes del host.

## Comandos

```bash
make build        # ./autohost-agent para esta máquina
make test         # go test ./...
go vet ./...
make lab-deploy   # instala el agente en un servidor de pruebas Incus (lab-create, lab-update, lab-logs...)
make node-update  # copia agente + CLI a un nodo real y reinicia
make release TAG=v1.2.3   # crea y sube el tag; CI publica (solo cuando se pida)
```

## Estructura

```
cmd/agent                 entrypoint (acepta <config-path> o --version)
internal/agent            composition root: config, lifecycle, renovación del certificado mTLS
internal/transport        cliente gRPC (stream Connect) y handlers de mensajes: terminal, archivos, logs,
                          diagnósticos, network stats, contenedores; http/ es el fallback REST
internal/transport/nodepb código generado del proto de la API (no editar a mano)
internal/dispatcher       jobs: registro en command.go (`r.Register("tipo", ...)`), uno por archivo
internal/adapters         docker (incl. compose_validator), caddy, hypervisor, state (store atómico)
internal/services         heartbeat, métricas, enrollment, autoheal
internal/security         identidad, sanitización, validación de IPs, firmas
internal/platform         exec (runner), fs, paths (/var/lib/autohost)
pkg/sysinfo               lectura de CPU, memoria, disco y red
```

## Reglas

- **Validar todo lo que llega en un job o mensaje**: rutas, nombres de contenedor, identificadores de
  Postgres, URLs. Funciones existentes: `docker.ValidateCompose` (bloquea privileged, capabilities
  peligrosas, namespaces del host, rutas sensibles y binds fuera del directorio de la app),
  `ValidateScriptSecurity` (scripts custom), `security/ip_validator.go`, `sanitizer.go`. Reutilizarlas antes
  de escribir otra.
- **Rutas**: resolver symlinks y comprobar que quedan dentro del directorio permitido (ver los fixes de
  carpetas compartidas y backups); nunca concatenar rutas recibidas sin limpiar.
- **Comandos**: ejecutar con `platform/exec` (`Exec`, `ExecWithOutput`, `ExecWithDir`) pasando argumentos
  como lista. `ExecShell` solo con scripts fijos, nunca con un string construido a partir de datos recibidos.
- **Logs y diagnósticos**: sanitizar secretos antes de enviarlos a la API.
- **Jobs nuevos**: un archivo en `internal/dispatcher`, registrarlo en `command.go`, test de las
  validaciones (`*_test.go` al lado). El nombre del job (`dominio.accion`) tiene que coincidir con el que
  manda la API.
- **Proto**: el contrato vive en `../autohost-cloud-api/proto` y los flujos se explican en
  `../autohost-cloud-api/api/grpc.md`. Tras `make proto` allí, copiar los `.pb.go` a
  `internal/transport/nodepb` aquí. Campos nuevos opcionales para que agente y API de versiones distintas
  sigan entendiéndose.
- El código debe compilar para linux/amd64 y linux/arm64 (`make build-all`); lo específico de Linux va en
  archivos `_linux.go` con su alternativa `_other.go`.
- Los tests no deben necesitar root, Docker ni red: separar la validación y el parseo en funciones puras y
  testear esas (ver `script_security_test.go`, `compose_validator_test.go`).
