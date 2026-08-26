#!/bin/sh

# Install trusted Linux application bundles for the current desktop user.
# Managed applications stay in the user's home directory and never require
# setuid files, FUSE mounts, or root privileges.

set -eu

PATH=/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin
export PATH

PROGRAM=${0##*/}
MANAGED_MARKER="Managed by the Sylve Linux app helper"
stage_directory=
backup_directory=
temporary_wrapper=
temporary_desktop=
installed_target=

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
Usage:
  $PROGRAM install [options] <source> [entry]
  $PROGRAM list
  $PROGRAM remove <app-id>

Install a trusted Linux application for the current Plasma user and create a
command plus a desktop launcher.

Sources:
  *.AppImage          Extract a Type 2 AppImage and launch its AppRun entry.
  supported-archive   Extract .tar.xz, .tar.gz, .tar.bz2, .txz, .tgz, .tbz,
                     or .zip. Supply the executable path inside the archive.
  executable-file     Install one standalone Linux executable.
  directory entry    Copy an unpacked application directory. The entry path is
                     relative to that directory, for example: feishin

Install options:
  --name NAME         Friendly launcher name. Defaults to the source name.
  --id ID             Command/application ID. Defaults to a slug of NAME.
  --icon FILE         PNG, SVG, or XPM icon to use in Plasma.
  --electron          Add Linuxulator's reduced-sandbox Chromium flags.
  --terminal          Open the application in a terminal window.
  --replace           Replace an application previously managed by this helper.
  -h, --help          Show this help.

Examples:
  $PROGRAM install --name Feishin --electron ~/Downloads/Feishin-linux-x86_64.AppImage
  $PROGRAM install --name Feishin --electron ~/Downloads/Feishin-linux-x64 feishin
  $PROGRAM install --name Feishin --electron ~/Downloads/Feishin-linux-x64.tar.xz Feishin-linux-x64/feishin
EOF
}

cleanup() {
    [ -z "$temporary_wrapper" ] || rm -f "$temporary_wrapper"
    [ -z "$temporary_desktop" ] || rm -f "$temporary_desktop"
    [ -z "$stage_directory" ] || rm -rf "$stage_directory"

    if [ -n "$backup_directory" ] && [ -d "$backup_directory" ]; then
        if [ -n "$installed_target" ] && [ ! -e "$installed_target" ]; then
            mv "$backup_directory" "$installed_target"
        else
            rm -rf "$backup_directory"
        fi
    fi
}

trap cleanup 0
trap 'exit 1' 1 2 15

validate_app_id() {
    candidate_id=$1
    case "$candidate_id" in
        ''|*[!a-z0-9-]*|-*|*-)
            die "Application IDs must contain lowercase letters, digits, and internal hyphens only."
            ;;
    esac
    [ "${#candidate_id}" -le 64 ] || die "Application IDs must be 64 characters or fewer."
}

slugify() {
    printf '%s\n' "$1" |
        LC_ALL=C tr '[:upper:]' '[:lower:]' |
        sed -e 's/[^a-z0-9][^a-z0-9]*/-/g' -e 's/^-//' -e 's/-$//'
}

require_regular_user() {
    [ "$(id -u)" -ne 0 ] || die "Run this helper as the regular Plasma user, not root."
    [ -n "${HOME:-}" ] && [ "${HOME#/}" != "$HOME" ] || die "HOME must be an absolute path."
    [ -d "$HOME" ] || die "The home directory does not exist: $HOME"
}

is_managed_file() {
    managed_file=$1
    [ -f "$managed_file" ] && grep -Fq "$MANAGED_MARKER" "$managed_file"
}

metadata_value() {
    metadata_key=$1
    metadata_file=$2
    sed -n "s/^${metadata_key}=//p" "$metadata_file" | sed -n '1p'
}

refresh_desktop_database() {
    desktop_directory=$1
    if command -v update-desktop-database >/dev/null 2>&1; then
        update-desktop-database "$desktop_directory" >/dev/null 2>&1 ||
            warn "Plasma's application cache could not be refreshed; the launcher may appear after the next login."
    fi
}

