#!/bin/sh
#
# Zelta Uninstaller
#
# Removes Zelta installation files, including legacy paths.
# Checks both system-wide and user installation locations.

usage() {
	cat <<-EOF
	usage: uninstall.sh [PHASE...]

	Remove Zelta installation files from specified locations.

	PHASES:
	  system    Remove system-wide installation (/usr/local/*)
	  home      Remove default user installation (~/.local/*, ~/bin)
	  env       Remove custom installation from ZELTA_* environment variables
	  all       Remove from all locations

	EXAMPLES:
	  uninstall.sh all          # Remove from all locations
	  uninstall.sh system       # Remove only system-wide installation
	  uninstall.sh home env     # Remove user and custom installations

	EOF
	exit 0
}

remove_if_exists() {
	if [ -L "$1" ] || [ -f "$1" ] && [ -w "$1" ]; then
		echo "- removing: $1"
		rm -f "$1"
		Tidied=1
	fi
}

remove_dir_if_exists() {
	if [ -d "$1" ] && [ -w "$1" ]; then
		if rmdir "$1" 2>/dev/null; then
			echo "- removing directory: $1"
			Tidied=1
		fi
	fi
}

find_zelta_symlinks() {
	search_dir="$1"
	if [ ! -d "$search_dir" ]; then
		return
	fi

	for link in "$search_dir"/*; do
		if [ -L "$link" ] && [ -e "$link" ]; then
			target=$(readlink "$link")
			case "$target" in
				*/zelta|zelta)
					remove_if_exists "$link"
					;;
			esac
		fi
	done
}

is_in_git_repo() {
	check_path="$1"
	if [ ! -e "$check_path" ]; then
		return 1
	fi

	# Check if path or any parent is a git repo
	current="$check_path"
	while [ "$current" != "/" ] && [ "$current" != "." ]; do
		if [ -d "$current/.git" ]; then
			return 0
		fi
		current=$(dirname "$current")
	done
	return 1
}

zelta_tidy() {
	phase_name="$1"
	bin_path="$2"
	sbin_path="$3"
	share_path="$4"
	etc_path="$5"
	doc_path="$6"
	legacy_doc="$7"


	# Check write permissions for at least one location
	can_write=0
	for check_path in "$bin_path" "$share_path" "$etc_path"; do
		if [ -d "$check_path" ] && [ -w "$check_path" ]; then
			can_write=1
			break
		fi
	done

	if [ $can_write -eq 0 ] && [ -d "$bin_path" -o -d "$share_path" -o -d "$etc_path" ]; then
		return
	fi

	# Protect git repositories
	if is_in_git_repo "$share_path"; then
		return
	fi

	echo "$phase_name:"

	# Remove zelta binaries
	remove_if_exists "$bin_path/zelta"
	[ -n "$sbin_path" ] && remove_if_exists "$sbin_path/zelta"

	# Remove symlinks
	find_zelta_symlinks "$bin_path"
	find_zelta_symlinks "$sbin_path"

	# Remove man pages (current location)
	for manpath in "$doc_path" "$legacy_doc"; do
		for file in "$manpath"/zelta*; do
			remove_if_exists "$file"
		done
		for section in 7 8; do
			mandir="${manpath}/man${section}"
			for manpage in "$mandir"/zelta*; do
				remove_if_exists "$manpage"
			done
			remove_dir_if_exists "$mandir"
		done
		remove_dir_if_exists "$manpath"
	done

	# Remove share directory
	if [ -d "$share_path" ] && ! is_in_git_repo "$share_path"; then
		for share_file in "$share_path"/zelta-*; do
			remove_if_exists "$share_file"
		done
		remove_dir_if_exists "$share_path"
	fi

	# Remove sample configs (but preserve user configs)
	if [ -d "$etc_path" ]; then
		remove_if_exists "$etc_path/zelta.conf.example"
		remove_if_exists "$etc_path/zelta.env.example"

		# Check for user configs
		if [ -w "$etc_path/zelta.conf" ] || [ -w "$etc_path/zelta.env" ]; then
			echo "- User configuration files preserved in $etc_path"
			Tidied=1
		fi
	fi
	if [ -n "$Tidied" ] ; then
		Tidied=""
	else
		echo "- Nothing to remove."
	fi
}

# Parse arguments
run_system=0
run_home=0
run_env=0

if [ $# -eq 0 ]; then
	# No arguments: run all phases (default behavior)
	usage
	exit 1
else
	# Parse phase arguments
	for arg in "$@"; do
		case "$arg" in
			system)
				run_system=1
				;;
			home)
				run_home=1
				;;
			env)
				run_env=1
				;;
			all)
				run_system=1
				run_home=1
				run_env=1
				;;
			-h|--help|help)
				usage
				;;
			*)
				echo "error: unknown phase '$arg'"
				usage
				exit 1
				;;
		esac
	done
fi

echo "Uninstalling Zelta"
echo "=================="
echo

# Phase 1: System-wide installation (if root or accessible)
if [ $run_system -eq 1 ]; then
	zelta_tidy "System-wide" \
		"/usr/local/bin" \
		"/usr/local/sbin" \
		"/usr/local/share/zelta" \
		"/usr/local/etc/zelta" \
		"/usr/local/man" \
		"/usr/local/share/man"
fi

# Phase 2: Default user installation
if [ $run_home -eq 1 ]; then
	zelta_tidy "User default" \
		"$HOME/bin" \
		"" \
		"$HOME/.local/share/zelta" \
		"$HOME/.config/zelta" \
		"$HOME/.local/share/zelta/doc" \
		"$HOME/.local/share/man"
fi

# Phase 3: Custom paths from environment (if set and different)
if [ $run_env -eq 1 ]; then
	if [ -n "$ZELTA_BIN" ] || [ -n "$ZELTA_SHARE" ] || [ -n "$ZELTA_ETC" ]; then
		custom_bin="${ZELTA_BIN:-$HOME/bin}"
		custom_share="${ZELTA_SHARE:-$HOME/.local/share/zelta}"
		custom_etc="${ZELTA_ETC:-$HOME/.config/zelta}"
		custom_doc="${ZELTA_DOC:-$custom_share/doc}"

		zelta_tidy "Custom environment" \
			"$custom_bin" \
			"" \
			"$custom_share" \
			"$custom_etc" \
			"$custom_doc" \
			""
	fi
fi

echo
echo "To reinstall, run: ./install.sh"
echo
echo "For detailed instructions, see:"
echo
echo "    https://zelta.space/home/install"
