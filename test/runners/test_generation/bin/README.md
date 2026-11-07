# Test Generation

Maintaining test correctness between versions of zelta is simplified by automatically 
generating the test files from a YAML definition file.

## Generate all tests
```shell
./generate_all_tests.sh
```

## Generating s specific test
```shell
./generate_test.sh (test-def-yml) (prod-location) (shellspec-setup)
```
### where:
- `test-def-yml`: YAML file defining the test located in `../config/test_defs`
- `prod-location`: on successful generation, move generated spec file to this directory
- `shellspec-setup`: shellspec setup instructions
   - shellspec-setup: `[options=value] (pattern=value|tag=value|path=value)...`   
      - `options=value`: defines `$options` for each shellspec invocation
      - `pattern=value`: invokes `shellspec $options --pattern "$value"`
      - `tag=value`:     invokes `shellspec $options --tag "$value"`
      - `path=value`:    invokes `shellspec $options "$value"`

### examples:
- review `generate_*_*_test.sh` scripts for examples

- - - 
# Shellspec Command Line Filtering Options 
> included from shellspec documentation for convenience 
## Line Numbers and IDs
```shell
shellspec path/to/a_spec.sh:10      # Run groups/examples that include line 10  
shellspec path/to/a_spec.sh:@1-5    # Run the 5th example in the 1st group  
shellspec a_spec.sh:10:@1:20:@2     # Mix multiple line numbers and IDs
```

## Pattern Filtering

File pattern: `-P, --pattern PATTERN` - Load files matching pattern (default: *_spec.sh) parser_definition.sh:206-207

Example pattern: `-E, --example PATTERN` - Run examples whose names include PATTERN parser_definition.sh:209-210

## Tag Filtering
Use -T, --tag TAG[:VALUE] to run examples with specified tags parser_definition.sh:212-213 :

```shell
shellspec --tag slow  
shellspec --tag tagA:val1,tagA:val2
```

## Path Filtering

Specify paths recursively with special prefixes (requires quotes)

```shell
shellspec "*/spec"               # Pattern "*/" matches 1 directory  
shellspec "**/spec"              # Pattern "**/" matches 0+ directories  
shellspec "*/*/**/test_spec.sh"  # Multiple patterns can be combined
```

## Focus Mode
Use the -F, --focus flag to run only focused groups/examples (those prefixed with f in the DSL) parser_definition.sh:203-204 :
```shell
shellspec --focus
```
- - - 
# Shellspec Hooks
> included from shellspec documentation for convenience

When to Use Each Hook Type

| Hook Type            | Best Used For                                                               |
|----------------------|-----------------------------------------------------------------------------|
| BeforeEach/AfterEach | Setting up and cleaning up test state that should be fresh for each example |
| BeforeAll/AfterAll   | Expensive operations needed once for a group of tests                       |
| BeforeCall/AfterCall | Environment setup for function calls                                        |
| BeforeRun/AfterRun   | Environment setup for command executions                                    |
| AfterMock            | Cleaning up mock functions                                                  |

## Hook Differences
- Each evaluation type has its own specific hooks:
   
### BeforeCall vs BeforeRun
- `BeforeCall / AfterCall` hooks run around call evaluations 
- `BeforeRun / AfterRun` hooks run around run evaluations

### BeforeAll and BeforeEach
- `BeforeAll and BeforeEach` do not use the run or call convention. They simply take a string containing shell code that is evaluated directly.
   - For example:
       ```
       # Define a function that runs your script
       setup_all() {  
       . /MyPath/MyBeforeAllScript.sh  
       }
        
       BeforeAll 'setup_all'
       ```
       Or simply:
       ```
       BeforeAll '. /MyPath/MyBeforeAllScript.sh'       
       ```
   - Alternatively, if you need to execute a script that doesn't need to update the current shell context:
        ```
        setup_all() {  
          /MyPath/MyBeforeAllScript.sh  
        }
        ```
## `call` vs `run` Implementation Details
- The call evaluation is handled by shellspec_around_call() which executes the function directly in the current shell context
- The run evaluation is handled by shellspec_around_run() which executes in a subshell environment

## Subtypes of run
- The run evaluation has specialized variants:
   - `run command` - runs external commands respecting shebang
   - `run script`  - runs shell scripts ignoring shebang
   - `run source` - sources scripts in current shell (similar to call but with script loading) README.md:946-976
