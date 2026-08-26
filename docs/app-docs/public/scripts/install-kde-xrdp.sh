#!/bin/sh

# Install a KDE Plasma X11 desktop and XRDP inside a FreeBSD jail.
# This script is intended to be safe to rerun after an interruption.

set -eu

PATH=/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin
export PATH

PROGRAM=${0##*/}
MANAGED_MARKER="Managed by the Sylve KDE/XRDP one-shot installer"
SKIP_PASSWORD_SETUP=${SKIP_PASSWORD_SETUP:-NO}
ALLOW_NON_JAIL=${ALLOW_NON_JAIL:-NO}
tmp_file=

log() {
    printf '\n==> %s\n' "$*"
}

warn() {
    printf 'WARNING: %s\n' "$*" >&2
}

die() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<EOF
Usage: $PROGRAM <desktop-user>

Install KDE Plasma and XRDP in the current FreeBSD jail. The named regular
user is created when necessary and is used to sign in through RDP.

Environment overrides:
  SKIP_PASSWORD_SETUP=YES  Allow the user to remain locked for later setup.
  ALLOW_NON_JAIL=YES       Allow installation on a FreeBSD host (not advised).
EOF
}

cleanup() {
    if [ -n "$tmp_file" ]; then
        rm -f "$tmp_file"
    fi
}

trap cleanup EXIT HUP INT TERM

if [ "$#" -eq 1 ] && { [ "$1" = "-h" ] || [ "$1" = "--help" ]; }; then
    usage
    exit 0
fi

[ "$#" -eq 1 ] || {
    usage >&2
    exit 64
}

desktop_user=$1

case "$desktop_user" in
    root)
        die "Use a regular login account, not root."
        ;;
    ''|*[!a-z0-9_-]*|[0-9-]*)
        die "The desktop user must start with a letter or underscore and contain only lowercase letters, digits, underscores, or hyphens."
        ;;
esac

[ "${#desktop_user}" -le 32 ] || die "The desktop user name must be 32 characters or fewer."
[ "$(id -u)" -eq 0 ] || die "Run this script as root."
[ "$(uname -s)" = "FreeBSD" ] || die "This installer supports FreeBSD only."

jailed=$(sysctl -n security.jail.jailed 2>/dev/null || printf '0')
if [ "$jailed" != "1" ] && [ "$ALLOW_NON_JAIL" != "YES" ]; then
    die "This system is not a jail. Set ALLOW_NON_JAIL=YES only if you deliberately want to install on the host."
fi

userland_release=$(freebsd-version -u 2>/dev/null || freebsd-version)
release_major=${userland_release%%.*}
case "$release_major" in
    ''|*[!0-9]*)
        die "Could not determine the FreeBSD userland version."
        ;;
esac
[ "$release_major" -ge 15 ] || die "FreeBSD 15.0 or newer is required; found $userland_release."

command -v pkg >/dev/null 2>&1 || die "pkg is not installed in this jail."
command -v pw >/dev/null 2>&1 || die "pw is not available in this jail."
pw groupshow video >/dev/null 2>&1 || die "The FreeBSD video group is missing."

user_exists=NO
password_needed=YES

if pw usershow "$desktop_user" >/dev/null 2>&1; then
    user_exists=YES
    user_record=$(pw usershow "$desktop_user")
    user_id=$(printf '%s\n' "$user_record" | awk -F: '{ print $3 }')
    [ "$user_id" -ge 1000 ] || die "$desktop_user exists but is not a regular user account."

    password_field=$(printf '%s\n' "$user_record" | awk -F: '{ print $2 }')
    case "$password_field" in
        ''|\**|!*)
            password_needed=YES
            ;;
        *)
            password_needed=NO
            ;;
    esac
elif [ ! -t 0 ] && [ "$SKIP_PASSWORD_SETUP" != "YES" ]; then
    die "Creating $desktop_user requires an interactive terminal for passwd. Rerun interactively or set SKIP_PASSWORD_SETUP=YES and assign a password later."
fi

if [ "$user_exists" = "NO" ]; then
    log "Creating the regular desktop account: $desktop_user"
    pw useradd "$desktop_user" -m -s /bin/sh -c "KDE desktop user" -G video
fi

if ! id -Gn "$desktop_user" | tr ' ' '\n' | grep -qx video; then
    log "Adding $desktop_user to the video group"
    pw groupmod video -m "$desktop_user"
fi

if [ "$password_needed" = "YES" ]; then
    if [ "$SKIP_PASSWORD_SETUP" = "YES" ]; then
        warn "$desktop_user does not yet have a usable password. Run 'passwd $desktop_user' before trying to sign in through RDP."
    elif [ -t 0 ]; then
        log "Set the RDP password for $desktop_user"
        passwd "$desktop_user"
    else
        die "$desktop_user has no usable password. Run 'passwd $desktop_user' interactively, then rerun this installer."
    fi
