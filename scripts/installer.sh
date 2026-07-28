#!/bin/sh
set -eu

PROGRAM="sylve-installer"
PROGRAM_VERSION="0.2.1"

# We should Flip this to 1 when pkg/port is ready and want this script
# to automatically use pkg instead of GitHub release binaries.
AUTO_SWITCH_TO_PKG=0

REPO_OWNER="AlchemillaHQ"
REPO_NAME="Sylve"
API_BASE="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}"

BIN_PATH="/usr/local/sbin/sylve"
RC_SCRIPT_PATH="/usr/local/etc/rc.d/sylve"
CONFIG_PATH="/usr/local/etc/sylve/config.json"
DEFAULT_DATA_PATH="/var/db/sylve"

YES_MODE=0
FORCE_MODE=0
DATA_PATH=""
SUBCOMMAND="install"
SUBCOMMAND_SET=0

ARCH=""
OS_RELEASE=""
RELEASE_TAG=""
ASSET_NAME=""
ASSET_URL=""
ASSET_DIGEST=""
GENERATED_ADMIN_PASSWORD=""

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	C_RESET="$(printf '\033[0m')"
	C_RED="$(printf '\033[31m')"
	C_GREEN="$(printf '\033[32m')"
	C_YELLOW="$(printf '\033[33m')"
	C_BLUE="$(printf '\033[34m')"
else
	C_RESET=""
	C_RED=""
	C_GREEN=""
	C_YELLOW=""
	C_BLUE=""
fi

ascii_art() {
	cat <<'EOF' | sed "s/@VERSION@/${PROGRAM_VERSION}/"
   _____       __         
  / ___/__  __/ /   _____ 
  \__ \/ / / / / | / / _ \
 ___/ / /_/ / /| |/ /  __/
/____/\__, /_/ |___/\___/ 
     /____/         Installer v@VERSION@

EOF
}

log_info() {
	printf "%s[INFO]%s %s\n" "$C_BLUE" "$C_RESET" "$*"
}

log_warn() {
	printf "%s[WARN]%s %s\n" "$C_YELLOW" "$C_RESET" "$*" >&2
}

log_error() {
	printf "%s[ERROR]%s %s\n" "$C_RED" "$C_RESET" "$*" >&2
}

log_ok() {
	printf "%s[OK]%s %s\n" "$C_GREEN" "$C_RESET" "$*"
}

die() {
	log_error "$*"
	exit 1
}

usage() {
	cat <<EOF
${PROGRAM} v${PROGRAM_VERSION}

Usage:
  $PROGRAM [install|update] [flags]

Subcommands:
  install   Install Sylve and write/update config.json (default)
  update    Update Sylve binary

Flags:
  --yes                Non-interactive mode
  --data-path <path>   Data path for install config (default: ${DEFAULT_DATA_PATH})
  --force              Force update even if tag matches installed metadata
  -h, --help           Show this help message

Notes:
  - AUTO_SWITCH_TO_PKG is currently set to ${AUTO_SWITCH_TO_PKG}.
  - When AUTO_SWITCH_TO_PKG=1, install/update uses pkg install/upgrade sylve.
EOF
}

have_cmd() {
	command -v "$1" >/dev/null 2>&1
}

require_root() {
	if [ "$(id -u)" -ne 0 ]; then
		die "Root privileges are required. Re-run as root/doas/sudo."
	fi
}

ask_input_default() {
	prompt="$1"
	default_value="$2"

	if [ "$YES_MODE" -eq 1 ]; then
		printf '%s\n' "$default_value"
		return 0
	fi

	if [ ! -r /dev/tty ]; then
		log_warn "No tty available; using default: ${default_value}"
		printf '%s\n' "$default_value"
		return 0
	fi

	printf "%s [%s]: " "$prompt" "$default_value" > /dev/tty
	IFS= read -r answer < /dev/tty || answer=""
	if [ -z "$answer" ]; then
		printf '%s\n' "$default_value"
	else
		printf '%s\n' "$answer"
	fi
}

