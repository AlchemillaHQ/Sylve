# frozen_string_literal: true

require 'fileutils'

class FileHelper
  def self.move_file_to_dir(src_path, dest_dir_path)
    src      = Pathname(src_path)
    dest_dir = Pathname(dest_dir_path)
    dest_dir.mkpath                              # mkdir -p; mv won't create it
    new_path = dest_dir / src.basename
    FileUtils.mv(src, new_path)
    new_path                                     # => #<Pathname:/new/dir/file.txt>
  rescue SystemCallError => e
    puts "Error moving file #{src} to #{dest_dir}: #{e.message}"
    raise
  end
end