fi

log "Refreshing signed FreeBSD package catalogues"
pkg update

log "Installing the full KDE Plasma desktop and XRDP Xorg backend"
pkg install -y kde xrdp xorgxrdp

for required_command in \
    /usr/local/bin/ck-launch-session \
    /usr/local/bin/dbus-launch \
    /usr/local/bin/startplasma-x11 \
    /usr/local/libexec/Xorg \
    /usr/local/sbin/xrdp \
    /usr/local/sbin/xrdp-sesman
do
    [ -x "$required_command" ] || die "The installation completed without the expected command: $required_command"
done

user_record=$(pw usershow "$desktop_user")
home_directory=$(printf '%s\n' "$user_record" | awk -F: '{ print $9 }')
primary_group=$(id -gn "$desktop_user")
[ -d "$home_directory" ] || die "The desktop user's home directory does not exist: $home_directory"

session_script="$home_directory/startwm.sh"
if [ -f "$session_script" ] && ! grep -Fq "$MANAGED_MARKER" "$session_script"; then
    backup="$session_script.pre-sylve-kde-xrdp.$(date +%Y%m%d%H%M%S)"
    cp -p "$session_script" "$backup"
    warn "Saved the existing session script as $backup."
fi

tmp_file=$(mktemp /tmp/sylve-kde-xrdp.XXXXXX)
cat >"$tmp_file" <<'EOF'
#!/bin/sh
# Managed by the Sylve KDE/XRDP one-shot installer.

PATH=/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin
export PATH

export LANG="${LANG:-C.UTF-8}"
export LC_ALL="${LC_ALL:-C.UTF-8}"
export XDG_SESSION_TYPE=x11
export XDG_CURRENT_DESKTOP=KDE
export KDE_FULL_SESSION=true

# A headless jail has no GPU device. Force Mesa's software renderer so Plasma
# does not depend on host graphics devices or a permissive DevFS ruleset.
export LIBGL_ALWAYS_SOFTWARE=1

unset DBUS_SESSION_BUS_ADDRESS
unset SESSION_MANAGER

exec /usr/local/bin/dbus-launch --exit-with-x11 \
    /usr/local/bin/ck-launch-session /usr/local/bin/startplasma-x11
EOF
install -o "$desktop_user" -g "$primary_group" -m 0700 "$tmp_file" "$session_script"
rm -f "$tmp_file"
tmp_file=

sesman_config=/usr/local/etc/xrdp/sesman.ini
[ -f "$sesman_config" ] || die "XRDP did not install $sesman_config."

if grep -Eq '^AllowRootLogin=(true|yes|1)$' "$sesman_config"; then
    cp -p "$sesman_config" "$sesman_config.pre-sylve-kde-xrdp"
    sed -i '' -E 's/^AllowRootLogin=(true|yes|1)$/AllowRootLogin=false/' "$sesman_config"
fi

log "Enabling D-Bus and XRDP at jail startup"
sysrc dbus_enable=YES
sysrc xrdp_enable=YES
sysrc xrdp_sesman_enable=YES

restart_or_start() {
    service_name=$1
    if service "$service_name" onestatus >/dev/null 2>&1; then
        service "$service_name" restart
    else
        service "$service_name" start
    fi
}

log "Starting D-Bus and XRDP"
restart_or_start dbus
restart_or_start xrdp-sesman
restart_or_start xrdp

service dbus onestatus >/dev/null 2>&1 || die "D-Bus is not running."
service xrdp-sesman onestatus >/dev/null 2>&1 || die "xrdp-sesman is not running."
service xrdp onestatus >/dev/null 2>&1 || die "xrdp is not running."

port_ready=NO
port_check=0
while [ "$port_check" -lt 15 ]; do
    if sockstat -4 -6 -l | grep -Eq '[.:]3389[[:space:]]'; then
        port_ready=YES
        break
    fi
    port_check=$((port_check + 1))
    sleep 1
done

if [ "$port_ready" != "YES" ]; then
    die "XRDP is running but TCP port 3389 is not listening."
fi

log "KDE Plasma and XRDP are ready"
printf '%s\n' \
    "RDP user: $desktop_user" \
    "RDP port: 3389" \
    "Session:  Xorg / Plasma X11" \
    "" \
    "Connect to this jail's IP address with an RDP client. Keep TCP 3389" \
    "limited to a trusted LAN or VPN; do not publish it directly to the Internet."

if [ "$SKIP_PASSWORD_SETUP" = "YES" ] && [ "$password_needed" = "YES" ]; then
    printf '\nSet a password before connecting:\n  passwd %s\n' "$desktop_user"
fi
