#!/usr/bin/env bash
# ==============================================================================
#  Autohost Agent — Installer & Auto-configurator
#  Uso:
#    curl -fsSL https://raw.githubusercontent.com/mazapanuwu13/autohost-agent/main/scripts/install.sh | bash
#
#  Variables de entorno opcionales para instalación no-interactiva:
#    AUTOHOST_API_URL   — URL de la API  (ej: https://cloud.autohost.dev)
#    AUTOHOST_TOKEN     — Token de enrolamiento del agente
#    AUTOHOST_NODE_ID   — ID del nodo (default: hostname)
#    AUTOHOST_TAGS      — Tags separados por coma (ej: "production,web")
#    VERSION            — Versión específica (ej: v0.2.0)
#    BIN_DIR            — Directorio de instalación del binario (default: /usr/local/bin)
# ==============================================================================
set -euo pipefail

REPO="mazapanuwu13/autohost-agent"
BIN_NAME="autohost-agent"
SERVICE_NAME="autohost-agent"
CONFIG_DIR="/etc/autohost"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
AGENT_USER="autohost"

VERSION="${VERSION:-}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"

# ──────────────────────────────────────────────
# Helpers
# ──────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log_info()  { echo -e "${CYAN}ℹ️  $*${NC}"; }
log_ok()    { echo -e "${GREEN}✅ $*${NC}"; }
log_warn()  { echo -e "${YELLOW}⚠️  $*${NC}"; }
log_error() { echo -e "${RED}❌ $*${NC}" >&2; }

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
      SUDO="sudo"
    else
      log_error "Se requieren privilegios de root. Instala sudo o ejecuta como root."
      exit 1
    fi
  else
    SUDO=""
  fi
}

# ──────────────────────────────────────────────
# Detección de OS / ARCH
# ──────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64|amd64)   ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  *) log_error "Arquitectura no soportada: $ARCH_RAW"; exit 1 ;;
esac

if [ "$OS" != "linux" ]; then
  log_error "El agente solo soporta Linux."
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

ua() { echo "autohost-agent-installer/1.0 (+https://github.com/${REPO})"; }

sha256_cmd() {
  if command -v sha256sum >/dev/null 2>&1; then echo "sha256sum";
  elif command -v shasum >/dev/null 2>&1; then echo "shasum -a 256";
  else echo ""; fi
}

# ──────────────────────────────────────────────
# Descarga de release
# ──────────────────────────────────────────────
fetch_latest_tag() {
  curl -fsSL -H "User-Agent: $(ua)" \
    "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1
}

download_release() {
  local tag="$1"
  local asset="${BIN_NAME}-${OS}-${ARCH}"
  local url_bin="https://github.com/${REPO}/releases/download/${tag}/${asset}"
  local url_sum="https://github.com/${REPO}/releases/download/${tag}/checksums_${tag}.txt"

  log_info "Descargando binario: $url_bin"
  curl -fLsS -H "User-Agent: $(ua)" -o "${TMP_DIR}/${BIN_NAME}" "$url_bin"

  log_info "Descargando checksums: $url_sum"
  curl -fLsS -H "User-Agent: $(ua)" -o "${TMP_DIR}/checksums.txt" "$url_sum" || {
    log_warn "No se encontró archivo de checksums (continuando sin verificación)."
    return 0
  }

  local shacmd
  shacmd="$(sha256_cmd)"
  if [ -n "$shacmd" ]; then
    log_info "Verificando checksum..."
    (
      cd "$TMP_DIR"
      expected="$(grep -E "[[:space:]]${asset}$" checksums.txt | awk '{print $1}' || true)"
      if [ -z "${expected}" ]; then
        log_warn "No se encontró checksum para ${asset} (continuando)."
      else
        actual="$($shacmd "${BIN_NAME}" | awk '{print $1}')"
        if [ "$expected" != "$actual" ]; then
          log_error "Checksum inválido. Esperado: ${expected}  Actual: ${actual}"
          exit 1
        fi
        log_ok "Checksum verificado."
      fi
    )
  fi
}

