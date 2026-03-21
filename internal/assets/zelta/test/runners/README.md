# Test Runners

Test infrastructure for the Zelta ZFS backup tool, including environment setup and automated test generation.

## Overview

This directory provides:
- **Environment Management**: Scripts to configure and manage test ZFS pools and datasets
- **Test Generation**: Automated creation of ShellSpec tests from YAML definitions
- **Development Tools**: Helpers for debugging and iterating on tests

## Directory Structure

```
test/runners/
├── README.md                     # This file
├── doc/                          # Documentation
│   └── README_AliasHelpers.md    # Shell aliases for test workflows
│
├── env/                          # Environment setup
│   ├── test_env.sh               # Configure pools, datasets, remotes
│   ├── helpers.sh                # Common functions (setup_env, run_it, etc.)
│   ├── reset_env.sh              # Reset environment variables
│   ├── setup_debug_env.sh        # Debug environment setup
│   ├── set_reuse_tmp_env.sh      # Reuse existing /tmp/zelta* install
│   └── test_generator_cleanup.sh # Clean up after test generation
│
└── test_generation/              # Automated test generation (see below)
    ├── README.md                 # Detailed test generation docs
    ├── bin/                      # Entry point scripts
    ├── config/                   # YAML test definitions
    ├── lib/                      # Core implementation
    ├── scripts/                  # Utilities (shell, AWK)
    └── tmp/                      # Generated output
```

## Quick Start

### 1. Configure Test Environment

Edit your test pools and datasets:

```bash
vi env/test_env.sh
```

**Important**: The pools and datasets you configure will be destroyed and recreated by tests.

Example configuration:

```bash
export SANDBOX_ZELTA_SRC_POOL=apool
export SANDBOX_ZELTA_TGT_POOL=bpool
export SANDBOX_ZELTA_SRC_DS=apool/treetop
export SANDBOX_ZELTA_TGT_DS=bpool/backups

# Remote datasets and pools are optional and not needed for local tests
export SANDBOX_ZELTA_SRC_REMOTE=dever@zfsdev
export SANDBOX_ZELTA_TGT_REMOTE=dever@zfsdev
```

### 2. Run Tests

```bash
# From repo root
. test/runners/env/test_env.sh
shellspec
```

Tests use SANDBOX variables form `test_env.sh` to set up the required environment.

### 3. Generate New Tests

```bash
cd test/runners/test_generation
./bin/generate_new_tests.sh
```

See [test_generation/README.md](test_generation/README.md) for detailed documentation.

## Environment Scripts

### test_env.sh

Core configuration for test pools, datasets, and remotes. Source this file to set up environment variables:

```bash
. test/runners/env/test_env.sh
```

### helpers.sh

Common functions for environment management:

- `setup_env(DEBUG_MODE)` - Setup debug or standard environment
- `run_it(function_name)` - Run a function and report success/failure
- `clean_ds_and_pools()` - Destroy all test datasets and pools

### reset_env.sh

Resets environment variables by unsetting `SANDBOX_ZELTA_TMP_DIR`:

```bash
. test/runners/env/reset_env.sh
```

**Purpose**: Forces `test/test_helper.sh` to properly initialize all variables in the current ShellSpec process context when running `shellspec` from repo root.

**When to use**: Rarely needed manually - automatically called by `test_generator_cleanup.sh`. You might need it if you've been running test generation and want to ensure a clean state before running `shellspec`.

### setup_debug_env.sh

Configure environment for debugging without running full ShellSpec suite. Useful when manually testing commands or developing new tests.

### test_generator_cleanup.sh

Cleans up ZFS pools and datasets after test generation:

```bash
./env/test_generator_cleanup.sh
```

**When to use**: After completing test generation and copying all newly generated tests to `test/`. This script:
1. Calls `reset_env.sh` to reset environment variables
2. Destroys test pools and datasets
3. Prepares environment for normal test runs

**Important**: Run this after test generation is complete and before running the standard test suite with `shellspec`.

## Test Generation System

The `test_generation/` directory contains a complete system for automatically generating ShellSpec tests from YAML configurations.

### Key Features

- **YAML-driven**: Define tests declaratively
- **Automatic matchers**: Generates output validation functions
- **Smart substitution**: Handles environment variables, timestamps, dynamic values
- **End-to-end validation**: Tests are validated before deployment

### Basic Workflow

1. **Create YAML definition** with test cases
2. **Run generator** which executes commands and captures output
3. **Validate generated test** automatically
4. **Deploy to production** if validation passes

Example YAML:

```yaml
output_dir: tmp
shellspec_name: "040_zelta_tests_spec"
describe_desc: "Test zelta commands"

test_list:
  - test_name: backup_operation
    it_desc: performs backup - %{when_command}
    when_command: zelta backup "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
```

For complete documentation, see [test_generation/README.md](test_generation/README.md).

## Development Workflow

### Running Existing Tests

```bash
# Run all tests
shellspec

# Run specific test
shellspec test/040_zelta_tests_spec.sh
```

