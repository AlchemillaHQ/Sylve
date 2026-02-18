#!/usr/bin/env ruby
# frozen_string_literal: true

# Manage generation of ShellSpec test files from YAML configuration

require 'English'
require 'json-schema'
require 'yaml'
require 'fileutils'
require 'time'
require_relative 'placeholders'
require_relative 'sys_exec'

# TestGenerator - Generates ShellSpec test files from YAML configuration
class TestGenerator
  REPO_ROOT = `git rev-parse --show-toplevel`.strip
  TEST_GEN_DIR = File.expand_path(File.join(__dir__, '..', '..'))
  GENERATE_MATCHER_SH_SCRIPT = File.join(TEST_GEN_DIR, 'scripts', 'sh', 'generate_matcher.sh')

  # TODO: determine if additional defaults are needed, as more tests are added, we may need to update this
  DEFAULT_ENV_VAR_NAMES = 'SANDBOX_ZELTA_TGT_DS:SANDBOX_ZELTA_SRC_DS:SANDBOX_ZELTA_TGT_EP:SANDBOX_ZELTA_SRC_EP'

  private_constant :REPO_ROOT, :TEST_GEN_DIR, :GENERATE_MATCHER_SH_SCRIPT

  attr_reader :config, :output_dir, :shellspec_name, :describe_desc, :test_list, :skip_if_list,
              :matcher_files, :wip_file_path, :final_file_path, :env_var_names, :sorted_env_map

  def initialize(yaml_file_path, env_var_names = DEFAULT_ENV_VAR_NAMES)
    # Resolve path relative to this file's directory if it's a relative path
    yaml_file_path = File.expand_path(yaml_file_path, __dir__) unless yaml_file_path.start_with?('/')

    raise "YAML file not found: #{yaml_file_path}" unless File.exist?(yaml_file_path)

    @config = YAML.load_file(yaml_file_path)
    validate_config!

    @shellspec_name = @config['shellspec_name']
    @describe_desc = @config['describe_desc']

    # Resolve output_dir relative to test_generation directory
    output_dir = @config['output_dir']
    @output_dir = output_dir.start_with?('/') ? output_dir : File.join(TEST_GEN_DIR, output_dir)

    @test_list = @config['test_list'] || []
    @skip_if_list = @config['skip_if_list'] || []
    @matcher_files = []
    @wip_file_path = File.join(@output_dir, "#{@shellspec_name}_wip.sh")
    # remove _spec to prevent shellspec from finding the WIP file
    @wip_file_path.sub!('_spec', '')
    @final_file_path = File.join(@output_dir, "#{@shellspec_name}.sh")
    @env_var_names = env_var_names
    @sorted_env_map = build_sorted_env_map
    puts "Loading configuration from: #{@config.inspect}\n"
    puts '=' * 60
  end

  def generate
    create_output_directory
    create_wip_file
    process_tests
    assemble_final_file
    report_summary
  end

  private

  def build_sorted_env_map
    # Parse and sort env vars by value length (descending)
    env_map = @env_var_names.split(':').each_with_object({}) do |name, hash|
      hash[name] = ENV[name] if ENV[name]
    end

    # Sort by value length descending to replace longest matches first
    env_map.sort_by { |_name, value| -value.length }
  end

  def matcher_func_name(test_name)
    "output_for_#{test_name}"
  end

  def validate_config!(schema_path = File.join(TEST_GEN_DIR, 'config', 'test_config_schema.yml'))
    schema = YAML.load_file(schema_path)
    JSON::Validator.validate!(schema, @config)
  end

  def create_output_directory
    FileUtils.mkdir_p(@output_dir)
    puts "Created output directory: #{@output_dir}"
  end

  def create_wip_file
    File.open(@wip_file_path, 'w') do |file|
      file.puts "Describe '#{@describe_desc}'"

      # Add Skip If statements for each condition
      @skip_if_list.each do |skip_item|
        file.puts "  Skip #{skip_item['condition']}"
      end
      file.puts '' unless @skip_if_list.empty?
    end
    puts "Created WIP file: #{@wip_file_path}"
  end

  def process_tests
    @test_list.each do |test|
      test_name = test['test_name']
      # allow var substitution in test description
      it_desc = Placeholders.substitute(test['it_desc'], test, inclusions: [:when_command])

      when_command = test['when_command']
      setup_scripts = test['setup_scripts'] || []
      allow_no_output = test['allow_no_output'] || false

      puts "Processing test: #{test_name}"

      # Generate matcher files
      generate_matcher_files(test_name, when_command, setup_scripts, allow_no_output)

      # Append It clause to WIP file
      append_it_clause(test_name, it_desc, when_command, allow_no_output)
    end

    # Close Describe block
    File.open(@wip_file_path, 'a') do |file|
      file.puts 'End'
    end
  end

  def generate_matcher_files(test_name, when_command, setup_scripts, allow_no_output)
    matcher_script = GENERATE_MATCHER_SH_SCRIPT
    matcher_function_name = matcher_func_name(test_name)

    unless File.exist?(matcher_script)
      puts "Warning: Matcher generator script not found: #{matcher_script}"
      return
    end

    # Build command with optional setup scripts
    full_command = build_command_with_setup(when_command, setup_scripts)

    # Add allow_no_output flag
    allow_no_output_flag = allow_no_output ? "true" : "false"

    cmd = "#{matcher_script} \"#{full_command}\" #{matcher_function_name} #{@output_dir} #{allow_no_output_flag}"
    SysExec.run(cmd, timeout: 10)

    unless allow_no_output
      # Track the generated matcher file
      func_name = matcher_func_name(test_name)
      matcher_file = File.join(@output_dir, func_name, "#{func_name}.sh")

      # Post-process the matcher file to apply env substitutions
      if File.exist?(matcher_file)
        post_process_matcher_file(matcher_file)
        puts "Generated matcher file: #{matcher_file}"
        @matcher_files << matcher_file
      end
    end
  end

  def post_process_matcher_file(matcher_file)
    # Read the matcher file and apply env substitutions to case statement patterns
    content = File.read(matcher_file)
    lines = content.lines

    # Process each line
    processed_lines = lines.map do |line|
      # Only process lines that look like case patterns (contain quoted strings)
      if line =~ /^\s*".*"(?:\)|\|\\)$/
        # Extract the quoted content, normalize it, and reconstruct the line
        if line =~ /^(\s*)"(.*)"(\)|\|\\)$/
          indent = $1
          pattern = $2
          suffix = $3
          normalized = normalize_output_line(pattern)
          "#{indent}\"#{normalized}\"#{suffix}\n"
        else
          line
        end
      else
        line
      end
    end

    # Write back the processed content
    File.write(matcher_file, processed_lines.join)
  end

  def build_command_with_setup(when_command, setup_scripts)
    return when_command if setup_scripts.empty?

    # Resolve relative script paths to absolute paths relative to repo root
    resolved_scripts = setup_scripts.map do |script|
      if script.start_with?('/')
        script
      else
        File.join(REPO_ROOT, script)
      end
    end

    # Build source commands for each setup script (using . for POSIX compatibility)
    source_cmds = resolved_scripts.map { |script| ". #{script}" }

    # Combine all source commands with the actual command
    "#{source_cmds.join(' && ')} && #{when_command}"
  end

  def append_it_clause(test_name, it_desc, when_command, allow_no_output)
    File.open(@wip_file_path, 'a') do |file|
      file.puts "  It \"#{it_desc.gsub('"', '\\"')}\""

      func_name = matcher_func_name(test_name)

      # TODO: clean up all the trial and error with shellspec error output, documented approaches don't work!
     # Check for stderr output
      stderr_file = File.join(@output_dir, func_name, "#{func_name}_stderr.out")
      expected_error = nil
      if File.exist?(stderr_file) && !File.zero?(stderr_file)
        expected_error = format_expected_error(stderr_file)
        #file.puts expected_error
        #status_line = '    The status should be failure'
      else
        #status_line = '    The status should equal 0'
      end

      # TODO: zelta exits with 0 even when there is error output
      #status_line = '    The status should equal 0'
      status_line = '    The status should be success'

      file.puts "    When call #{when_command}"

      file.puts "    The output should satisfy #{matcher_func_name(test_name)}" unless allow_no_output

      file.puts "    The error should equal \"#{expected_error}\"\n" if expected_error
      file.puts status_line

      file.puts '  End'
      file.puts ''
    end
  end

  def v1_format_expected_error(stderr_file)
    lines = read_stderr_file(stderr_file)
    result = "    expected_error=%text\n"
    lines.each do |line|
      result += "    #|#{line}\n"
    end
    "#{result}    End\n"
  end
  # expected_error() { %text
  # #|warning: insufficient snapshots; performing full backup for 3 datasets
  # #|warning: missing `zfs allow` permissions: readonly,mountpoint
  # }
  def v2_format_expected_error(stderr_file)
    lines = read_stderr_file(stderr_file)
    result = "    expected_error() { %text\n"
    lines.each do |line|
      result += "    #|#{line}\n"
    end
    "#{result}    }\n"
  end

  def format_expected_error(stderr_file)
    lines = read_stderr_file(stderr_file)
    lines.map! { |line| normalize_output_line(line) }
    lines.join("\n")
  end

  def normalize_output_line(line)
    # Normalize whitespace
    normalized = line.gsub(/\s+/, ' ').strip

    # Replace timestamp patterns (both @zelta_ and _zelta_ prefixes)
    normalized.gsub!(/@zelta_\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}/, '@zelta_"*"')
    normalized.gsub!(/_zelta_\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}/, '_zelta_"*"')

    # Escape backticks
    normalized.gsub!('`', '\\\`')

    # Wildcard time and quantity sent
    if normalized =~ /(\d+[KMGT]? sent, )(\d+ streams)( received in \d+\.\d+ seconds)/
      stream_count = $2
      normalized.gsub!(/\d+[KMGT]? sent, \d+ streams received in \d+\.\d+ seconds/,
                      "* sent, #{stream_count} received in * seconds")
    end

    # Substitute env var names for values (longest first)
    # Use a placeholder to prevent already-substituted values from being re-matched
    placeholder_map = {}
    @sorted_env_map.each_with_index do |(name, value), idx|
      placeholder = "__ENV_PLACEHOLDER_#{idx}__"
      normalized.gsub!(value, placeholder)
      placeholder_map[placeholder] = "${#{name}}"
    end

    # Replace placeholders with actual env var references
    placeholder_map.each do |placeholder, replacement|
      normalized.gsub!(placeholder, replacement)
    end

    normalized
  end

  def read_stderr_file(stderr_file)
    File.readlines(stderr_file).map(&:chomp)
  rescue StandardError => e
    puts "Warning: Could not read stderr file #{stderr_file}: #{e.message}"
    []
  end

  def assemble_final_file
    File.open(@final_file_path, 'w') do |final|
      final.puts '# Auto-generated ShellSpec test file'
      final.puts "# Generated at: #{Time.now}"
      final.puts "# Source: #{@shellspec_name}"
      final.puts '# WARNING: This file was automatically generated. Manual edits may be lost.'
      final.puts ''

      # Copy all matcher function files
      @matcher_files.each do |matcher_file|
        if File.exist?(matcher_file)
          final.puts File.read(matcher_file)
          final.puts ''
        end
      end

      # Copy the WIP file content
      final.puts File.read(@wip_file_path) if File.exist?(@wip_file_path)
    end
    puts "Assembled final test file: #{@final_file_path}"
  end

  def report_summary
    puts "\n#{'=' * 60}"
    puts 'Test Generation Summary'
    puts '=' * 60
    puts "YAML Configuration: #{@config.inspect}"
    puts "ShellSpec Name: #{@shellspec_name}"
    puts "Description: #{@describe_desc}"
    puts "Output Directory: #{@output_dir}"
    puts "Tests Processed: #{@test_list.length}"
    puts "Matcher Files Generated: #{@matcher_files.length}"
    puts "\nGenerated Files:"
    puts "  - WIP File: #{@wip_file_path}"
    @matcher_files.each do |file|
      puts "  - Matcher: #{file}"
    end
    puts "\nFinal ShellSpec Test File:"
    puts "  Location: #{@final_file_path}"
    puts '=' * 60
    puts "__SHELLSPEC_NAME__:#{@shellspec_name}"
  end
end

def run_generator
  if ARGV.empty?
    puts "Usage: #{$PROGRAM_NAME} <yaml_config_file>"
    puts "\nExample YAML format:"
    puts <<~YAML
      shellspec_name: example_tests
      describe_desc: Example Zelta Command Tests
      output_dir: test/output
      test_list:
        - test_name: test_version
          it_desc: should display version information
          when_command: zelta --version
        - test_name: test_help
          it_desc: should display help message
          when_command: zelta --help
    YAML
    return 1
  end

  yaml_file = ARGV[0]
  generator = TestGenerator.new(yaml_file)
  generator.generate
  0
end


# Script execution
run_generator if __FILE__ == $PROGRAM_NAME