install_from_release() {
  local tag
  if [ -n "${VERSION}" ]; then
    tag="${VERSION}"
  else
    log_info "Obteniendo última versión..."
    tag="$(fetch_latest_tag || true)"
    if [ -z "${tag:-}" ]; then
      log_warn "No hay releases publicados o la API no respondió."
      return 1
    fi
  fi
  log_info "Versión a instalar: ${tag}"
  download_release "${tag}"
  chmod +x "${TMP_DIR}/${BIN_NAME}"
  $SUDO mv "${TMP_DIR}/${BIN_NAME}" "${BIN_DIR}/${BIN_NAME}"
  log_ok "Binario instalado en ${BIN_DIR}/${BIN_NAME}"
}

install_from_source() {
  log_info "Compilando desde código fuente..."
  if ! command -v go >/dev/null 2>&1; then
    log_error "Necesitas Go instalado para compilar desde fuente."
    exit 1
  fi
  local tmp_src="${TMP_DIR}/src"
  mkdir -p "$tmp_src"
  if command -v git >/dev/null 2>&1; then
    git clone --depth=1 "https://github.com/${REPO}.git" "$tmp_src"
  else
    log_error "git no encontrado, no se puede compilar desde fuente."
    exit 1
  fi
  (
    cd "$tmp_src"
    local ver="${VERSION:-dev}"
    go build -ldflags "-s -w -X main.Version=${ver}" -o "${TMP_DIR}/${BIN_NAME}" cmd/agent/main.go
  )
  chmod +x "${TMP_DIR}/${BIN_NAME}"
  $SUDO mv "${TMP_DIR}/${BIN_NAME}" "${BIN_DIR}/${BIN_NAME}"
  log_ok "Binario compilado e instalado en ${BIN_DIR}/${BIN_NAME}"
}

# ──────────────────────────────────────────────
# Recopilación de configuración
# ──────────────────────────────────────────────
prompt_or_env() {
  local var_name="$1"
  local prompt_text="$2"
  local default_val="${3:-}"
  local current="${!var_name:-}"

  if [ -n "$current" ]; then
    if echo "$var_name" | grep -qi "token"; then
      log_info "${var_name} = [REDACTED]  (de variable de entorno)"
    else
      log_info "${var_name} = ${current}  (de variable de entorno)"
    fi
    return
  fi

  eval "${var_name}=\"${default_val}\""
  if echo "$var_name" | grep -qi "token"; then
    log_info "${var_name} asignado al valor por defecto (REDACTED)"
  else
    log_info "${var_name} asignado al valor por defecto (${default_val})"
  fi
}

collect_config() {
  echo ""
  echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo -e "${CYAN}  Configuración del Autohost Agent${NC}"
  echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
  echo ""

  prompt_or_env "AUTOHOST_API_URL"  "URL de la API (ej: https://cloud.autohost.dev)" "https://cloud.autohost.dev"
  prompt_or_env "AUTOHOST_TOKEN"    "Token de enrolamiento del nodo"                 "default-token"
  prompt_or_env "AUTOHOST_NODE_ID"  "ID del nodo"                                    "$(hostname)"

  local default_tags=""
  prompt_or_env "AUTOHOST_TAGS"     "Tags del nodo, separados por coma (opcional)"   "$default_tags"

  # Derivar ws_url y grpc_address desde api_url
  local base_url="${AUTOHOST_API_URL}"
  WS_URL="${base_url/https:\/\//wss://}"
  WS_URL="${WS_URL/http:\/\//ws://}"
  WS_URL="${WS_URL%/}/ws"

  # gRPC: usar la nueva dirección
  GRPC_ADDRESS="grpc.autohst.dev:443"

  echo ""
  log_info "Resumen de configuración:"
  echo "   api_url:       ${AUTOHOST_API_URL}"
  echo "   ws_url:        ${WS_URL}"
  echo "   grpc_address:  ${GRPC_ADDRESS}"
  echo "   node_id:       ${AUTOHOST_NODE_ID}"
  echo "   tags:          ${AUTOHOST_TAGS:-<ninguno>}"
  echo ""
}

# ──────────────────────────────────────────────
# Creación de usuario del sistema y almacenamiento
# ──────────────────────────────────────────────
STORAGE_DIR="/var/lib/autohost"

