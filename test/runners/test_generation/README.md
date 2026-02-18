# Test Generation System

Automated generation of ShellSpec test files from YAML configurations for the Zelta ZFS backup tool.

## Overview

This system generates complete ShellSpec test files by:
1. Reading test definitions from YAML configuration files
2. Executing commands and capturing their output
3. Generating matcher functions that validate command output
4. Assembling complete ShellSpec test files with proper structure

## Directory Structure

```
test_generation/
├── bin/                          # Entry point scripts
│   ├── generate_new_tests.sh     # Generate multiple tests (040, 050, 060)
│   └── debug_gen.sh              # Debug test generation
├── config/
│   ├── test_defs/                # YAML test definitions
│   │   ├── 040_zelta_tests.yml
│   │   ├── 050_zelta_revert_test.yml
│   │   └── 060_zelta_clone_test.yml
│   └── test_config_schema.yml    # YAML validation schema
├── lib/
│   ├── ruby/                     # Core Ruby implementation
│   │   ├── test_generator.rb     # Main test generator class
│   │   ├── sys_exec.rb           # Command execution with timeout
│   │   ├── placeholders.rb       # Variable substitution
│   │   └── .rubocop.yml          # Ruby style config
│   └── orchestration/            # Shell orchestration scripts
│       ├── generate_test.sh      # Test generation workflow
│       └── setup_tree.sh         # ZFS tree setup
├── scripts/
│   ├── sh/                       # Shell utilities
│   │   ├── generate_matcher.sh   # Generate matcher functions
│   │   └── matcher_func_generator.sh
│   └── awk/                      # AWK text processing
│       └── generate_case_stmt_func.awk
├── tmp/                          # Generated test output
└── Gemfile                       # Ruby dependencies

```

## Quick Start

### Prerequisites

```bash
# Install Ruby dependencies
cd test/runners/test_generation
bundle install

# Configure your test environment
vi ../env/test_env.sh  # Set pools, datasets, remotes
```

### Generate Tests

```bash
# Generate all configured tests
./bin/generate_new_tests.sh
```

## YAML Test Configuration

### Basic Structure

```yaml
output_dir: tmp
shellspec_name: "040_zelta_tests_spec"
describe_desc: "Run zelta commands on divergent tree"

skip_if_list:
  - condition: if 'SANDBOX_ZELTA_SRC_DS undefined' test -z "$SANDBOX_ZELTA_SRC_DS"

test_list:
  - test_name: match_after_divergence
    it_desc: show divergence - %{when_command}
    when_command: zelta match "$SANDBOX_ZELTA_SRC_EP" "$SANDBOX_ZELTA_TGT_EP"
```

### Advanced Features

#### Setup Scripts

Source helper scripts before running commands:

```yaml
test_list:
  - test_name: add_and_remove_src_datasets
    setup_scripts:
      - "test/test_helper.sh"  # Paths relative to repo root
    when_command: add_tree_delta
```

#### Commands with No Output

For commands that shouldn't produce output:

```yaml
test_list:
  - test_name: cleanup_operation
    allow_no_output: true
    when_command: zelta cleanup "$SANDBOX_ZELTA_SRC_EP"
```

#### Variable Substitution

Use `%{variable}` syntax in descriptions:

```yaml
it_desc: "backup after rotate - %{when_command}"
```

## How It Works

### 1. Test Generation Workflow

```
generate_test.sh
    ↓
1. Setup ZFS tree (via setup_tree.sh)
    ↓
2. Run test_generator.rb
    ↓
    - Parse YAML config
    - For each test:
        a. Execute command
        b. Capture stdout/stderr
        c. Generate matcher function
        d. Apply env var substitutions
        e. Create ShellSpec test clause
    ↓
3. Setup ZFS tree again
    ↓
4. Run generated test with shellspec
    ↓
5. If passes, copy to production (test/)
```

### 2. Matcher Function Generation

The system automatically generates matcher functions that:
- Normalize whitespace in output
- Replace environment variable values with variable references
- Replace timestamps with wildcards
- Create case statements for output validation

Example generated matcher:

```bash
output_for_match_after_divergence() {
  while IFS= read -r line; do
    normalized=$(echo "$line" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$normalized" in
      "backing up from ${SANDBOX_ZELTA_SRC_EP}"|\
      "target ${SANDBOX_ZELTA_TGT_EP}"|\
      "* sent, * streams received in * seconds")
        ;;
      *)
        printf "Unexpected line: %s\n" "$line" >&2
        return 1
        ;;
    esac
  done
}
```

### 3. Output Processing

The generator automatically handles:
- **Environment variables**: Replaces actual values with `${VAR_NAME}` references
- **Timestamps**: Converts `@zelta_2024-01-15_10.30.45` to `@zelta_"*"`
- **Dynamic values**: Wildcards for transfer speeds, stream counts, etc.
- **Backticks**: Proper escaping for shell commands

## Creating New Tests

### Step 1: Create YAML Definition

```bash
vi config/test_defs/070_my_new_test.yml
```

### Step 2: Define Test Structure

