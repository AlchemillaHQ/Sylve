### Suggested alias helpers:
> NOTE: these aliases simplify the iterative process of setting up 
> test environments for manual checks of zelta commands, and 
> for resetting the environment to run from a clean slate. 

```shell
# shellspec test development aliases


# set to your repo location for zelta
ZELTA_REPO_ROOT=~/src/repos/bt/zelta      # local repo location 

# env helpers for zelta testing
ZELTA_ENV="$ZELTA_REPO_ROOT/test/runners/env"

# zelta test generation helper directory
ZELTA_TEST_GEN="$ZELTA_REPO_ROOT/test/runners/test_generation"


# show all the aliases we've setup for zelta testing
alias zhlp="alias | grep 'z'"
alias zcd="cd $ZELTA_REPO_ROOT"
alias ecd="cd $ZELTA_ENV"
alias gcd="cd $ZELTA_TEST_GEN"


# NOTE: the aliases work from the context of the zelta repo, use zcd before action

# run shellspec with trace and evaluation
# note: macOS requires homebrew bash, use bash shell for your env
BASH_SH=/opt/homebrew/bin/bash
alias zspect="zcd && shellspec --xtrace --shell $BASH_SH"

# runs shellspec with document format output
alias zspecd="zcd && shellspec --format document"

# update current environment to allow running commands from the command line
# in the context of the current debug zfs tree state
alias zdbgenv="zcd && . $ZELTA_ENV/helpers.sh && setup_env 1"

# force a clean up of pools, datasets and remotes
alias zclean="zcd && $ZELTA_ENV/test_generator_cleanup.sh"

# force next evaluation of test/test_helpers.sh to initialize env fully
alias zrenv="zcd && . $ZELTA_ENV/reset_env.sh"


# setup env vars for your test environment
# setup pools, datasets and remotes env vars
alias ztenv="zcd && . $ZELTA_ENV/test_env.sh"
```
