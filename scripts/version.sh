#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/utils/log.sh"
. "$SCRIPT_DIR/utils/fs.sh"

VERSION="${1:?usage: $0 <version>}"

status=0

info "Starting version bump to $VERSION"

run_npm_version() {
    dir=$1

    info "Updating version in $dir -> $VERSION"

    if output=$(npm version "$VERSION" --no-git-tag-version --prefix "$dir" 2>&1); then
        info "Success: $dir"
        info "$output"
        return 0
    fi

    case "$output" in
        *"Version not changed"*)
            warn "Skipped: $dir already at $VERSION"
            return 0
            ;;
        *)
            error "Failed: $dir"
            error "$output"
            status=1
            return 0
            ;;
    esac
}

update_internal_version() {
    root_file="internal/cmd/root.go"
    routes_file="internal/handlers/routes.go"

    info "Updating Backend version -> $VERSION"

    if [ ! -f "$root_file" ]; then
        error "File not found: $root_file"
        status=1
    else
        if sed -i '' "s/^const Version = \".*\"/const Version = \"$VERSION\"/" "$root_file" 2>/dev/null; then
            info "Updated $root_file"
        else
            sed -i "s/^const Version = \".*\"/const Version = \"$VERSION\"/" "$root_file"
            info "Updated $root_file"
        fi
    fi

    if [ ! -f "$routes_file" ]; then
        error "File not found: $routes_file"
        status=1
    else
        if sed -i '' "s|^// @version[[:space:]]*.*|// @version         $VERSION|" "$routes_file" 2>/dev/null; then
            info "Updated $routes_file"
        else
            sed -i "s|^// @version[[:space:]]*.*|// @version         $VERSION|" "$routes_file"
            info "Updated $routes_file"
        fi
    fi
}

require_dir web
require_dir internal
require_dir docs

run_npm_version web/
run_npm_version docs/app-docs/
update_internal_version

exit "$status"