list_apps() {
    require_regular_user
    list_root="$HOME/.local/share/sylve-linux-apps"
    found_app=NO

    printf '%-24s %-10s %s\n' "APP ID" "TYPE" "NAME"
    for list_metadata in "$list_root"/*/.sylve-linux-app
    do
        [ -f "$list_metadata" ] || continue
        grep -Fq "$MANAGED_MARKER" "$list_metadata" || continue
        found_app=YES
        list_id=$(metadata_value id "$list_metadata")
        list_kind=$(metadata_value kind "$list_metadata")
        list_name=$(metadata_value name "$list_metadata")
        printf '%-24s %-10s %s\n' "$list_id" "$list_kind" "$list_name"
    done

    [ "$found_app" = "YES" ] || printf '%s\n' "No applications are managed by $PROGRAM."
}

remove_app() {
    [ "$#" -eq 1 ] || die "remove requires one application ID."
    require_regular_user
    remove_id=$1
    validate_app_id "$remove_id"

    remove_root="$HOME/.local/share/sylve-linux-apps/$remove_id"
    remove_metadata="$remove_root/.sylve-linux-app"
    remove_wrapper="$HOME/.local/bin/$remove_id"
    remove_desktop="$HOME/.local/share/applications/sylve-linux-$remove_id.desktop"

    [ -f "$remove_metadata" ] && grep -Fq "$MANAGED_MARKER" "$remove_metadata" ||
        die "No managed application has the ID: $remove_id"

    if [ -e "$remove_wrapper" ] && ! is_managed_file "$remove_wrapper"; then
        die "Refusing to remove the unmanaged command: $remove_wrapper"
    fi
    if [ -e "$remove_desktop" ] && ! is_managed_file "$remove_desktop"; then
        die "Refusing to remove the unmanaged launcher: $remove_desktop"
    fi

    rm -f "$remove_wrapper" "$remove_desktop"
    rm -rf "$remove_root"
    refresh_desktop_database "$HOME/.local/share/applications"
    log "Removed $remove_id"
}

install_app() {
    require_regular_user

    install_name=
    requested_id=
    requested_icon=
    electron_mode=NO
    terminal_mode=NO
    replace_mode=NO

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --name)
                [ "$#" -ge 2 ] || die "--name requires a value."
                install_name=$2
                shift 2
                ;;
            --id)
                [ "$#" -ge 2 ] || die "--id requires a value."
                requested_id=$2
                shift 2
                ;;
            --icon)
                [ "$#" -ge 2 ] || die "--icon requires a file."
                requested_icon=$2
                shift 2
                ;;
            --electron)
                electron_mode=YES
                shift
                ;;
            --terminal)
                terminal_mode=YES
                shift
                ;;
            --replace)
                replace_mode=YES
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            --)
                shift
                break
                ;;
            -*)
                die "Unknown option: $1"
                ;;
            *)
                break
                ;;
        esac
    done

    [ "$#" -ge 1 ] || die "install requires an AppImage, executable, or directory."
    [ "$#" -le 2 ] || die "Too many arguments. Archives and directories accept one relative entry path."

    source_argument=$1
    shift
    requested_entry=${1:-}
    [ -e "$source_argument" ] || die "Source does not exist: $source_argument"
    source_path=$(realpath "$source_argument") || die "Could not resolve the source path: $source_argument"

    source_base=${source_path##*/}
    if [ -d "$source_path" ]; then
        app_kind=bundle
        [ -n "$requested_entry" ] || die "Directory sources require a relative entry path, such as: feishin"
    elif [ -f "$source_path" ]; then
        case "$source_base" in
            *.[Aa][Pp][Pp][Ii][Mm][Aa][Gg][Ee])
                app_kind=appimage
                ;;
            *.[Tt][Aa][Rr].[Xx][Zz]|*.[Tt][Xx][Zz]|*.[Tt][Aa][Rr].[Gg][Zz]|*.[Tt][Gg][Zz]|*.[Tt][Aa][Rr].[Bb][Zz]2|*.[Tt][Bb][Zz]|*.[Zz][Ii][Pp])
                app_kind=archive
                ;;
            *)
                app_kind=binary
                ;;
        esac
        if [ "$app_kind" = "archive" ]; then
            [ -n "$requested_entry" ] || die "Archive sources require the executable's relative path inside the archive."
        else
            [ -z "$requested_entry" ] || die "An entry path is accepted only when the source is an archive or directory."
        fi
    else
        die "The source must be a regular file or directory."
    fi

    if [ -z "$install_name" ]; then
        install_name=$source_base
        if [ "$app_kind" = "appimage" ]; then
            install_name=$(printf '%s\n' "$install_name" | sed 's/\.[Aa][Pp][Pp][Ii][Mm][Aa][Gg][Ee]$//')
        fi
    fi
    [ -n "$install_name" ] || die "The application name cannot be empty."
    if printf '%s' "$install_name" | LC_ALL=C grep -q '[[:cntrl:]]'; then
        die "The application name cannot contain control characters."
    fi

    if [ -n "$requested_id" ]; then
        app_id=$requested_id
    else
        app_id=$(slugify "$install_name")
    fi
    validate_app_id "$app_id"

    if [ -n "$requested_icon" ]; then
        [ -f "$requested_icon" ] || die "Icon is not a regular file: $requested_icon"
        icon_path=$(realpath "$requested_icon") || die "Could not resolve the icon path."
        case "$icon_path" in
            *.[Pp][Nn][Gg]|*.[Ss][Vv][Gg]|*.[Xx][Pp][Mm]) ;;
            *) die "Icons must use PNG, SVG, or XPM format." ;;
        esac
    else
        icon_path=
    fi

    linux_uname=/compat/linux/bin/uname
    linux_env=/compat/linux/usr/bin/env
    linux_bash=/compat/linux/bin/bash
    [ -x "$linux_uname" ] || die "The Rocky Linux compatibility userland is missing. Complete the Linux setup first."
    [ -x "$linux_env" ] || die "The Linux userland is missing /usr/bin/env. Install the linux-rl9 metapackage."
    if ! "$linux_uname" -s 2>/dev/null | grep -qx Linux; then
        die "Linuxulator could not execute a test binary. Check the jail's Linux option and FSTab mounts."
    fi
    if command -v pkg >/dev/null 2>&1 && ! pkg info -e linux-rl9 >/dev/null 2>&1; then
        warn "The full linux-rl9 metapackage is not installed. Graphical applications may report missing libraries."
    fi

    data_root="$HOME/.local/share/sylve-linux-apps"
    binary_root="$HOME/.local/bin"
    desktop_root="$HOME/.local/share/applications"
    target_directory="$data_root/$app_id"
    wrapper_path="$binary_root/$app_id"
    desktop_path="$desktop_root/sylve-linux-$app_id.desktop"
    installed_target=$target_directory

    mkdir -p "$data_root" "$binary_root" "$desktop_root"

    if [ -e "$target_directory" ]; then
        [ -f "$target_directory/.sylve-linux-app" ] &&
            grep -Fq "$MANAGED_MARKER" "$target_directory/.sylve-linux-app" ||
            die "Refusing to replace an unmanaged directory: $target_directory"
        [ "$replace_mode" = "YES" ] ||
            die "$app_id is already installed. Rerun with --replace to update it."
    fi
    if [ -e "$wrapper_path" ] && ! is_managed_file "$wrapper_path"; then
        die "Refusing to replace the unmanaged command: $wrapper_path"
    fi
    if [ -e "$desktop_path" ] && ! is_managed_file "$desktop_path"; then
        die "Refusing to replace the unmanaged launcher: $desktop_path"
    fi

    stage_directory=$(mktemp -d "$data_root/.stage.$app_id.XXXXXX") ||
        die "Could not create an installation staging directory."
    payload_directory="$stage_directory/payload"
    mkdir "$payload_directory"

    case "$app_kind" in
        appimage)
            log "Extracting $source_base without FUSE"
            staged_appimage="$stage_directory/source.AppImage"
            cp "$source_path" "$staged_appimage"
            chmod 0700 "$staged_appimage"
            command -v brandelf >/dev/null 2>&1 ||
                die "FreeBSD's brandelf command is required to extract Linux AppImages."
            if ! brandelf -t Linux "$staged_appimage"; then
                die "The AppImage runtime could not be marked as a Linux ELF executable."
            fi
            extraction_log="$stage_directory/extraction.log"
            if ! (cd "$stage_directory" && ./source.AppImage --appimage-extract) >"$extraction_log" 2>&1; then
                cat "$extraction_log" >&2
                die "AppImage extraction failed. This helper supports Type 2 AppImages with --appimage-extract."
            fi
            [ -d "$stage_directory/squashfs-root" ] || die "The AppImage did not produce a squashfs-root directory."
            rmdir "$payload_directory"
            mv "$stage_directory/squashfs-root" "$payload_directory"
            mv "$staged_appimage" "$payload_directory/.source.AppImage"
            rm -f "$extraction_log"
            installed_entry=AppRun
            [ -e "$payload_directory/$installed_entry" ] || die "The extracted AppImage has no AppRun entry."
            chmod u+x "$payload_directory/$installed_entry"
            ;;
        archive)
            command -v tar >/dev/null 2>&1 ||
                die "FreeBSD's tar command is required to extract application archives."
            case "$requested_entry" in
                ''|/*|..|../*|*/../*|*/..|*[!A-Za-z0-9._/-]*)
                    die "The archive entry must be a safe relative path using letters, digits, dots, underscores, slashes, or hyphens."
                    ;;
            esac

            archive_listing="$stage_directory/archive-listing"
            tar -tf "$source_path" >"$archive_listing" || die "The archive could not be read by FreeBSD tar."
            if ! awk '
                {
                    path = $0
                    sub(/^\.\//, "", path)
                    if (path ~ /^\// || path == ".." || path ~ /(^|\/)\.\.(\/|$)/) {
                        print path > "/dev/stderr"
                        unsafe = 1
                    }
                }
                END { exit unsafe }
            ' "$archive_listing"; then
                die "The archive contains a path that escapes its installation directory."
            fi

            log "Extracting the application archive: $source_base"
            tar -xf "$source_path" -C "$payload_directory"
            rm -f "$archive_listing"
            installed_entry=$requested_entry
            archive_entry=$(realpath "$payload_directory/$installed_entry" 2>/dev/null) ||
                die "The requested executable was not found after extraction: $installed_entry"
            case "$archive_entry" in
                "$payload_directory"/*) ;;
                *) die "The archive entry resolves outside the extracted application." ;;
            esac
            [ -f "$archive_entry" ] || die "The archive entry is not a regular file: $installed_entry"
            chmod u+x "$payload_directory/$installed_entry"
            ;;
        binary)
            log "Installing the standalone Linux executable: $source_base"
            installed_entry=$app_id
            cp "$source_path" "$payload_directory/$installed_entry"
            chmod 0700 "$payload_directory/$installed_entry"
            ;;
        bundle)
            case "$requested_entry" in
                ''|/*|..|../*|*/../*|*/..|*[!A-Za-z0-9._/-]*)
                    die "The bundle entry must be a safe relative path using letters, digits, dots, underscores, slashes, or hyphens."
                    ;;
            esac
            source_entry=$(realpath "$source_path/$requested_entry" 2>/dev/null) ||
                die "The bundle entry does not exist: $requested_entry"
            case "$source_entry" in
                "$source_path"/*) ;;
                *) die "The bundle entry resolves outside the source directory." ;;
            esac
            [ -f "$source_entry" ] || die "The bundle entry is not a regular file: $requested_entry"

            special_entry=$(find "$source_path" ! -type f ! -type d ! -type l -print | sed -n '1p')
            [ -z "$special_entry" ] || die "The bundle contains an unsupported special file: $special_entry"

            log "Copying the application bundle: $source_base"
            cp -R "$source_path/." "$payload_directory/"
            installed_entry=$requested_entry
            chmod u+x "$payload_directory/$installed_entry"
            ;;
    esac

    special_payload=$(find "$payload_directory" ! -type f ! -type d ! -type l -print | sed -n '1p')
    [ -z "$special_payload" ] || die "The installed payload contains an unsupported special file: $special_payload"

    # A user-managed application never needs inherited setuid or setgid bits.
    find "$payload_directory" -type f -exec chmod u-s,g-s {} +

    installed_entry_path="$payload_directory/$installed_entry"
    entry_description=$(file -b -L "$installed_entry_path" 2>/dev/null || printf 'unknown')
    case "$entry_description" in
        *ELF*)
            host_architecture=$(uname -m)
            case "$host_architecture:$entry_description" in
                amd64:*x86-64*|x86_64:*x86-64*|arm64:*aarch64*|aarch64:*aarch64*) ;;
                amd64:*|x86_64:*|arm64:*|aarch64:*)
                    die "The application architecture does not match this jail: $entry_description"
                    ;;
            esac

            if [ -x "$linux_bash" ]; then
                ldd_output=$("$linux_bash" -c '/usr/bin/ldd "$1"' sylve-linux-app "$installed_entry_path" 2>&1 || :)
                missing_libraries=$(printf '%s\n' "$ldd_output" | awk '/not found/ { print }')
                if [ -n "$missing_libraries" ]; then
                    warn "The application still has unresolved Linux libraries:"
                    printf '%s\n' "$missing_libraries" >&2
                fi
            fi
            ;;
    esac

    icon_relative=
    if [ -n "$icon_path" ]; then
        icon_extension=${icon_path##*.}
        icon_relative=".sylve-icon.$icon_extension"
        cp "$icon_path" "$payload_directory/$icon_relative"
        chmod 0600 "$payload_directory/$icon_relative"
    elif [ -e "$payload_directory/.DirIcon" ]; then
        resolved_icon=$(realpath "$payload_directory/.DirIcon" 2>/dev/null || :)
        case "$resolved_icon" in
            "$payload_directory"/*)
                icon_relative=${resolved_icon#"$payload_directory/"}
                ;;
        esac
    else
        for icon_candidate in "$payload_directory"/*.png "$payload_directory"/*.svg "$payload_directory"/*.xpm
        do
            [ -f "$icon_candidate" ] || continue
            icon_relative=${icon_candidate#"$payload_directory/"}
            break
        done
    fi

    installed_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    cat >"$stage_directory/.sylve-linux-app" <<EOF
# $MANAGED_MARKER
id=$app_id
name=$install_name
kind=$app_kind
entry=$installed_entry
electron=$electron_mode
installed_at=$installed_at
EOF
    chmod 0600 "$stage_directory/.sylve-linux-app"

    temporary_wrapper=$(mktemp "$binary_root/.$app_id.XXXXXX") || die "Could not create the command wrapper."
    cat >"$temporary_wrapper" <<EOF
#!/bin/sh
# $MANAGED_MARKER

set -u

APP_ROOT="\${HOME}/.local/share/sylve-linux-apps/$app_id/payload"
ENTRY="\${APP_ROOT}/$installed_entry"

if [ ! -x "\$ENTRY" ]; then
    printf 'ERROR: Linux application entry is missing or not executable: %s\\n' "\$ENTRY" >&2
    exit 126
fi

export GDK_BACKEND="\${GDK_BACKEND:-x11}"
export QT_QPA_PLATFORM="\${QT_QPA_PLATFORM:-xcb}"
if [ -c /dev/dri/renderD128 ] && [ -r /dev/dri/renderD128 ] && [ -w /dev/dri/renderD128 ]; then
    : # Let Mesa select the host render node unless the environment overrides it.
else
    export LIBGL_ALWAYS_SOFTWARE="\${LIBGL_ALWAYS_SOFTWARE:-1}"
fi
EOF

    if [ "$app_kind" = "appimage" ]; then
        cat >>"$temporary_wrapper" <<'EOF'
export APPDIR="$APP_ROOT"
export APPIMAGE="$APP_ROOT/.source.AppImage"
export OWD="${PWD}"
EOF
    fi

    if [ "$electron_mode" = "YES" ]; then
        cat >>"$temporary_wrapper" <<'EOF'

# FreeBSD Linuxulator does not provide Chromium's Linux namespace sandbox or
# zygote process model. These flags trade Electron's inner sandbox for the
# outer FreeBSD jail boundary and must be used only with trusted applications.
set -- --no-sandbox --no-zygote --in-process-gpu "$@"
EOF
    fi

    cat >>"$temporary_wrapper" <<'EOF'

exec /compat/linux/usr/bin/env "$ENTRY" "$@"
EOF
    chmod 0700 "$temporary_wrapper"

    desktop_name=$(printf '%s\n' "$install_name" | sed 's/\\/\\\\/g')
    desktop_exec=$(printf '%s\n' "$wrapper_path" | sed 's/\\/\\\\/g; s/"/\\"/g; s/`/\\`/g; s/\$/\\$/g')
    if [ -n "$icon_relative" ]; then
        final_icon="$target_directory/payload/$icon_relative"
        desktop_icon=$(printf '%s\n' "$final_icon" | sed 's/\\/\\\\/g')
    else
        desktop_icon=application-x-executable
    fi

    temporary_desktop=$(mktemp "$desktop_root/.sylve-linux-$app_id.XXXXXX") ||
        die "Could not create the Plasma launcher."
    cat >"$temporary_desktop" <<EOF
[Desktop Entry]
# $MANAGED_MARKER
Type=Application
Name=$desktop_name
Comment=Linux application installed by Sylve
Exec="$desktop_exec" %U
Icon=$desktop_icon
Terminal=$( [ "$terminal_mode" = "YES" ] && printf 'true' || printf 'false' )
Categories=Utility;
StartupNotify=true
X-Sylve-Linux-App=true
EOF
    chmod 0600 "$temporary_desktop"

    if [ -d "$target_directory" ]; then
        backup_directory=$(mktemp -d "$data_root/.backup.$app_id.XXXXXX") ||
            die "Could not create a replacement backup directory."
        rmdir "$backup_directory"
        mv "$target_directory" "$backup_directory"
    fi

    mv "$stage_directory" "$target_directory"
    stage_directory=
    mv "$temporary_wrapper" "$wrapper_path"
    temporary_wrapper=
    mv "$temporary_desktop" "$desktop_path"
    temporary_desktop=
    chmod 0700 "$wrapper_path"
    chmod 0600 "$desktop_path"

    if [ -n "$backup_directory" ]; then
        rm -rf "$backup_directory"
        backup_directory=
    fi

    refresh_desktop_database "$desktop_root"

    log "Installed $install_name"
    printf '%s\n' \
        "Command:  $wrapper_path" \
        "Launcher: $desktop_path" \
        "App ID:   $app_id"
    if [ "$electron_mode" = "YES" ]; then
        warn "Electron compatibility mode disables Chromium's internal sandbox. The application still runs as this user inside the FreeBSD jail."
    fi
}

command_name=${1:-help}
if [ "$#" -gt 0 ]; then
    shift
fi

case "$command_name" in
    install)
        install_app "$@"
        ;;
    list)
        [ "$#" -eq 0 ] || die "list does not accept arguments."
        list_apps
        ;;
    remove)
        remove_app "$@"
        ;;
    help|-h|--help)
        usage
        ;;
    *)
        usage >&2
        die "Unknown command: $command_name"
        ;;
esac
