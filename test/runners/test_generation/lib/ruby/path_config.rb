# frozen_string_literal: true

# PathConfig - Manages file paths for test generation
class PathConfig
  attr_reader :output_dir, :wip_file_path, :final_file_path

  def initialize(output_dir, shellspec_name, test_gen_dir)
    @output_dir = output_dir.start_with?('/') ? output_dir : File.join(test_gen_dir, output_dir)
    @wip_file_path = File.join(@output_dir, "#{shellspec_name}_wip.sh")
    # remove _spec to prevent shellspec from finding the WIP file
    @wip_file_path.sub!('_spec', '')
    @final_file_path = File.join(@output_dir, "#{shellspec_name}.sh")
  end
end
