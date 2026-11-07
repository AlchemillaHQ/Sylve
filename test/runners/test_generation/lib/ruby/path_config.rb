# frozen_string_literal: true

# PathConfig - Manages file paths for test generation
# noinspection RubyTooManyInstanceVariablesInspection
class PathConfig
  SPEC_TEST_SCHEMA_PATH = 'config/test_config_schema.yml'
  SPEC_TESTS_GLOB_PATH = 'config/test_defs/**/*.yml'
  TEST_GEN_DIR = 'test/runners/test_generation'
  TEST_ENV_SETUP = 'test/runners/env/test_env.sh'

  @repo_root = `git rev-parse --show-toplevel`.strip.freeze
  @test_gen_dir = File.join(@repo_root, TEST_GEN_DIR).freeze
  @yaml_schema_path = File.join(@test_gen_dir, SPEC_TEST_SCHEMA_PATH).freeze
  @test_specfiles_glob = File.join(@test_gen_dir, SPEC_TESTS_GLOB_PATH).freeze
  @test_env_setup_path = File.join(@repo_root, TEST_ENV_SETUP).freeze

  class << self
    attr_reader :repo_root, :test_gen_dir, :yaml_schema_path, :test_specfiles_glob, :test_env_setup_path

    def verify_paths
      raise "Failed to find test generation directory: #{test_gen_dir}" unless File.directory?(test_gen_dir)
      raise "Failed to find repo root directory #{repo_root}:" unless File.directory?(repo_root)
      raise "Failed to find test config schema #{yaml_schema_path}" unless File.exist?(yaml_schema_path)
      raise "Failed to find test definition files #{test_specfiles_glob}" unless Dir.glob(test_specfiles_glob).any?
      raise "Failed to find test environment setup script: #{test_env_setup_path}" unless File.exist?(test_env_setup_path)
    end
  end

  verify_paths

  attr_reader :shellspec_name, :test_gen_dir, :output_path, :output_spec_path,
              :final_spec_path, :captured_output_path, :default_verified_files_path

  def testdef_stdout_path(test_name) = File.join(@captured_output_path, "#{test_name}.stdout")
  def testdef_stderr_path(test_name) = File.join(@captured_output_path, "#{test_name}.stderr")
  def testdef_status_path(test_name) = File.join(@captured_output_path, "#{test_name}.status")

  private

  def initialize(output_dir, shellspec_name)
    @shellspec_name = shellspec_name

    @output_path = output_dir.start_with?('/') ? output_dir : File.join(PathConfig.test_gen_dir, output_dir)
    FileUtils.mkdir_p(@output_path)

    raise "Output directory #{@output_path} does not exist" unless File.directory?(@output_path)

    @default_verified_files_path = File.join(output_path, 'verified')
    @captured_output_path = File.join(output_path, shellspec_name)
    @output_spec_path = File.join(output_path, "output_#{shellspec_name}.sh")
    @final_spec_path = File.join(output_path, "#{shellspec_name}.sh")
  end
end
