#!/usr/bin/env bash
# Local development lab for AutoHost: disposable Incus test servers and a real node on the LAN.
#
# Usage: scripts/lab.sh <command> [args]   (normally through `make lab-*` / `make node-*`)
#
# Configuration comes from the environment, falling back to lab.env next to the Makefile
# (copy lab.env.example). Nothing personal is hard-coded here.
set -euo pipefail

AGENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE_DIR="$(cd "$AGENT_DIR/.." && pwd)"
CLI_DIR="${CLI_DIR:-$WORKSPACE_DIR/autohost-cli}"
API_DIR="${API_DIR:-$WORKSPACE_DIR/autohost-cloud-api}"

# Load lab.env without overriding variables already set (e.g. `make lab-create NAME=foo`).
if [[ -f "$AGENT_DIR/lab.env" ]]; then
	while IFS='=' read -r key value; do
		[[ "$key" =~ ^[A-Z_][A-Z0-9_]*$ ]] || continue
		[[ -n "${!key:-}" ]] && continue
		value="${value%%#*}"                                      # inline comment
		value="${value%"${value##*[![:space:]]}"}"                # trailing spaces
		value="${value%\"}"; value="${value#\"}"
		export "$key=$value"
	done <"$AGENT_DIR/lab.env"
fi

DEFAULT_NAME="autohost-test"
NAME="${NAME:-}"
RAM="${RAM:-512MiB}"
CPU="${CPU:-1}"
IMAGE="${IMAGE:-images:ubuntu/24.04}"
CLOUD_URL="${CLOUD_URL:-http://localhost:3000}"
API_PORT="${API_PORT:-8080}"
GRPC_PORT="${GRPC_PORT:-9090}"

AGENT_BIN="$AGENT_DIR/autohost-agent"
CLI_BIN="$CLI_DIR/autohost"

# ─── Helpers ─────────────────────────────────────────────────────────────────

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

require() { command -v "$1" >/dev/null 2>&1 || die "'$1' is required"; }

build_cli() {
	info "Building autohost-cli"
	(cd "$CLI_DIR" && go build -ldflags "-s -w" -o autohost main.go)
}

instance_exists() { incus info "$1" >/dev/null 2>&1; }

# pick_instances [--allow-all]: prints the target instance names, one per line.
# Uses $NAME when set ("all" = every instance), the only instance when there is one, or asks interactively.
pick_instances() {
	local allow_all="${1:-}"
	mapfile -t instances < <(incus list -c n --format csv)
	((${#instances[@]})) || die "No Incus instances. Create one with: make lab-create"

	if [[ -n "$NAME" ]]; then
		if [[ "$NAME" == "all" && -n "$allow_all" ]]; then printf '%s\n' "${instances[@]}"; return; fi
		instance_exists "$NAME" || die "Instance '$NAME' not found"
		echo "$NAME"; return
	fi
	if ((${#instances[@]} == 1)); then echo "${instances[0]}"; return; fi

	{
		echo "Incus instances:"
		[[ -n "$allow_all" ]] && printf '  [\033[1;33m0\033[0m] all instances\n'
		for i in "${!instances[@]}"; do
			local state ip
			state=$(incus list "${instances[$i]}" -c s --format csv)
			ip=$(incus list "${instances[$i]}" -c 4 --format csv | head -n1)
			printf '  [\033[1;32m%d\033[0m] %-20s (%s, %s)\n' "$((i + 1))" "${instances[$i]}" "$state" "$ip"
		done
		printf 'Select [%s-%d]: ' "$([[ -n "$allow_all" ]] && echo 0 || echo 1)" "${#instances[@]}"
	} >&2
	local choice
	read -r choice
	if [[ -n "$allow_all" && "$choice" == "0" ]]; then printf '%s\n' "${instances[@]}"; return; fi
	[[ "$choice" =~ ^[0-9]+$ ]] && ((choice >= 1 && choice <= ${#instances[@]})) || die "Invalid selection"
	echo "${instances[$((choice - 1))]}"
}

pick_one() { pick_instances | head -n1; }

# host_gateway <instance>: the host's address as seen from inside the instance.
host_gateway() { incus exec "$1" -- sh -c "ip route show default" | awk '/default/{print $3}' | head -1; }

push_bin() { # push_bin <instance> <local> <remote>
	incus file push "$2" "$1/tmp/$(basename "$3")" --mode=0755
	incus exec "$1" -- mv -f "/tmp/$(basename "$3")" "$3"
}

# ─── Incus test servers ──────────────────────────────────────────────────────

cmd_setup() {
	if ! command -v incus >/dev/null; then
		info "Installing Incus"
		sudo apt-get update -qq && sudo apt-get install -y incus
	fi
	incus info >/dev/null 2>&1 || echo "{}" | sudo incus admin init --preseed
	if ! id -nG "$(whoami)" | grep -qw incus; then
		sudo usermod -aG incus "$(whoami)"
		ok "Added $(whoami) to the incus group (run: newgrp incus)"
	fi
	ok "Incus ready: $(incus --version)"
}

cmd_create() {
	local name="${NAME:-$DEFAULT_NAME}"
	if instance_exists "$name"; then ok "Instance '$name' already exists"; return; fi
	info "Creating '$name' ($RAM RAM, $CPU CPU)"
	incus launch "$IMAGE" "$name" -c limits.memory="$RAM" -c limits.cpu="$CPU" -c security.nesting=true
	sleep 4
	info "Installing Docker and tools in '$name'"
	incus exec "$name" -- apt-get update -qq
	incus exec "$name" -- apt-get install -y -qq docker.io docker-compose-v2 curl wireguard-tools
	ok "Instance '$name' ready"
}

cmd_delete() {
	local name
	name=$(pick_one)
	info "Deleting '$name'"
	incus delete "$name" --force
	ok "Deleted '$name'"
}

cmd_shell() {
	local name
	name=$(pick_one)
	exec incus exec "$name" -- bash
}

# deploy: install the agent (binary, system user, empty config, systemd unit) in an instance.
cmd_deploy() {
	local name="${NAME:-$DEFAULT_NAME}"
	[[ -x "$AGENT_BIN" ]] || die "Build the agent first (make build)"
	instance_exists "$name" || NAME="$name" cmd_create
	info "Deploying autohost-agent to '$name'"
	incus exec "$name" -- sh -c 'id -u autohost >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin autohost'
	incus exec "$name" -- usermod -aG docker autohost || true
	incus exec "$name" -- mkdir -p /etc/autohost /var/lib/autohost
	incus exec "$name" -- chown autohost:autohost /var/lib/autohost
	push_bin "$name" "$AGENT_BIN" /usr/local/bin/autohost-agent
	if ! incus exec "$name" -- test -f /etc/autohost/config.yaml; then
		incus file push "$AGENT_DIR/configs/agent.yaml" "$name/etc/autohost/config.yaml" --mode=0640
		incus exec "$name" -- chown root:autohost /etc/autohost/config.yaml
	fi
	incus file push "$AGENT_DIR/autohost-agent.service" "$name/etc/systemd/system/autohost-agent.service" --mode=0644
	incus exec "$name" -- systemctl daemon-reload
	ok "Agent installed in '$name' (enroll it with: make lab-enroll NAME=$name)"
}

# update: push fresh agent + CLI binaries to one instance, all of them, or a picked one, and restart.
cmd_update() {
	[[ -x "$AGENT_BIN" ]] || die "Build the agent first (make build)"
	build_cli
	local name
	while read -r name; do
		info "Updating '$name'"
		push_bin "$name" "$AGENT_BIN" /usr/local/bin/autohost-agent
		push_bin "$name" "$CLI_BIN" /usr/local/bin/autohost
		incus exec "$name" -- systemctl restart autohost-agent
		ok "'$name' updated"
	done < <(pick_instances --allow-all)
}

cmd_service() { # start|stop|restart|status|logs
	local name
	name=$(pick_one)
	case "$1" in
	start) incus exec "$name" -- systemctl enable --now autohost-agent ;;
	stop | restart) incus exec "$name" -- systemctl "$1" autohost-agent ;;
	status) incus exec "$name" -- systemctl status autohost-agent --no-pager ;;
	logs) exec incus exec "$name" -- journalctl -u autohost-agent -f ;;
	esac
}

# enroll: interactive `autohost up` (browser authorization) inside an instance.
cmd_enroll() {
	local name="${NAME:-$DEFAULT_NAME}"
	cmd_deploy
	build_cli
	push_bin "$name" "$CLI_BIN" /usr/local/bin/autohost
	local gw
	gw=$(host_gateway "$name")
	info "Running 'autohost up' in '$name' (API at http://$gw:$API_PORT)"
	incus exec "$name" -- env AUTOHOST_CLOUD_URL="$CLOUD_URL" AUTOHOST_API_URL="http://$gw:$API_PORT" autohost up
}

# link: non-interactive enrollment with an enrollment token from the dashboard.
cmd_link() {
	local name="${NAME:-$DEFAULT_NAME}"
	[[ -n "${TOKEN:-}" ]] || die "Pass the enrollment token: make lab-link TOKEN=<token>"
	cmd_deploy
	build_cli
	push_bin "$name" "$CLI_BIN" /usr/local/bin/autohost
	local gw
	gw=$(host_gateway "$name")
	info "Linking '$name' to http://$gw:$API_PORT"
	incus exec "$name" -- autohost enroll link --api "http://$gw:$API_PORT" --token "$TOKEN" --name "$name"
	incus exec "$name" -- systemctl restart autohost-agent
	ok "'$name' linked to AutoHost"
}

# ─── Real node on the LAN (over SSH) ─────────────────────────────────────────

require_node() {
	[[ -n "${NODE_HOST:-}" ]] || die "Set NODE_HOST (user@ip) in lab.env or the environment"
}

cmd_node_update() {
	require_node
	[[ -x "$AGENT_BIN" ]] || die "Build the agent first (make build)"
	build_cli
	info "Copying binaries to $NODE_HOST"
	scp -q "$CLI_BIN" "$NODE_HOST:/tmp/autohost"
	scp -q "$AGENT_BIN" "$NODE_HOST:/tmp/autohost-agent"
	ssh "$NODE_HOST" "sudo install -m 0755 /tmp/autohost /usr/local/bin/autohost && \
		sudo install -m 0755 /tmp/autohost-agent /usr/local/bin/autohost-agent && \
		rm -f /tmp/autohost /tmp/autohost-agent && sudo systemctl restart autohost-agent"
	ssh "$NODE_HOST" "sudo systemctl status autohost-agent --no-pager | head -n 12"
	ok "Binaries updated on $NODE_HOST"
}

# new_enroll_token: creates a 24h enrollment token in the local API database for the first admin (or
# ENROLL_USER_EMAIL) and prints the plaintext token.
new_enroll_token() {
	require psql
	require openssl
	if [[ -z "${DATABASE_URL:-}" && -f "$API_DIR/.env" ]]; then
		DATABASE_URL=$(grep -E '^DATABASE_URL=' "$API_DIR/.env" | head -1 | cut -d= -f2- | tr -d '"')
	fi
	[[ -n "${DATABASE_URL:-}" ]] || die "Set DATABASE_URL (or keep it in $API_DIR/.env)"

	local plain hash user_filter
	plain="autohost-enr_$(openssl rand 32 | base64 | tr '+/' '-_' | tr -d '=\n')"
	hash=$(printf '%s' "$plain" | sha256sum | cut -d' ' -f1)
	user_filter="role = 'admin' AND is_active"
	if [[ -n "${ENROLL_USER_EMAIL:-}" ]]; then user_filter="email = :'email'"; fi

	local inserted
	inserted=$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -v email="${ENROLL_USER_EMAIL:-}" -Atq <<SQL
INSERT INTO enroll_tokens (token, user_id, expires_at)
SELECT '$hash', id, now() + interval '24 hours'
FROM users WHERE $user_filter ORDER BY created_at LIMIT 1
RETURNING 1;
SQL
	)
	[[ "$inserted" == "1" ]] || die "No matching user to own the enrollment token"
	echo "$plain"
}

cmd_node_setup() {
	require_node
	[[ -n "${HOST_IP:-}" ]] || die "Set HOST_IP (this machine's address as seen from the node) in lab.env"
	cmd_node_update
	local name="${NODE_NAME:-${NODE_HOST#*@}}" token
	token=$(new_enroll_token)
	info "Enrolling '$name' against http://$HOST_IP:$API_PORT"
	ssh "$NODE_HOST" "sudo autohost enroll link --api http://$HOST_IP:$API_PORT --token $token --name $name && \
		sudo sed -i 's|grpc_address:.*|grpc_address: \"$HOST_IP:$GRPC_PORT\"|' /etc/autohost/config.yaml && \
		sudo systemctl daemon-reload && sudo systemctl enable --now autohost-agent && sudo systemctl restart autohost-agent"
	ok "Node '$name' connected (dashboard: $CLOUD_URL)"
}

cmd_node_hypervisor() {
	require_node
	info "Installing KVM/libvirt on $NODE_HOST"
	ssh "$NODE_HOST" "yes | sudo autohost setup hypervisor && sudo usermod -aG libvirt,kvm autohost && \
		sudo sed -i 's|SupplementaryGroups=docker.*|SupplementaryGroups=docker libvirt kvm|' /etc/systemd/system/autohost-agent.service && \
		sudo systemctl daemon-reload && sudo systemctl restart autohost-agent"
	ok "Hypervisor ready on $NODE_HOST"
}

# ─── Dispatch ────────────────────────────────────────────────────────────────

cmd="${1:-}"
shift || true
case "$cmd" in
setup) cmd_setup ;;
create) require incus && cmd_create ;;
delete) require incus && cmd_delete ;;
list) require incus && incus list ;;
shell) require incus && cmd_shell ;;
deploy) require incus && cmd_deploy ;;
update) require incus && cmd_update ;;
start | stop | restart | status | logs) require incus && cmd_service "$cmd" ;;
enroll) require incus && cmd_enroll ;;
link) require incus && cmd_link ;;
node-update) cmd_node_update ;;
node-setup) cmd_node_setup ;;
node-hypervisor) cmd_node_hypervisor ;;
node-status) require_node && ssh "$NODE_HOST" "sudo systemctl status autohost-agent --no-pager" ;;
node-logs) require_node && exec ssh "$NODE_HOST" "sudo journalctl -u autohost-agent -f" ;;
node-shell) require_node && exec ssh -t "$NODE_HOST" "bash -l" ;;
*) die "Unknown command '$cmd'. See: make help" ;;
esac