detect_storage() {
  if [ -n "${AUTOHOST_STORAGE_DIR:-}" ]; then
    STORAGE_DIR="$AUTOHOST_STORAGE_DIR"
    log_info "Usando directorio de almacenamiento configurado: ${STORAGE_DIR}"
    return
  fi

  local root_avail_kb
  root_avail_kb=$(df -Pk / | awk 'NR==2 {print $4}')
  local best_mount=""
  local best_avail_kb=0

  while read -r dev fstype mountpoint avail_kb; do
    case "$mountpoint" in
      /|/boot|/boot/*|/snap/*|/run/*|/sys/*|/proc/*|/dev/*|/var/lib/docker/*)
        continue
        ;;
    esac
    case "$fstype" in
      ext4|xfs|btrfs|zfs|f2fs)
        ;;
      *)
        continue
        ;;
    esac
    if [ "${avail_kb:-0}" -gt 33554432 ] && [ "${avail_kb:-0}" -gt "$((root_avail_kb * 2))" ] && [ "${avail_kb:-0}" -gt "$best_avail_kb" ]; then
      best_avail_kb="$avail_kb"
      best_mount="$mountpoint"
    fi
  done < <(df -PTk 2>/dev/null | awk 'NR>1 && $1 ~ /^\/dev\// && $1 !~ /^\/dev\/(loop|zram)/ {print $1, $2, $7, $5}')

  if [ -n "$best_mount" ]; then
    STORAGE_DIR="${best_mount%/}/autohost"
    local best_gb=$((best_avail_kb / 1024 / 1024))
    log_ok "Disco secundario de alta capacidad detectado en ${best_mount} (${best_gb} GB libres) -> Usando ${STORAGE_DIR}"
  else
    STORAGE_DIR="/var/lib/autohost"
  fi
}

setup_storage_dirs() {
  detect_storage

  $SUDO mkdir -p "${STORAGE_DIR}/config" \
                 "${STORAGE_DIR}/templates" \
                 "${STORAGE_DIR}/apps" \
                 "${STORAGE_DIR}/logs" \
                 "${STORAGE_DIR}/state" \
                 "${STORAGE_DIR}/backups" \
                 "${STORAGE_DIR}/shared"

  if [ "$STORAGE_DIR" != "/var/lib/autohost" ]; then
    if [ -d "/var/lib/autohost" ] && [ ! -L "/var/lib/autohost" ]; then
      log_info "Migrando datos previos de /var/lib/autohost hacia ${STORAGE_DIR}..."
      $SUDO cp -a /var/lib/autohost/. "${STORAGE_DIR}/" 2>/dev/null || true
      $SUDO rm -rf /var/lib/autohost
    fi
    if [ ! -e "/var/lib/autohost" ]; then
      $SUDO ln -s "$STORAGE_DIR" /var/lib/autohost
      log_ok "Symlink creado: /var/lib/autohost -> ${STORAGE_DIR}"
    fi
  fi

  $SUDO chown -R "${AGENT_USER}:${AGENT_USER}" "${STORAGE_DIR}" 2>/dev/null || true
  $SUDO chmod 775 "${STORAGE_DIR}/shared" 2>/dev/null || true
  log_ok "Carpeta compartida del nodo lista en ${STORAGE_DIR}/shared"
}

create_system_user() {
  if id -u "$AGENT_USER" >/dev/null 2>&1; then
    log_info "Usuario del sistema '${AGENT_USER}' ya existe."
  else
    log_info "Creando usuario del sistema '${AGENT_USER}'..."
    $SUDO useradd \
      --system \
      --no-create-home \
      --shell /bin/bash \
      --comment "Autohost Agent" \
      "$AGENT_USER"
    log_ok "Usuario '${AGENT_USER}' creado."
  fi

  # Ensure membership in docker group for container management commands (idempotent)
  if getent group docker >/dev/null 2>&1; then
    $SUDO usermod -aG docker "$AGENT_USER"
    log_ok "Usuario '${AGENT_USER}' añadido al grupo docker."
  fi

  setup_storage_dirs
}

# ──────────────────────────────────────────────
# Creación del config.yaml
# ──────────────────────────────────────────────
write_config() {
  log_info "Creando configuración en ${CONFIG_FILE}..."
  $SUDO mkdir -p "$CONFIG_DIR"

  # Construir bloque de tags YAML
  local tags_yaml=""
  if [ -n "${AUTOHOST_TAGS:-}" ]; then
    IFS=',' read -ra tag_arr <<< "$AUTOHOST_TAGS"
    for t in "${tag_arr[@]}"; do
      t="$(echo "$t" | xargs)"  # trim spaces
      tags_yaml="${tags_yaml}  - \"${t}\"\n"
    done
  fi

  $SUDO tee "$CONFIG_FILE" > /dev/null <<EOF
# Autohost Agent — Configuración generada por el instalador
# Generado: $(date -u +"%Y-%m-%dT%H:%M:%SZ")

api_url:      "${AUTOHOST_API_URL}"
ws_url:       "${WS_URL}"
grpc_address: "${GRPC_ADDRESS}"
agent_token:  "${AUTOHOST_TOKEN}"
node_id:      "${AUTOHOST_NODE_ID}"
storage_dir:  "${STORAGE_DIR}"
shared_dir:   "${STORAGE_DIR}/shared"
tags:
$(printf '%b' "${tags_yaml:-  []\n}")
EOF

  $SUDO chown "root:${AGENT_USER}" "$CONFIG_FILE"
  $SUDO chmod 640 "$CONFIG_FILE"
  log_ok "Configuración escrita en ${CONFIG_FILE}"
}

# ──────────────────────────────────────────────
# Instalación del servicio systemd
# ──────────────────────────────────────────────
install_service() {
  log_info "Instalando servicio systemd en ${SERVICE_FILE}..."
  $SUDO tee "$SERVICE_FILE" > /dev/null <<EOF
[Unit]
Description=Autohost Monitoring Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${AGENT_USER}
Group=${AGENT_USER}
ExecStart=${BIN_DIR}/${BIN_NAME} ${CONFIG_FILE}
Environment=DOCKER_CONFIG=${STORAGE_DIR}/.docker
Environment=AUTOHOST_STORAGE_DIR=${STORAGE_DIR}
Environment=AUTOHOST_SHARED_DIR=${STORAGE_DIR}/shared
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=false
StateDirectory=autohost
ReadWritePaths=-/var/lib/autohost -/media -/mnt -/srv -/opt/autohost

[Install]
WantedBy=multi-user.target
EOF

  $SUDO systemctl daemon-reload
  log_ok "Servicio instalado."
}

# ──────────────────────────────────────────────
# Enable + start
# ──────────────────────────────────────────────
enable_service() {
  log_info "Habilitando y arrancando ${SERVICE_NAME}..."
  $SUDO systemctl enable "$SERVICE_NAME"
  $SUDO systemctl restart "$SERVICE_NAME"
  sleep 2

  if $SUDO systemctl is-active --quiet "$SERVICE_NAME"; then
    log_ok "Servicio '${SERVICE_NAME}' activo y corriendo."
  else
    log_warn "El servicio no arrancó correctamente. Revisa los logs:"
    echo "   journalctl -u ${SERVICE_NAME} -n 20 --no-pager"
  fi
}

# ──────────────────────────────────────────────
# MAIN
# ──────────────────────────────────────────────
echo ""
echo -e "${CYAN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║      Autohost Agent — Instalador             ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════╝${NC}"

require_root

# 1. Descargar binario
echo ""
log_info "Paso 1/5: Descargando binario..."
if ! install_from_release; then
  log_warn "No se pudo descargar release. Compilando desde fuente..."
  install_from_source
fi

# 2. Recopilar configuración
echo ""
log_info "Paso 2/5: Configuración del agente..."
collect_config

# 3. Crear usuario de sistema
echo ""
log_info "Paso 3/5: Creando usuario del sistema..."
create_system_user

# 4. Escribir config + servicio
echo ""
log_info "Paso 4/5: Instalando archivos de configuración y servicio..."
write_config
install_service

# 5. Activar servicio
echo ""
log_info "Paso 5/5: Activando servicio..."
enable_service

# ──────────────────────────────────────────────
# Resumen final
# ──────────────────────────────────────────────
echo ""
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}  ✅ Instalación completada${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo "  Binario:       ${BIN_DIR}/${BIN_NAME}"
echo "  Configuración: ${CONFIG_FILE}"
echo "  Servicio:      ${SERVICE_FILE}"
echo ""
echo "  Comandos útiles:"
echo "    systemctl status ${SERVICE_NAME}"
echo "    journalctl -u ${SERVICE_NAME} -f"
echo "    systemctl restart ${SERVICE_NAME}"
echo ""