### Creating New Tests

#### Option 1: Write Manually

Create a new ShellSpec file in `test/`:

```bash
vi test/070_my_new_test_spec.sh
```

Follow existing test patterns.

#### Option 2: Generate from YAML

1. Create YAML definition:
   ```bash
   vi test/runners/test_generation/config/test_defs/070_my_test.yml
   ```

2. Add to generation script:
   ```bash
   vi test/runners/test_generation/bin/generate_new_tests.sh
   ```

3. Generate:
   ```bash
   cd test/runners/test_generation
   ./bin/generate_new_tests.sh
   ```

### Debugging Tests

#### Use Debug Environment

```bash
# Setup debug environment
. test/runners/env/setup_debug_env.sh

# Manually run zelta commands
zelta backup "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"

# Reset when done
. test/runners/env/reset_env.sh
```

#### Run Single Test

```bash
# Run just one test file
shellspec test/040_zelta_tests_spec.sh

# Run with detailed output
shellspec --format documentation test/040_zelta_tests_spec.sh
```

#### Examine Generated Tests

```bash
# View generated test
cat test/runners/test_generation/tmp/040_zelta_tests_spec.sh

# View generated matchers
cat test/runners/test_generation/tmp/output_for_*/output_for_*.sh
```

## Shell Aliases

For productivity, source the alias helpers:

```bash
. test/runners/doc/alias_setup.sh  # if this exists
```

Or add to your shell rc file. See [doc/README_AliasHelpers.md](doc/README_AliasHelpers.md) for available aliases.

Common aliases:
- `ztenv`   - source sandbox env from `test/runners/env/test_env.sh`
- `zdbgenv` - Setup debug environment
- `zcd`     - Change to repo root
- `zspect`  - Run shellspec tests with tracing
- `zspecd`  - Run shellspec tests with more detailed output
- `zclean`  - Remove test pools and datasets, clean slate
   - use `zclean` to reset env after test generation
   - use `ztenv`  to source sandbox env vars
   - now you can run `shellspec` normally

## Architecture

### Test Execution Flow

```
Manually setup up your SANDBOX env vars
use . env/test_env.sh or equivalent
    ↓
shellspec
    ↓
test/*_spec.sh (ShellSpec tests)
    ↓
test/test_helper.sh (setup)
    ↓
Creates/configures test pools
    ↓
Runs test commands
```

### Test Generation Flow

```
bin/generate_new_tests.sh
    ↓
lib/orchestration/generate_test.sh
    ↓
1. Setup ZFS tree
2. Run lib/ruby/test_generator.rb
   - Parse YAML
   - Execute commands
   - Generate matchers
   - Assemble test file
3. Validate generated test
4. Copy to production
```

### Path Resolution

- **Environment scripts**: Use relative paths (sourced from repo root)
- **Test generation scripts**: Use absolute paths (work from any directory)
- **Generated tests**: Use environment variables for portability

## Troubleshooting

### Tests Fail Immediately

Check that `env/test_env.sh` is configured correctly:

```bash
. test/runners/env/test_env.sh
echo $SANDBOX_ZELTA_SRC_POOL
echo $SANDBOX_ZELTA_TGT_POOL
```

### Pools Already Exist

If you've been running test generation, clean up:

```bash
./test/runners/env/test_generator_cleanup.sh
```

Then run tests normally:

```bash
shellspec  # Will recreate pools with fresh state
```

### Test Generation Fails

1. Verify YAML against schema:
   ```bash
   cd test/runners/test_generation
   ./bin/validate_yaml.rb config/test_defs/040_zelta_tests.yml
   ```

2. Check Ruby dependencies:
   ```bash
   cd test/runners/test_generation
   bundle install
   ```

3. Run with debug:
   ```bash
   ./bin/debug_gen.sh config/test_defs/040_zelta_tests.yml
   ```

### Generated Test Doesn't Match Output

The matcher may be too strict or environment variables not substituted correctly.

See [test_generation/README.md - Troubleshooting](test_generation/README.md#troubleshooting) for details.

## Related Documentation

- **[Test Generation Details](test_generation/README.md)** - Complete guide to automated test generation
- **[Alias Helpers](doc/README_AliasHelpers.md)** - Shell aliases for common workflows
- **[Main Test Directory](../)** - ShellSpec test files and test_helper.sh

## Design Principles

1. **Separation of Concerns**
   - `env/` - Environment configuration (sourced, repo-root relative)
   - `test_generation/` - Test generation (executable, path-independent)

2. **Single Source of Truth**
   - Test names come from YAML `shellspec_name`
   - Paths resolve from known anchors (repo root, script location)
   - Environment variables defined once in `env/test_env.sh`

3. **Reproducibility**
   - Environment can be reset to known state
   - Tests generate identically from same YAML
   - All paths absolute or explicitly resolved

4. **Developer Friendly**
   - Clear directory structure
   - Comprehensive documentation
   - Debug modes and helpers
