#!/bin/sh

SPEC_SETUP_DIR="test/spec/0000_setup"
# Debug environment setup
#   To facilitate creating and manually shellspec tests, and debugging existing tests
#   use the last spec installed zelta version
#   if no previous zelta install is found in /tmp, show user how to create one

# find the last installed version of zelta installed by shellspec
last_installed_fullpath=$(ls -1d /tmp/zelta_sandbox* | tail -1)

# exit if no previous install found
if [ -z "$last_installed_fullpath" ]; then
   printf " ❌ %s\n" "No previous zelta installs found in /tmp/zelta* "
   printf "   ***\n   *** %s\n   ***\n" "run shellspec ${SPEC_SETUP_DIR}/00_install_spec.sh"
   return 1
fi

# use discovered zelta dir
SANDBOX_ZELTA_TMP_PREFIX=zelta_sandbox
last_installed_dir=${last_installed_fullpath##*/}

export SANDBOX_ZELTA_TMP_SUFFIX=${last_installed_dir#${SANDBOX_ZELTA_TMP_PREFIX}_}

# SHELLSPEC_PROJECT_ROOT is not currently being used in the zelta test env setup
# if that changes we'll need to address it here when we are creating a custom debug environment
echo "*** NOTE: SHELLSPEC_PROJECT_ROOT is not set, make sure it's not used!"

printf " ✅ %s\n\n" "using zelta $last_tmp_installed_zelta_ver having suffix {$SANDBOX_ZELTA_TMP_SUFFIX}"
