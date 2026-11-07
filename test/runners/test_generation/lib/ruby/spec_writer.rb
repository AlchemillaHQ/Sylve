# frozen_string_literal: true
require 'fileutils'
require_relative 'shell_spec_gen'
require_relative 'path_config'
require_relative 'case_stmt_func_generator'

class SpecWriter
  include ShellSpecGen

  def self.write_output_spec(config, paths)
    new(config, paths, true).create_output_spec_file
  end

  def self.write_prod_spec(config, paths)
    new(config, paths, false).create_prod_spec_file
  end

  attr_reader :config, :paths, :capture_output_only

  def initialize(config, paths, capture_output_only)
    @paths = paths
    @config = config
    @capture_output_only = capture_output_only
  end

  def create_prod_spec_file
    filename = paths.final_spec_path
    File.open(filename, 'w') do |file|
      write_prod_header(file)
      create_spec_file(file)
    end
    puts "Output prod file written to #{filename}"
    File.exist?(filename)
  end

  def create_output_spec_file
    output_spec_path = paths.output_spec_path
    File.open(output_spec_path, 'w') do |file|
      create_spec_file(file)
    end
    puts "Output spec file written to #{output_spec_path}"
    File.exist?(output_spec_path)
  end


  private

  def create_spec_file(file)
    config => { multi_desc:, multi_tag:, example_list:, shell_code: }

    unless shell_code.nil?
      file.puts shell_code
      file.puts ''
    end

    indent = ''
    add_outer_describe_block = (example_list.length > 1)

    if add_outer_describe_block
      # Open outermost Describe block
      file.puts "Describe #{multi_desc} #{ multi_tag ? " #{multi_tag}" : ''}"
      indent = '  '
    end

    example_list.each_with_index do |example, index|
      process_example(file, example, indent)
      process_tests(file, example.test_list, indent)

      # close Describe block
      file.puts 'End'
      indent = '  ' * (index + 1)
    end

    return unless add_outer_describe_block

    # Close outermost Describe block
    file.puts 'End'
  end

  def auto_generated_notice
    <<~NOTICE
      # Auto-generated ShellSpec test file
      # Generated at: #{Time.now}
      # Source: #{@paths.shellspec_name}
      # WARNING: This file was automatically generated. Manual edits may be lost.

    NOTICE
  end

  def write_prod_header(file)
    file.puts auto_generated_notice
    config.example_list.each do |example|
      create_test_matcher_functions(file, example.test_list)
    end
  end

  def capture_output_clause(subdir, test_name, indent)
    <<~CLAUSE
      #{indent}    if testgen_record_only #{subdir} #{test_name}; then
      #{indent}      The output should satisfy noop
      #{indent}      The error  should satisfy noop
      #{indent}      The status should satisfy noop
      #{indent}    else
      #{indent}      The output should satisfy noop
      #{indent}      The error  should satisfy noop
      #{indent}      The status should satisfy noop
      #{indent}    fi
    CLAUSE
  end

  def matcher_func_name(test_name)
    "output_for_#{test_name}"
  end

  def add_expected_output_clause(file, test_name, allow_no_output, output_clause, indent)
    # Check for stderr output
    func_name = matcher_func_name(test_name)
    file.puts "#{indent}    The output should satisfy #{func_name}" unless allow_no_output
    file.puts "#{indent}    #{output_clause}" unless output_clause.nil?
    add_error_lines(file, test_name, indent)
    file.puts status_line(test_name, indent)
  end

  def add_error_lines(file, test_name, indent)
    stderr_filename = @paths.testdef_stderr_path(test_name)
    if File.exist?(stderr_filename) && !File.zero?(stderr_filename)
      expected_error = CaseStmtFuncGenerator.format_expected_error(stderr_filename)
    end
    # NOTE: this style of checking error output was the only one that worked for me, inline equal
    file.puts "#{indent}    The error should equal \"#{expected_error}\"\n" if expected_error
  end

  def status_line(test_name, indent)
    status_filename = @paths.testdef_status_path(test_name)
    raise "Status file #{status_filename} not found" unless File.exist?(status_filename)

    puts "Status file: #{status_filename}"
    status = File.read(status_filename).strip
    if status == '0'
      "#{indent}    The status should be success"
    else
      "#{indent}    The status should be failure"
    end
  end

  def append_it_clause(file, indent, test_def)
    test_def => { when_command:, test_name:, allow_no_output:, it_desc:, tag:, output_clause: }

    it_desc = Placeholders.substitute(it_desc, test_def, inclusions: [:when_command])
    file.puts "#{indent}  It \"#{it_desc.gsub('"', '\\"')}\"#{ tag ? " #{tag}" : ''}"

    file.puts "#{indent}    When #{when_command}"
    if capture_output_only
      file.puts capture_output_clause(@paths.shellspec_name, test_name,indent)
    else
      add_expected_output_clause(file, test_name, allow_no_output, output_clause, indent)
    end
    # end It clause
    file.puts "#{indent}  End\n\n"
  end

  def create_test_matcher_functions(file, test_list)
    test_list.each do |test_def|
      next if test_def.allow_no_output

      stdout_filename = @paths.testdef_stdout_path(test_def.test_name)
      func_name = matcher_func_name(test_def.test_name)
      # TODO pass in clean_output
      file.puts CaseStmtFuncGenerator.matcher_function_from_file(stdout_filename, func_name, test_def.remove_timestamps)
    end
  end

  def process_tests(file, test_list, indent)
    test_list.each do |test_def|
      test_def => { test_name: }
      puts "Processing test: #{test_name}"
      # Append It clause to WIP file
      append_it_clause(file, indent, test_def)
    end
  end

  def process_example(file, example, indent)
    example => { describe_desc:, tag:, skip_if_list:, hooks_list:, shell_code:}
    file.puts "#{indent}Describe '#{describe_desc}'#{ tag ? " #{tag}" : ''}"

    unless shell_code.nil?
      shell_code.each_line { |line| file.puts "#{indent}  #{line.chomp}" }
      file.puts ''
    end

    skip_if_list.each do |skip_item|
      file.puts "  Skip #{skip_item.condition}"
    end
    file.puts '' unless skip_if_list.empty?

    # Add defined hooks
    # TODO: consider removing hook.invocation, not used currently
    hooks_list.each do |hook|
      file.puts "  #{hook.name}  #{hook.command}"
    end
    file.puts '' unless hooks_list.empty?
  end
end
