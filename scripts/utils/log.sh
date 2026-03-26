#!/bin/sh

RED=$(printf '\033[31m')
YELLOW=$(printf '\033[33m')
GREEN=$(printf '\033[32m')
RESET=$(printf '\033[0m')

log() {
    level=$1
    shift
    ts=$(date '+%H:%M:%S')

    case "$level" in
        INFO)  color=$GREEN ;;
        WARN)  color=$YELLOW ;;
        ERROR) color=$RED ;;
        *)     color=$RESET ;;
    esac

    printf '%s %s[%s]%s %s\n' "$ts" "$color" "$level" "$RESET" "$*"
}

info() {
    log INFO "$@"
}

warn() {
    log WARN "$@"
}

error() {
    log ERROR "$@"
}