ask_yes_no() {
	question="$1"
	default_answer="$2"

	if [ "$YES_MODE" -eq 1 ]; then
		if [ "$default_answer" = "yes" ]; then
			log_info "$question -> yes (--yes default)"
			return 0
		fi
		log_info "$question -> no (--yes default)"
		return 1
	fi

	if [ ! -r /dev/tty ]; then
		if [ "$default_answer" = "yes" ]; then
			log_warn "$question -> yes (no tty, default)"
			return 0
		fi
		log_warn "$question -> no (no tty, default)"
		return 1
	fi

	if [ "$default_answer" = "yes" ]; then
		suffix="[Y/n]"
	else
		suffix="[y/N]"
	fi

	while :; do
		printf "%s %s " "$question" "$suffix" > /dev/tty
		IFS= read -r reply < /dev/tty || reply=""
		reply_lc=$(printf '%s' "$reply" | tr '[:upper:]' '[:lower:]')
		case "$reply_lc" in
			y|yes) return 0 ;;
			n|no) return 1 ;;
			"")
				if [ "$default_answer" = "yes" ]; then
					return 0
				fi
				return 1
				;;
			*) printf "Please answer yes or no.\n" > /dev/tty ;;
		esac
	done
}

generate_random_password() {
	if have_cmd uuidgen; then
		uuidgen | tr -d '-' | tr '[:upper:]' '[:lower:]'
		return 0
	fi

	if have_cmd openssl; then
		openssl rand -hex 16
		return 0
	fi

	# Fallback (still non-static): timestamp + pid.
	printf 'sylve-%s-%s' "$(date +%s)" "$$"
}

validate_platform_and_arch() {
	os_name="$(uname -s 2>/dev/null || printf 'unknown')"
	if [ "$os_name" != "FreeBSD" ]; then
		die "Unsupported OS: $os_name. This script supports FreeBSD only."
	fi

	OS_RELEASE="$(uname -r 2>/dev/null || printf '')"
	os_major="$(printf '%s' "$OS_RELEASE" | awk -F'[.-]' '{print $1}')"
	os_minor="$(printf '%s' "$OS_RELEASE" | awk -F'[.-]' '{print $2}')"

	case "$os_major" in
		''|*[!0-9]*)
			die "Unable to parse FreeBSD release from '$OS_RELEASE'"
			;;
	esac
	case "$os_minor" in
		''|*[!0-9]*)
			os_minor=0
			;;
	esac

	if [ "$os_major" -lt 15 ]; then
		die "FreeBSD >= 15 is required (found: $OS_RELEASE)."
	fi

	machine_arch="$(uname -m 2>/dev/null || printf '')"
	case "$machine_arch" in
		amd64|x86_64) ARCH="amd64" ;;
		arm64|aarch64) ARCH="arm64" ;;
		*) die "Unsupported architecture: ${machine_arch}" ;;
	esac
}

http_get() {
	url="$1"
	if have_cmd fetch; then
		fetch -q -o - "$url"
		return 0
	fi
	if have_cmd curl; then
		curl -fsSL \
			-H "Accept: application/vnd.github+json" \
			-H "X-GitHub-Api-Version: 2022-11-28" \
			"$url"
		return 0
	fi
	die "Neither fetch nor curl is available."
}

download_file() {
	url="$1"
	dst="$2"
	if have_cmd fetch; then
		fetch -q -o "$dst" "$url"
		return 0
	fi
	if have_cmd curl; then
		curl -fsSL -o "$dst" "$url"
		return 0
	fi
	die "Neither fetch nor curl is available."
}

sha256_file() {
	target="$1"
	if have_cmd sha256; then
		sha256 -q "$target"
		return 0
	fi
	if have_cmd shasum; then
		shasum -a 256 "$target" | awk '{print $1}'
		return 0
	fi
	if have_cmd openssl; then
		openssl dgst -sha256 "$target" | awk '{print $2}'
		return 0
	fi
	die "No SHA-256 tool found."
}

