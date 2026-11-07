# frozen_string_literal: true

require_relative 'shell_spec_gen'
require_relative 'placeholders'
require_relative 'spec_writer'
require_relative 'path_config'
require_relative 'sys_exec'
require_relative 'file_helper'
require_relative 'shellspec_runner'

class TestGenerator
  TIMEOUT_SECONDS = 120

  TEST_ENV_SETUP = '. ./test/runners/env/test_env.sh; TESTGEN_ZELTA_RECORD=1'
  attr_reader :paths, :config, :spectest_def_yml_file

  def initialize(spectest_def_yml_file)
    @spectest_def_yml_file = spectest_def_yml_file
    @config = ShellSpecGen::Config.load_file(spectest_def_yml_file)
    @paths = PathConfig.new(config.output_dir, config.shellspec_name)
    raise 'Failed to create output directory' unless File.directory?(@paths.output_path)
  end

  # generate shellspec example file corresponding to the test definition
  def generate_shellspec_test
    puts "\n#{'-' * 80}"
    puts '-- Generating shellspec test output'
    puts '-' * 80
    generate_shellspec_output   # generate shellspec output
    puts "\n#{'-' * 80}"
    puts '-- Generating final shellspec test from output'
    puts '-' * 80
    generate_final_spec         # generate final spec file from output
  end

  def verify_final_specfile(verified_files_dir_option = nil)
    cmd = "cd #{PathConfig.repo_root}; #{TEST_ENV_SETUP} shellspec #{@paths.final_spec_path}"
    puts "Running shellspec command: #{cmd}"

    # throws on error
    SysExec.run(cmd, timeout: TIMEOUT_SECONDS)
    puts 'Final spec file verified successfully, moving to verified files directory'

    puts "Verified files directory: #{@paths.default_verified_files_path}"
    puts "Verified files directory option: #{verified_files_dir_option}"
    verified_files_path = verified_files_dir_option || @paths.default_verified_files_path
    puts "Final verified spec file path: #{verified_files_path}"
    # throws
    FileHelper.move_file_to_dir(@paths.final_spec_path, verified_files_path)

    final_spec_path = File.join(verified_files_path, File.basename(@paths.final_spec_path))
    success = File.exist?(final_spec_path)

    if success
      puts "Final verified spec file: #{final_spec_path}"
    else
      puts "Final verified spec file not found: #{final_spec_path}"
    end
    success
  end

  private

  # use shellspec to generate the output for the tests
  def generate_shellspec_output
    SpecWriter.write_output_spec(@config, @paths)
    cmd = "cd #{PathConfig.repo_root}; #{TEST_ENV_SETUP} shellspec #{@paths.output_spec_path}"
    puts "Running shellspec command: #{cmd}"
    # throws on error
    SysExec.run(cmd, timeout: TIMEOUT_SECONDS)
  end

  # generate the final spec file using the captured output
  def generate_final_spec
    SpecWriter.write_prod_spec(@config, @paths)
  end
end
