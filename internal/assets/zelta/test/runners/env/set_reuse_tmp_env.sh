#!/bin/sh

# Debug environment setup
#   To facilitate creating and manually shellspec tests, and debugging existing tests
#   use the last spec installed zelta version
#   if no previous zelta install is found in /tmp, show user how to create one

# find the last installed version of zelta installed by shellspec
last_tmp_installed_zelta_ver=$(ls -1d /tmp/zelta* | tail -1)

# exit if no previous install found
if [ -z "$last_tmp_installed_zelta_ver" ]; then
   printf " ❌ %s\n" "No previous zelta installs found in /tmp/zelta* "
   printf "   ***\n   *** %s\n   ***\n" "run shellspec test/00_install_spec.sh"
   return 1
fi

# extract the process number used when zelta install wsa created
last_tmp_process_number=$(echo "$last_tmp_installed_zelta_ver" | grep -o '[0-9]\+$')

#set -x
# use discovered zelta dir
export SANDBOX_ZELTA_TMP_DIR="$last_tmp_installed_zelta_ver"

# use discovered process number
export SANDBOX_ZELTA_PROCNUM="$last_tmp_process_number"

# standardized shellspec run environment depends on these env vars
# we use the last installed zelta and dbg area for shellspec
export ZELTA_BIN="$SANDBOX_ZELTA_TMP_DIR/bin"
export ZELTA_SHARE="$SANDBOX_ZELTA_TMP_DIR/share"
export ZELTA_ETC="$SANDBOX_ZELTA_TMP_DIR/etc"
export ZELTA_DOC="$SANDBOX_ZELTA_TMP_DIR/man"
export SHELLSPEC_TMPBASE=~/tmp/dbg_shellspecs
mkdir -p $SHELLSPEC_TMPBASE

# add the tmp zelta bin if not already on path
if ! echo ":$PATH:" | grep -q ":$ZELTA_BIN:"; then
  export PATH="$ZELTA_BIN:$PATH"
fi

# SHELLSPEC_PROJECT_ROOT is not currently being used in the zelta test env setup
# if that changes we'll need to address it here when we are creating a custom debug environment
echo "*** NOTE: SHELLSPEC_PROJECT_ROOT is not set, make sure it's not used!"

printf " ✅ %s\n\n" "using zelta $last_tmp_installed_zelta_ver with process number $last_tmp_process_number"
