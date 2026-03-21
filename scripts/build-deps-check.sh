#!/bin/sh

echo "=== Checking system dependencies for building Sylve ==="

if [ "$(uname)" != "FreeBSD" ]; then
    echo "❌ Error: This script must be run on FreeBSD."
    exit 1
fi

echo "✅ OS Check: Running on FreeBSD."

check_command() {
    if command -v "$1" >/dev/null 2>&1; then
        echo "✅ $1 found: $($2)"
    else
        echo "❌ Error: $1 is required but not found. Install using '$3'"
        exit 1
    fi
}

check_command node "node -v" "pkg install node24"
check_command npm "npm -v" "pkg install npm-node24"
check_command go "go version" "pkg install go"

echo "=== Dependency check completed ==="
echo
exit 0
