#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
cd "$REPO_ROOT"

usage() {
    echo "usage: $0 <vMAJOR.MINOR.PATCH | --all>" >&2
    exit 2
}

validate_changelog() {
    tag=$1

    if ! printf '%s\n' "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
        echo "invalid release tag '$tag': expected vMAJOR.MINOR.PATCH" >&2
        return 1
    fi

    changelog_path="docs/changelogs/${tag}.md"

    if [ ! -s "$changelog_path" ]; then
        echo "missing or empty release changelog: $changelog_path" >&2
        return 1
    fi

    first_line=$(sed -n '1p' "$changelog_path")
    case "$first_line" in
        "Sylve $tag" | "Sylve $tag "*) ;;
        *)
            echo "invalid introduction in $changelog_path: first line must start with 'Sylve $tag'" >&2
            return 1
            ;;
    esac

    if grep -Eiq '(^|[^[:alnum:]_])(TODO|TBD|PLACEHOLDER)([^[:alnum:]_]|$)' "$changelog_path"; then
        echo "release changelog contains placeholder text: $changelog_path" >&2
        return 1
    fi

    printf '%s\n' "$changelog_path"
}

[ "$#" -eq 1 ] || usage

if [ "$1" = "--all" ]; then
    found=false

    for changelog_path in docs/changelogs/*.md; do
        [ -f "$changelog_path" ] || continue
        found=true
        tag=$(basename "$changelog_path" .md)
        validate_changelog "$tag"
    done

    if [ "$found" != true ]; then
        echo "no release changelogs found in docs/changelogs" >&2
        exit 1
    fi

    exit 0
fi

validate_changelog "$1"
