# frozen_string_literal: true

require_relative 'sys_exec'
require_relative 'test_generator'

class ShellspecRunner
  TIMEOUT_SECONDS = 120

  attr_reader :setup_cmds, :teardown_cmds, :yaml_test_def_path

  def initialize(setup_cmds, teardown_cmds, yaml_test_def_path)
    setup_cmds_with_env = []
    setup_cmds.each do |cmd|
      setup_cmds_with_env << ". #{PathConfig.test_env_setup_path} && #{cmd}"
    end
    teardown_cmds_with_env = []
    teardown_cmds.each do |cmd|
      teardown_cmds_with_env << ". #{PathConfig.test_env_setup_path} && #{cmd}"
    end
    @setup_cmds = setup_cmds_with_env
    @yaml_test_def_path = yaml_test_def_path
    @teardown_cmds = teardown_cmds_with_env
  end

  def generate_test(verified_files_dir_option = nil)
    return false unless prepare_test_env

    generator = TestGenerator.new(yaml_test_def_path)

    puts "\n***\n*** Generating shellspec test #{yaml_test_def_path}\n***"
    success = generator.generate_shellspec_test
    return false unless success
    return false unless teardown_test_env
    return false unless prepare_test_env

    puts "\n***\n*** Verifying shellspec test #{yaml_test_def_path}\n***"
    generator.verify_final_specfile(verified_files_dir_option)

    false unless teardown_test_env
  end

  private

  def prepare_test_env
    return true unless setup_cmds.length.positive?

    puts 'Shellspec setup commands:'
    setup_cmds.each do |cmd|
      puts cmd
    end

    result = SysExec.run_all(setup_cmds, timeout: TIMEOUT_SECONDS)
    puts "Shellspec setup completed  #{result.exit_status.zero? ? 'successfully' : 'with errors'}"
    result.exit_status.zero?
  end

  def teardown_test_env
    return true unless teardown_cmds.length.positive?

    puts 'Shellspec teardown commands:'
    teardown_cmds.each do |cmd|
      puts cmd
    end

    result = SysExec.run_all(teardown_cmds, timeout: TIMEOUT_SECONDS)
    puts "Shellspec teardown completed  #{result.exit_status.zero? ? 'successfully' : 'with errors'}"
    result.exit_status.zero?
  end


end