json_release_tag() {
	printf '%s' "$1" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

json_error_message() {
	printf '%s' "$1" | sed -n 's/.*"message"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

json_asset_metadata_with_jq() {
	json_payload="$1"
	asset_name="$2"
	printf '%s\n' "$json_payload" | jq -r --arg n "$asset_name" '
		.assets[]? | select(.name == $n) |
		((.digest // "" | sub("^sha256:"; "")) + "\t" + (.browser_download_url // ""))
	' | head -n 1
}

json_asset_metadata_plain() {
	json_payload="$1"
	asset_name="$2"

	# Normalize compact JSON into line-oriented tokens so we can parse without jq.
	normalized_json="$(printf '%s' "$json_payload" | sed 's/[{}]/\n&\n/g; s/,/\n/g')"

	printf '%s\n' "$normalized_json" | awk -v asset="$asset_name" '
	BEGIN { in_assets=0; hit=0; digest=""; url="" }
	/"assets"[[:space:]]*:[[:space:]]*\[/ { in_assets=1; next }
	in_assets == 1 {
		if (hit == 0 && $0 ~ ("\"name\"[[:space:]]*:[[:space:]]*\"" asset "\"")) {
			hit=1; digest=""; url=""; next
		}
		if (hit == 1) {
			if ($0 ~ /"digest"[[:space:]]*:[[:space:]]*"sha256:/) {
				line=$0
				sub(/.*"digest"[[:space:]]*:[[:space:]]*"sha256:/, "", line)
				sub(/".*/, "", line)
				digest=line
			}
			if ($0 ~ /"browser_download_url"[[:space:]]*:[[:space:]]*"/) {
				line=$0
				sub(/.*"browser_download_url"[[:space:]]*:[[:space:]]*"/, "", line)
				sub(/".*/, "", line)
				url=line
			}
			if (digest != "" && url != "") { print digest "\t" url; exit }
			if ($0 ~ /"name"[[:space:]]*:[[:space:]]*"sylve-/ && $0 !~ ("\"name\"[[:space:]]*:[[:space:]]*\"" asset "\"")) {
				hit=0; digest=""; url=""
			}
		}
	}
	'
}

resolve_latest_release() {
	release_url="${API_BASE}/releases/latest"
	log_info "Resolving latest release metadata..."

	release_json="$(http_get "$release_url" 2>/dev/null || true)"
	[ -n "$release_json" ] || die "Failed to fetch release metadata."

	RELEASE_TAG="$(json_release_tag "$release_json")"
	if [ -z "$RELEASE_TAG" ]; then
		api_message="$(json_error_message "$release_json")"
		if [ -n "$api_message" ]; then
			die "Failed to parse release tag. GitHub API message: ${api_message}"
		fi
		die "Failed to parse release tag from API response."
	fi

	ASSET_NAME="sylve-${ARCH}"
	if have_cmd jq; then
		asset_meta="$(json_asset_metadata_with_jq "$release_json" "$ASSET_NAME")"
	else
		asset_meta="$(json_asset_metadata_plain "$release_json" "$ASSET_NAME")"
	fi

	[ -n "$asset_meta" ] || die "Release asset '${ASSET_NAME}' not found in ${RELEASE_TAG}."

	ASSET_DIGEST="$(printf '%s' "$asset_meta" | awk -F '\t' '{print $1}')"
	ASSET_URL="$(printf '%s' "$asset_meta" | awk -F '\t' '{print $2}')"
	[ -n "$ASSET_DIGEST" ] || die "Missing asset digest."
	[ -n "$ASSET_URL" ] || die "Missing asset URL."
}

extract_version_tag() {
	printf '%s\n' "$1" | sed -n 's/.*\(v[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -n 1
}

detect_installed_binary_tag() {
	if [ ! -x "$BIN_PATH" ]; then
		return 0
	fi

	# Use -h for compatibility with older releases (for example v0.1.x).
	out="$("$BIN_PATH" -h 2>&1 || true)"
	tag="$(extract_version_tag "$out")"
	if [ -n "$tag" ]; then
		printf '%s\n' "$tag"
		return 0
	fi

	return 0
}

mktemp_file() {
	if tempf="$(mktemp -t sylve-installer 2>/dev/null)"; then
		printf '%s\n' "$tempf"
		return 0
	fi
	mktemp "/tmp/sylve-installer.XXXXXX"
}

install_binary_from_release() {
	resolve_latest_release

	local_tag="$(detect_installed_binary_tag)"

	if [ -n "$local_tag" ] && [ "$local_tag" = "$RELEASE_TAG" ] && [ "$FORCE_MODE" -eq 0 ] && [ -x "$BIN_PATH" ]; then
		log_ok "Already at latest tag ${RELEASE_TAG}."
		return 0
	fi

	log_info "Downloading ${ASSET_NAME} (${RELEASE_TAG})..."
	tmp_bin="$(mktemp_file)"
	trap 'rm -f "$tmp_bin"' EXIT INT TERM HUP

	download_file "$ASSET_URL" "$tmp_bin" || die "Download failed."

	actual_digest="$(sha256_file "$tmp_bin")"
	if [ "$actual_digest" != "$ASSET_DIGEST" ]; then
		die "Digest mismatch. expected=${ASSET_DIGEST} actual=${actual_digest}"
	fi
	log_ok "Digest verified."

	mkdir -p "$(dirname "$BIN_PATH")"
	if [ -f "$BIN_PATH" ]; then
		backup_path="${BIN_PATH}.bak.$(date -u '+%Y%m%d%H%M%S')"
		cp -p "$BIN_PATH" "$backup_path"
		log_info "Backed up existing binary to ${backup_path}"
	fi

	install -m 755 "$tmp_bin" "$BIN_PATH"
	log_ok "Installed ${BIN_PATH} (${RELEASE_TAG})"

	rm -f "$tmp_bin"
	trap - EXIT INT TERM HUP
}

pkg_install_or_update() {
	if ! have_cmd pkg; then
		die "'pkg' command not found."
	fi

	if pkg info -e sylve >/dev/null 2>&1; then
		log_info "Updating pkg-installed sylve..."
		ASSUME_ALWAYS_YES=yes pkg upgrade -y sylve
	else
		log_info "Installing sylve from pkg..."
		ASSUME_ALWAYS_YES=yes pkg install -y sylve
	fi

	log_ok "pkg operation complete."
}

detect_samba_package() {
	if ! have_cmd pkg; then
		printf "samba423\n"
		return 0
	fi

	installed_samba="$(pkg info 2>/dev/null | awk '/^samba4[0-9][0-9][0-9]*-/ { sub(/-.*/, "", $1); print $1; exit }')"
	if [ -n "$installed_samba" ]; then
		printf "%s\n" "$installed_samba"
		return 0
	fi

	samba_pkg="$(pkg search -q '^samba4[0-9][0-9][0-9]*-' 2>/dev/null | sed 's/-.*//' | sort -r | head -n 1 || true)"
	if [ -n "$samba_pkg" ]; then
		printf "%s\n" "$samba_pkg"
		return 0
	fi

	printf "samba423\n"
}

maybe_install_recommended_dependencies() {
	if ! have_cmd pkg; then
		log_warn "'pkg' command not found; skipping dependency installation."
		return 0
	fi

	if ask_yes_no "Install recommended dependencies (libvirt, bhyve-firmware, swtpm, qemu-tools, samba4XX, dnsmasq)?" "yes"; then
		samba_pkg="$(detect_samba_package)"
		deps="libvirt bhyve-firmware swtpm qemu-tools dnsmasq ${samba_pkg}"
		log_info "Installing dependencies: ${deps}"
		ASSUME_ALWAYS_YES=yes pkg install -y $deps
		log_ok "Recommended dependencies installed."
	else
		log_info "Skipped recommended dependency installation."
	fi
}

write_default_config() {
	mkdir -p "$(dirname "$CONFIG_PATH")"
	if [ -z "$GENERATED_ADMIN_PASSWORD" ]; then
		GENERATED_ADMIN_PASSWORD="$(generate_random_password)"
	fi
	cat > "$CONFIG_PATH" <<EOF
{
  "environment": "production",
  "proxyToVite": false,
  "dataPath": "${DATA_PATH}",
  "auth": {
    "enablePAM": true
  },
  "admin": {
    "email": "admin@sylve.local",
    "password": "${GENERATED_ADMIN_PASSWORD}"
  },
  "logLevel": 3,
  "port": 8181,
  "httpPort": 8182,
  "raft": {
    "reset": false
  },
  "btt": {
    "rpc": {
      "enabled": false,
      "host": "127.0.0.1",
      "port": 6890
    },
    "dht": {
      "enabled": true,
      "port": 7246
    }
  }
}
EOF
}

install_rc_script_if_missing() {
	if [ -f "$RC_SCRIPT_PATH" ]; then
		log_info "rc script already exists at ${RC_SCRIPT_PATH}; keeping it."
		return 0
	fi

	mkdir -p "$(dirname "$RC_SCRIPT_PATH")"
	cat > "$RC_SCRIPT_PATH" <<EOF
#!/bin/sh
#
# PROVIDE: sylve
# REQUIRE: DAEMON NETWORKING
# KEYWORD: shutdown
#
# Add the following lines to /etc/rc.conf.local or /etc/rc.conf to enable sylve:
#
# sylve_enable (bool):      Set to "NO" by default.
#                           Set it to "YES" to enable sylve.
# sylve_user (user):        Set to "root" by default.
#                           User to run sylve as.
# sylve_group (group):      Set to "wheel" by default.
#                           Group to run sylve as.
# sylve_args (str):         Set to "-config ${CONFIG_PATH}" by default.
#                           Extra flags passed to sylve.

. /etc/rc.subr

name=sylve
rcvar=sylve_enable

load_rc_config \$name

: \${sylve_enable:="NO"}
: \${sylve_user:="root"}
: \${sylve_group:="wheel"}
: \${sylve_args:="-config ${CONFIG_PATH}"}

export PATH="\${PATH}:/usr/local/bin:/usr/local/sbin"

pidfile="/var/run/\${name}.pid"
daemon_pidfile="/var/run/\${name}-daemon.pid"
procname="${BIN_PATH}"
command="/usr/sbin/daemon"
command_args="-f -c -R 5 -r -T \${name} -p \${pidfile} -P \${daemon_pidfile} \${procname} \${sylve_args}"

start_precmd=sylve_startprecmd
stop_postcmd=sylve_stoppostcmd

sylve_startprecmd()
{
	if [ ! -e \${daemon_pidfile} ]; then
		install -o \${sylve_user} -g \${sylve_group} /dev/null \${daemon_pidfile}
	fi
	if [ ! -e \${pidfile} ]; then
		install -o \${sylve_user} -g \${sylve_group} /dev/null \${pidfile}
	fi
}

sylve_stoppostcmd()
{
	if [ -f "\${daemon_pidfile}" ]; then
		pids=\$(pgrep -F \${daemon_pidfile} 2>&1)
		_err=\$?
		[ \${_err} -eq 0 ] && kill -9 \${pids}
	fi
}

run_rc_command "\$1"
EOF

	chmod 755 "$RC_SCRIPT_PATH"
	log_ok "Installed rc script at ${RC_SCRIPT_PATH}"
}

set_config_datapath() {
	[ -f "$CONFIG_PATH" ] || die "Config file not found: ${CONFIG_PATH}"

	if have_cmd jq; then
		tmp_json="$(mktemp_file)"
		jq --arg p "$DATA_PATH" '.dataPath = $p' "$CONFIG_PATH" > "$tmp_json"
		mv "$tmp_json" "$CONFIG_PATH"
		return 0
	fi

	if grep -q '"dataPath"[[:space:]]*:' "$CONFIG_PATH"; then
		escaped_path="$(printf '%s' "$DATA_PATH" | sed 's/[\/&]/\\&/g')"
		tmp_json="$(mktemp_file)"
		sed "s/\"dataPath\"[[:space:]]*:[[:space:]]*\"[^\"]*\"/\"dataPath\": \"${escaped_path}\"/" "$CONFIG_PATH" > "$tmp_json"
		mv "$tmp_json" "$CONFIG_PATH"
		return 0
	fi

	tmp_json="$(mktemp_file)"
	awk -v p="$DATA_PATH" '
	BEGIN { inserted = 0 }
	{
		if (inserted == 0) {
			idx = index($0, "{")
			if (idx > 0) {
				prefix = substr($0, 1, idx)
				suffix = substr($0, idx + 1)
				print prefix
				print "  \"dataPath\": \"" p "\","
				if (length(suffix) > 0) print suffix
				inserted = 1
				next
			}
		}
		print
	}
	' "$CONFIG_PATH" > "$tmp_json"
	mv "$tmp_json" "$CONFIG_PATH"
}

configure_install_data_path() {
	if [ -z "$DATA_PATH" ]; then
		DATA_PATH="$(ask_input_default "Sylve data directory" "$DEFAULT_DATA_PATH")"
	fi

	[ -n "$DATA_PATH" ] || die "Data path cannot be empty."

	if [ -f "$CONFIG_PATH" ]; then
		log_info "Config exists at ${CONFIG_PATH}; updating only dataPath."
		set_config_datapath
	else
		log_info "Creating config at ${CONFIG_PATH}."
		write_default_config
	fi

	log_ok "Config ready with dataPath='${DATA_PATH}'."
}

maybe_enable_and_start_sylve() {
	if [ ! -f "$RC_SCRIPT_PATH" ]; then
		log_warn "rc script '${RC_SCRIPT_PATH}' not found; skipping service enable/start."
		return 0
	fi

	if ! have_cmd sysrc; then
		log_warn "'sysrc' not found; cannot enable sylve in rc.conf automatically."
		return 0
	fi

	if ! have_cmd service; then
		log_warn "'service' command not found; cannot start sylve automatically."
		return 0
	fi

	if ask_yes_no "Enable Sylve service and start it now?" "yes"; then
		sysrc sylve_enable=YES
		if service sylve onestatus >/dev/null 2>&1; then
			if service sylve restart; then
				log_ok "Sylve service restarted."
			else
				log_warn "Failed to restart Sylve service. Please check service logs."
			fi
		else
			if service sylve start; then
				log_ok "Sylve service started."
			else
				log_warn "Failed to start Sylve service. Please check service logs."
			fi
		fi
	else
		log_info "Skipped enabling/starting Sylve service."
	fi
}

get_https_port_from_config() {
	if [ ! -f "$CONFIG_PATH" ]; then
		printf "8181\n"
		return 0
	fi

	port="$(sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$CONFIG_PATH" | head -n 1)"
	if [ -n "$port" ]; then
		printf "%s\n" "$port"
		return 0
	fi

	printf "8181\n"
}

show_web_ui_urls() {
	if ! have_cmd ifconfig; then
		return 0
	fi

	https_port="$(get_https_port_from_config)"
	if [ "$https_port" = "0" ]; then
		return 0
	fi

	ip_list="$(ifconfig -a 2>/dev/null | awk '
		/^[A-Za-z0-9].*:$/ { iface=$1; sub(/:$/, "", iface) }
		$1 == "inet" {
			ip=$2
			if (ip !~ /^127\./) print ip
		}
		$1 == "inet6" {
			ip=$2
			sub(/%.*/, "", ip)
			if (ip != "::1" && ip !~ /^fe80:/) print "[" ip "]"
		}
	' | sort -u)"

	if [ -z "$ip_list" ]; then
		return 0
	fi

	printf "\nAccess Web UI at:\n"
	printf '%s\n' "$ip_list" | while IFS= read -r ip; do
		[ -n "$ip" ] || continue
		printf "https://%s:%s\n" "$ip" "$https_port"
	done
	printf "\n"
}

command_install() {
	require_root
	validate_platform_and_arch

	if [ "$AUTO_SWITCH_TO_PKG" -eq 1 ]; then
		log_info "AUTO_SWITCH_TO_PKG=1 -> using pkg install path."
		pkg_install_or_update
	else
		install_binary_from_release
	fi

	configure_install_data_path
	maybe_install_recommended_dependencies
	install_rc_script_if_missing
	maybe_enable_and_start_sylve

	if [ -n "$GENERATED_ADMIN_PASSWORD" ]; then
		printf "\n"
		log_ok "Generated admin password has been written to ${CONFIG_PATH}"
		printf "Admin username: admin\n"
		printf "Admin password: %s\n" "$GENERATED_ADMIN_PASSWORD"
		printf "\n"
	fi

	show_web_ui_urls
	log_ok "Install completed."
}

command_update() {
	require_root
	validate_platform_and_arch

	if [ "$AUTO_SWITCH_TO_PKG" -eq 1 ]; then
		log_info "AUTO_SWITCH_TO_PKG=1 -> using pkg update path."
		pkg_install_or_update
		return 0
	fi

	install_binary_from_release
	log_ok "Update completed."
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
			install|update)
				if [ "$SUBCOMMAND_SET" -eq 1 ]; then
					die "Only one subcommand can be used."
				fi
				SUBCOMMAND="$1"
				SUBCOMMAND_SET=1
				shift
				;;
			--yes)
				YES_MODE=1
				shift
				;;
			--force)
				FORCE_MODE=1
				shift
				;;
			--data-path=*)
				DATA_PATH="${1#*=}"
				shift
				;;
			--data-path)
				[ $# -ge 2 ] || die "--data-path requires a value."
				DATA_PATH="$2"
				shift 2
				;;
			-h|--help)
				usage
				exit 0
				;;
			*)
				die "Unknown argument: $1 (run with --help)"
				;;
		esac
	done
}

main() {
	ascii_art
	parse_args "$@"

	case "$SUBCOMMAND" in
		install) command_install ;;
		update) command_update ;;
		*) die "Unknown subcommand: ${SUBCOMMAND}" ;;
	esac
}

main "$@"
