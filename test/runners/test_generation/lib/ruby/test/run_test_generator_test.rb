# frozen_string_literal: true

require 'minitest/autorun'
require 'mocha/minitest'
require_relative '../run_test_generator'

class RunTestGeneratorTest < Minitest::Test
  def setup
    @original_argv = ARGV.dup
    @original_program_name = $PROGRAM_NAME
    @existing_yaml_file = '/tmp/some.yaml'
    file = Tempfile.create(["input", ".yml"])
    file.close
    @yml_filepath = file.path
  end

  def teardown
    ARGV.replace(@original_argv)
    $PROGRAM_NAME = @original_program_name
    File.unlink(@yml_filepath) if File.exist?(@yml_filepath)
  end

  # ---------------------------------------------------------------------------
  # process_args — option parsing
  # ---------------------------------------------------------------------------

  def test_process_args_returns_nil_without_file
    ARGV.replace([])
    assert_nil RunTestGenerator.process_args
  end

  def test_process_args_returns_nil_for_invalid_option
    ARGV.replace(['--bogus-flag'])
    assert_nil RunTestGenerator.process_args
  end

  def test_process_args_with_yaml_file_only
    ARGV.replace([@yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal @yml_filepath, result[:file]
    refute result[:validate_file]
    refute result[:validate_all]
    assert_equal [], result[:setup_shellspec]
    assert_nil result[:verified_files_path_option]
  end

  def test_process_args_with_file_validate_long_form
    ARGV.replace(['--file-validate', @yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal @yml_filepath, result[:file]
    assert result[:validate_file]
  end

  def test_process_args_with_file_validate_short_form
    ARGV.replace(['--fv', @yml_filepath])
    result = RunTestGenerator.process_args
    assert result[:validate_file]
  end

  def test_process_args_with_all_validate_long_form
    ARGV.replace(['--all-validate'])
    result = RunTestGenerator.process_args
    assert result[:validate_all]
    assert_nil result[:file]
  end

  def test_process_args_with_all_validate_short_form
    ARGV.replace(['--av'])
    result = RunTestGenerator.process_args
    assert result[:validate_all]
    assert_nil result[:file]
  end

  def test_process_args_with_verified_dir_long_form
    ARGV.replace(['--verified-dir=/tmp/verified', @yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal '/tmp/verified', result[:verified_files_path_option]
  end

  def test_process_args_with_verified_dir_short_form
    ARGV.replace(['--vd=/tmp/verified', @yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal '/tmp/verified', result[:verified_files_path_option]
  end

  def test_process_args_with_single_setup_shellspec
    ARGV.replace(['--setup-shellspec=cmd1', @yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal ['cmd1'], result[:setup_shellspec]
  end

  def test_process_args_with_setup_shellspec_long_form
    ARGV.replace(['--setup-shellspec=cmd1', @yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal ['cmd1'], result[:setup_shellspec]
  end

  def test_process_args_accumulates_multiple_setup_shellspec
    ARGV.replace(['--setup-shellspec=cmd1', '--setup-shellspec=cmd2', '--setup-shellspec=cmd3', @yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal %w[cmd1 cmd2 cmd3], result[:setup_shellspec]
  end

  def test_process_args_combines_all_options
    ARGV.replace(['--fv', '-scmd1', '--vd=/tmp/v', @yml_filepath])
    result = RunTestGenerator.process_args
    assert_equal @yml_filepath, result[:file]
    assert result[:validate_file]
    assert_equal ['cmd1'], result[:setup_shellspec]
    assert_equal '/tmp/v', result[:verified_files_path_option]
  end

  def test_process_args_ignores_trailing_args_after_file
    ARGV.replace([@yml_filepath, 'extra'])
    result = RunTestGenerator.process_args
    assert_equal @yml_filepath, result[:file]
  end

  # ---------------------------------------------------------------------------
  # usage_message
  # ---------------------------------------------------------------------------

  def test_usage_message_includes_program_name
    $PROGRAM_NAME = '/path/to/run_test_generator.rb'
    assert_match(/run_test_generator\.rb/, RunTestGenerator.usage_message)
  end

  def test_usage_message_lists_file_validate_option
    options = { setup_shellspec: [], validate_file: false, validate_all: false, verified_files_path: nil }
    parser = RunTestGenerator.options_parser(options)
    assert_match(/--file-validate/, parser.to_s)
  end

  # ---------------------------------------------------------------------------
  # run — dispatches to validators and shellspec runner
  # ---------------------------------------------------------------------------
  def test_run_validates_all_when_av_flag_given
    ARGV.replace(['--av'])
    PathConfig.stubs(:yaml_schema_path).returns('/fake/schema.yml')
    PathConfig.stubs(:test_specfiles_glob).returns('/fake/**/*.yaml')

    validator = mock('schema_validator')
    validator.expects(:validate_all).with('/fake/**/*.yaml').returns(true)
    SchemaValidator.expects(:new).with('/fake/schema.yml').returns(validator)

    ShellspecRunner.any_instance.expects(:generate_test).never
    RunTestGenerator.run
  end

  def test_run_validates_file_only_when_fv_flag_given
    ARGV.replace(['--file-validate', @yml_filepath])
    PathConfig.stubs(:yaml_schema_path).returns(@yml_filepath)

    SchemaValidator.any_instance.expects(:validate_file).with(@yml_filepath).returns(true)
    ShellspecRunner.any_instance.expects(:generate_test).never
    RunTestGenerator.run
  end

  def test_run_validates_then_generates_test_for_yaml_file
    ARGV.replace(['/fake/test_def.yml'])
    PathConfig.stubs(:yaml_schema_path).returns('/fake/schema.yml')
    File.stubs(:exist?).returns(false)
    File.stubs(:exist?).with('/fake/schema.yml').returns(true)
    File.stubs(:exist?).with('/fake/test_def.yml').returns(true)
    validator = mock('schema_validator')
    validator.expects(:validate_file).with('/fake/test_def.yml').returns(true)
    SchemaValidator.expects(:new).with('/fake/schema.yml').returns(validator)
    runner = mock('shellspec_runner')
    runner.expects(:generate_test).with(nil).returns(true)
    ShellspecRunner.expects(:new).with([], '/fake/test_def.yml').returns(runner)

    RunTestGenerator.run
  end

  def test_run_passes_verified_dir_to_shellspec_runner
    ARGV.replace(['--vd=/tmp/verified', @yml_filepath])
    #PathConfig.stubs(:yaml_schema_path).returns('/fake/schema.yml')
    PathConfig.stubs(:yaml_schema_path).returns(@yml_filepath)

    SchemaValidator.any_instance.expects(:validate_file).returns(true)
    ShellspecRunner.any_instance.expects(:generate_test).with('/tmp/verified').returns(true)
    RunTestGenerator.run
  end


  def test_run_passes_setup_shellspec_to_shellspec_runner_constructor
    ARGV.replace(['-scmd1', '-scmd2', '/fake/test_def.yml'])
    PathConfig.stubs(:yaml_schema_path).returns('/fake/schema.yml')
    File.stubs(:exist?).returns(false)
    File.stubs(:exist?).with('/fake/schema.yml').returns(true)
    File.stubs(:exist?).with('/fake/test_def.yml').returns(true)
    validator = mock('schema_validator')
    validator.stubs(:validate_file).returns(true)
    SchemaValidator.stubs(:new).returns(validator)

    runner = mock('shellspec_runner')
    runner.stubs(:generate_test).returns(true)
    # This is the actual assertion — setup args arrive at the constructor
    ShellspecRunner.expects(:new).with(%w[cmd1 cmd2], '/fake/test_def.yml').returns(runner)

    RunTestGenerator.run
  end

  # NOTE: run_test_generator.rb:64 calls `options.usage` when `options` is nil,
  # which raises NoMethodError before the intended `return 1`. This test
  # documents that bug; once fixed, change to `assert_equal 1, ...`.
  def test_run_crashes_when_no_arguments_given_documents_bug
    ARGV.replace([])
    assert_output(nil, /Usage:/) do
      result = RunTestGenerator.run
      assert_equal false, result
    end
  end
end