```yaml
output_dir: tmp
shellspec_name: "070_my_new_test_spec"
describe_desc: "Test my new feature"

skip_if_list:
  - condition: if 'required var' test -z "$REQUIRED_VAR"

test_list:
  - test_name: first_operation
    it_desc: performs operation - %{when_command}
    when_command: zelta mycommand "$SANDBOX_ZELTA_SRC_EP"
```

### Step 3: Add to Generation Script

Edit `bin/generate_new_tests.sh`:

```bash
if ! "$GENERATE_TEST" \
 "$CONFIG_DIR/070_my_new_test.yml" \
 "test/01*_spec.sh|test/02*_spec.sh|test/040_*_spec.sh"; then

  printf "\n ❌ Failed to generate 070 test\n"
  exit 1
fi
```

### Step 4: Generate and Test

```bash
# Generate the test
./bin/generate_new_tests.sh

# Generated test is now in test/070_my_new_test_spec.sh
```

## Environment Variables

The generator recognizes these environment variables for substitution:
- `SANDBOX_ZELTA_SRC_DS` - Source ZFS dataset
- `SANDBOX_ZELTA_TGT_DS` - Target ZFS dataset
- `SANDBOX_ZELTA_SRC_EP` - Source endpoint (remote:dataset)
- `SANDBOX_ZELTA_TGT_EP` - Target endpoint (remote:dataset)

Add more in `test_generator.rb`:

```ruby
DEFAULT_ENV_VAR_NAMES = 'VAR1:VAR2:VAR3'
```

## Debugging Generated Tests

The `bin/debug_gen.sh` script helps you iteratively debug and verify generated tests. This is especially useful when test generation succeeds but the generated test fails validation.

### Using debug_gen.sh

1. **Edit the script** to configure your debug session:
   ```bash
   vi bin/debug_gen.sh
   ```

2. **Set the required variables**:
   ```bash
   # Tests to run for tree setup (pipe-separated globs)
   SPECS="test/01*_spec.sh|test/02*_spec.sh|test/040_*_spec.sh"

   # The generated test you're debugging
   NEW_SPEC="$TEST_GEN_DIR/tmp/050_zelta_revert_spec.sh"
   ```

3. **Optionally configure trace mode**:
   ```bash
   # Show detailed trace (helpful for debugging)
   TRACE_OPTIONS="--xtrace --shell /opt/homebrew/bin/bash"

   # Or disable trace for cleaner output
   #unset TRACE_OPTIONS
   ```

4. **Run the debug script**:
   ```bash
   ./bin/debug_gen.sh
   ```

### What debug_gen.sh Does

1. Sets up ZFS tree by running the specified setup tests (`SPECS`)
2. Configures debug environment (sources `env/setup_debug_env.sh`)
3. Runs your generated test (`NEW_SPEC`) with optional trace
4. Reports success or failure

### Why Edit Instead of Arguments?

Debugging is iterative - you'll likely run the script multiple times while tweaking:
- YAML test definitions
- Tree setup steps
- Environment configuration

Editing the script ensures you don't accidentally use wrong values and makes it easy to quickly re-run after changes.

## Troubleshooting

### Test Generation Fails

```bash
# Check Ruby syntax
ruby -c lib/ruby/test_generator.rb

# Validate YAML against schema
./bin/validate_yaml.rb config/test_defs/040_zelta_tests.yml
```

### Generated Test Fails During Generation

Use `debug_gen.sh` to investigate:

1. Generate the test (it will fail validation)
2. Edit `bin/debug_gen.sh` with appropriate SPECS and NEW_SPEC
3. Run `./bin/debug_gen.sh` to see detailed failure
4. Fix YAML definition or matcher expectations
5. Regenerate and test again

### Generated Test Fails When Run Manually

1. Check the generated test: `cat tmp/040_zelta_tests_spec.sh`
2. Check matcher functions: `cat tmp/output_for_*/output_for_*.sh`
3. Run the test manually: `shellspec tmp/040_zelta_tests_spec.sh`
4. Verify ZFS tree state matches expectations

### Matcher Doesn't Match Output

The matcher is too strict. Common issues:
- Output has extra whitespace (matcher normalizes this)
- Environment variable not in substitution list
- Timestamp format changed
- Dynamic values not wildcarded

Regenerate with updated env var list or edit matcher manually.

## Architecture

### Path Resolution

All paths use absolute resolution anchored to:
- **Repo root**: `git rev-parse --show-toplevel`
- **Test generation dir**: `File.expand_path(File.join(__dir__, '..', '..'))`

This ensures scripts work regardless of current directory.

### Single Source of Truth

- **Test name**: Defined in YAML `shellspec_name` field
- **Output directory**: Resolved relative to test_generation dir
- **Setup scripts**: Resolved relative to repo root
- **Config paths**: Resolved relative to test_generator.rb location

### Code Organization

- **bin/**: User-facing entry points
- **lib/ruby/**: Core logic (Ruby)
- **lib/orchestration/**: Workflow management (Shell)
- **scripts/**: Utilities (Shell, AWK)
- **config/**: Test definitions and schema

## See Also

- [Test Runners Overview](../README.md)
- [Environment Setup](../env/)
- [Main Test Directory](../../)
