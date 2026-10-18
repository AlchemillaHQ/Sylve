stage_osversion() (
	version=$(awk '$1 == "#define" && $2 == "__FreeBSD_version" {print $3; exit}' \
		"$STAGE/usr/include/sys/param.h") || return
	case "$version" in
	''|*[!0-9]*) echo "cannot read target FreeBSD version from $STAGE/usr/include/sys/param.h" >&2; return 1 ;;
	esac
	if [ "$((version / 100000))" -ne "$FBSD_MAJOR" ]; then
		echo "staged FreeBSD version $version does not match FreeBSD $FBSD_MAJOR" >&2
		return 1
	fi
	printf '%s\n' "$version"
)

stage_pkg()
{
	set -- -r "$STAGE" -o "REPOS_DIR=$STAGE/etc/pkg,$STAGE/usr/local/etc/pkg/repos" \
		-o "OSVERSION=$STAGE_OSVERSION" "$@"
	if [ -n "$FBSD_ABI" ]; then
		set -- -o "ABI=$FBSD_ABI" "$@"
	fi
	pkg "$@"
}

write_repos()
{
	cat >"$STAGE/usr/local/etc/pkg/repos/FreeBSD-base.conf" <<EOF
FreeBSD-base {
  url: "pkg+https://pkg.freebsd.org/\${ABI}/${PKGBASE_REPO}",
  mirror_type: "srv",
  signature_type: "fingerprints",
  fingerprints: "/usr/share/keys/pkg",
  enabled: yes
}
EOF
	cat >"$STAGE/etc/pkg/FreeBSD.conf" <<EOF
FreeBSD-ports {
  url: "pkg+https://pkg.FreeBSD.org/\${ABI}/${PORTS_REPO}",
  mirror_type: "srv",
  signature_type: "fingerprints",
  fingerprints: "/usr/share/keys/pkg",
  enabled: yes
}
FreeBSD-ports-kmods {
  url: "pkg+https://pkg.FreeBSD.org/\${ABI}/${KMODS_REPO}",
  mirror_type: "srv",
  signature_type: "fingerprints",
  fingerprints: "/usr/share/keys/pkg",
  enabled: yes
}
EOF
}
