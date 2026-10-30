# Autohost Agent

Agente de monitorización y ejecución remota para Linux. Se conecta a la [autohost-cloud-api](../autohost-cloud-api), envía heartbeats y métricas, y ejecuta comandos remotos via gRPC.

## Stack

- **Go** 1.25+
- **gRPC** (recepción de comandos remotos)
- **WebSocket** (Gorilla WS)
- **systemd** (modo servicio)

## Requisitos previos

- Linux (amd64 o arm64)
- Go 1.25+ (solo para compilar desde fuente)
- La API ([autohost-cloud-api](../autohost-cloud-api)) accesible desde el nodo
- Un token de enrollment generado desde el dashboard

---

## Instalación

### Opción 1 — Script automático (recomendado)

Descarga el binario, crea el usuario de sistema `autohost`, genera `/etc/autohost/config.yaml` y registra el servicio systemd:

```bash
curl -fsSL https://raw.githubusercontent.com/mazapanuwu13/autohost-agent/main/scripts/install.sh | bash
```

El script pedirá de forma interactiva:
- URL de la API (`AUTOHOST_API_URL`)
- Token de enrollment (`AUTOHOST_TOKEN`)
- ID del nodo (default: `hostname`)
- Tags opcionales

### Instalación no-interactiva (CI / automatización)

```bash
AUTOHOST_API_URL=https://mi-api.example.com \
AUTOHOST_TOKEN=autohost-node_xxxx \
AUTOHOST_NODE_ID=mi-servidor \
AUTOHOST_TAGS="production,web" \
bash <(curl -fsSL https://raw.githubusercontent.com/mazapanuwu13/autohost-agent/main/scripts/install.sh)
```

### Versión específica

```bash
VERSION=v0.2.0 bash <(curl -fsSL https://raw.githubusercontent.com/mazapanuwu13/autohost-agent/main/scripts/install.sh)
```

### Verificar instalación

```bash
autohost-agent --version
systemctl status autohost-agent
```

---

### Opción 2 — Compilar desde fuente

```bash
git clone https://github.com/mazapanuwu13/autohost-agent.git
cd autohost-agent
make build
```

El binario queda en `./autohost-agent`. Para instalar como servicio:

```bash
make install   # copia binario + servicio systemd + config inicial
make enable    # habilita e inicia el servicio
```

---

## Configuración

El archivo de configuración vive en `/etc/autohost/config.yaml` (permisos `600`):

```yaml
api_url: "http://192.168.1.10:8080"   # URL HTTP del backend
ws_url: ""                             # reservado
grpc_address: "192.168.1.10:9090"     # dirección gRPC del backend
agent_token: "autohost-node_xxxx"     # token de nodo obtenido en el enrollment
node_id: "mi-servidor"                # ID único del nodo
tags: ["production", "web"]
```

> Este archivo es generado automáticamente por `autohost up` (autohost-cli) o por el script de instalación. No editarlo manualmente salvo casos excepcionales.

---

## Comandos Make

```bash
make build       # Compilar binario en ./autohost-agent
make run         # Compilar y ejecutar con config.example.yaml
make test        # Ejecutar tests
make clean       # Eliminar binario compilado
make install     # Instalar binario + servicio systemd (requiere sudo)
make uninstall   # Desinstalar (preserva /etc/autohost/)
make enable      # Habilitar e iniciar el servicio systemd
make disable     # Detener y deshabilitar el servicio
make status      # Ver estado del servicio
make logs        # Seguir logs en tiempo real (journalctl -f)
make release     # Compilar binarios multi-plataforma + tag git en dist/
```

---

## Estructura del proyecto

```
.
├── cmd/agent/            # Entry point (acepta <config-path> o --version)
├── internal/
│   ├── agent/            # Orquestador principal y Composition Root (config, lifecycle, DI)
│   ├── dispatcher/       # Router y despachador reactivo de Jobs (built-in + scripts custom)
│   ├── domain/           # Tipos de dominio puros (Job, App, CustomCommand, Heartbeat, Metrics)
│   ├── services/         # Servicios en background (heartbeat, métricas, enrollment, autoheal)
│   ├── security/         # Identidad mTLS/tokens, firmado de payloads y sanitización
│   ├── transport/        # Clientes de red y comunicación hacia el Control Plane
│   │   ├── http/         # Cliente REST fallback para Heartbeat, Métricas y Jobs
│   │   ├── nodepb/       # Protocol Buffers generados (gRPC)
│   │   └── ws.go         # Cliente WebSocket y streams de diagnóstico
│   ├── adapters/         # Adaptadores de infraestructura externa y persistencia
│   │   ├── docker/       # Docker SDK, compose validator, swarm y gestión de contenedores
│   │   ├── caddy/        # Enrutamiento dinámico y reverse proxy Caddy
│   │   ├── hypervisor/   # Detección y telemetría de virtualización KVM/libvirt
│   │   └── state/        # Almacén atómico local (idempotencia de jobs y cache de desired state)
│   └── platform/         # Utilidades de bajo nivel del sistema
│       ├── exec/         # Ejecución y runner seguro de comandos shell
│       ├── fs/           # Operaciones del sistema de archivos
│       └── paths/        # Gestión de rutas del sistema (/var/lib/autohost)
├── pkg/
│   └── sysinfo/          # Lectura de CPU, memoria, disco y red (paquete público reutilizable)
├── configs/agent.yaml    # Plantilla de configuración
├── scripts/install.sh    # Instalador automático
├── autohost-agent.service # Unidad systemd
└── Makefile
```

---

## Funcionalidades

### Heartbeat
- **Endpoint:** `POST /v1/heartbeats/heartbeat`
- **Frecuencia:** cada 15 s
- Envía: `node_id`, `hostname`, `os`, `uptime_seconds`, `tags`

### Métricas del sistema
- **Endpoint:** `POST /v1/node-metrics/metrics`
- **Frecuencia:** cada 15 s
- Envía: uso de CPU, memoria (total/usado/disponible) y disco raíz

### Ejecución remota de comandos (gRPC)
- Se conecta al servidor gRPC del backend
- Registra los comandos disponibles en el nodo (built-in + scripts custom)
- Ejecuta trabajos recibidos y reporta el resultado (output / error / status)
- Scripts custom: archivos `.sh` en `~/.autohost/custom-commands/`

---

## Servicio systemd

El agente corre como usuario dedicado `autohost` con hardening de seguridad:

```
/etc/systemd/system/autohost-agent.service
```

Comandos útiles:

```bash
sudo systemctl status autohost-agent
sudo systemctl restart autohost-agent
sudo journalctl -u autohost-agent -f
```

---

## Seguridad

- Config en `/etc/autohost/config.yaml` con permisos `600` (solo root/autohost)
- El servicio corre sin privilegios de root (`NoNewPrivileges=true`)
- Filesystem de solo lectura excepto `StateDirectory=autohost`


## Licencia / License

Este proyecto es software libre y de código abierto (Open Source), distribuido bajo la licencia **[GNU Affero General Public License v3.0 (AGPLv3)](LICENSE)**.

Eres libre de usarlo, modificarlo y hospedar tu propia instancia (self-hosted) de forma gratuita. De acuerdo con los términos de AGPLv3, si modificas el software y ofreces servicios a través de una red, debes poner a disposición el código fuente correspondiente bajo la misma licencia.

Para licencias comerciales alternativas o consultas sobre la versión SaaS gestionada, contacta al equipo de AutoHost.
