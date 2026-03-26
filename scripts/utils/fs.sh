#!/bin/sh

require_dir() {
    if [ ! -d "$1" ]; then
        error "Required directory not found: $1"
        exit 1
    fi
}