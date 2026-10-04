#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause

set -eu
leaked=0

for tool in ctladm iscsictl ps timeout; do
	command -v "$tool" >/dev/null || { echo "$tool is required for the iSCSI leak check" >&2; exit 1; }
done

for state_dir in /tmp/sylve-iscsi-test-*; do
	[ -e "$state_dir" ] || continue
	printf 'iSCSI test fixture remains: %s\n' "$state_dir" >&2
	leaked=1
done

ports="$(timeout -k 1s 10s ctladm portlist -x)" || { echo "Cannot check iSCSI target ports" >&2; exit 1; }
port_ids="$(printf '%s\n' "$ports" | awk '
	/<targ_port id=/ { id=$0; sub(/^.*id="/, "", id); sub(/".*$/, "", id); owned=0 }
	/<cfiscsi_target>iqn\.2026-10\.io\.sylve:test-/ { owned=1 }
	/<\/targ_port>/ && owned { print id }
')"
if [ -n "$port_ids" ]; then
	printf 'iSCSI test CTL port IDs remain: %s\n' "$port_ids" >&2
	leaked=1
fi

luns="$(timeout -k 1s 10s ctladm devlist -x)" || { echo "Cannot check iSCSI target LUNs" >&2; exit 1; }
lun_ids="$(printf '%s\n' "$luns" | awk '
	/<lun id=/ { id=$0; sub(/^.*id="/, "", id); sub(/".*$/, "", id); owned=0 }
	/<ctld_name>iqn\.2026-10\.io\.sylve:test-/ { owned=1 }
	/<\/lun>/ && owned { print id }
')"
if [ -n "$lun_ids" ]; then
	printf 'iSCSI test CTL LUN IDs remain: %s\n' "$lun_ids" >&2
	leaked=1
fi

sessions="$(timeout -k 1s 10s iscsictl -L)" || { echo "Cannot check iSCSI initiator sessions" >&2; exit 1; }
session_count="$(printf '%s\n' "$sessions" | awk '$1 ~ /^iqn\.2026-10\.io\.sylve:test-/ { count++ } END { print count+0 }')"
if [ "$session_count" -ne 0 ]; then
	printf 'iSCSI test initiator sessions remain: %s\n' "$session_count" >&2
	leaked=1
fi

processes="$(timeout -k 1s 10s ps -ax -o pid= -o args=)" || { echo "Cannot check iSCSI test processes" >&2; exit 1; }
process_ids="$(printf '%s\n' "$processes" | awk '$0 ~ /ctld/ && $0 ~ /-f \/tmp\/sylve-iscsi-test-[A-Za-z0-9_-]+\/ctl\.conf/ { print $1 }')"
if [ -n "$process_ids" ]; then
	printf 'iSCSI test ctld process IDs remain: %s\n' "$process_ids" >&2
	leaked=1
fi

if [ "$leaked" -ne 0 ]; then
	echo "Leak check is read-only. Inspect the fixture manifest before manual cleanup." >&2
fi
exit "$leaked"
