# frozen_string_literal: true

require 'minitest/autorun'
require_relative '../path_config'

class PathConfigTest < Minitest::Test
  def setup
    # Do nothing
  end

  def teardown
    # Do nothing
  end

  def test_paths
    paths= PathConfig.new('output_dir', 'shellspec_name')
    expected_schema_path = File.join(PathConfig.test_gen_dir, ::PathConfig::SPEC_TEST_SCHEMA_PATH)
    assert_equal(expected_schema_path, PathConfig.yaml_schema_path)
    assert_equal("#{PathConfig.test_gen_dir}/output_dir", paths.output_path)
    assert_equal("#{PathConfig.test_gen_dir}/output_dir/shellspec_name", paths.captured_output_path)
    assert_equal("#{PathConfig.test_gen_dir}/output_dir/shellspec_name/test_name.stdout",
                 paths.testdef_stdout_path('test_name'))
    assert_equal("#{PathConfig.test_gen_dir}/output_dir/shellspec_name/test_name.stderr",
                 paths.testdef_stderr_path('test_name'))
    assert_equal("#{PathConfig.test_gen_dir}/output_dir/shellspec_name/test_name.status",
                 paths.testdef_status_path('test_name'))
  end
end
