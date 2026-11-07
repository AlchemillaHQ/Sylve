#!/usr/bin/env ruby
# frozen_string_literal: true

require_relative 'test_generator'
require_relative 'schema_validator'
require_relative 'path_config'

module RunTestGenerator
  module_function

  def run
    options = process_args

    return false if options.nil?

    yaml_file = options[:file]

    if options[:validate_all]
      SchemaValidator.new(PathConfig.yaml_schema_path).validate_all(PathConfig.test_specfiles_glob)
    elsif options[:validate_file]
      SchemaValidator.new(PathConfig.yaml_schema_path).validate_file(yaml_file)
    else
      result = SchemaValidator.new(PathConfig.yaml_schema_path).validate_file(yaml_file)
      if result.valid?
        ShellspecRunner.new(options[:setup_shellspec], options[:teardown_shellspec], yaml_file).generate_test(options[:verified_files_path_option])
      else
        puts "File #{yaml_file} does not conform to schema"
      end
    end
  end

  def usage_message
    <<~USAGE
      Usage: #{$PROGRAM_NAME} [options] <yaml_file>
    USAGE
  end

  def options_parser(options)
    OptionParser.new do |opts|
      opts.banner = usage_message
      opts.on('-h', '--help', 'Show this help') do
        puts opts
        exit 1
      end
      opts.on('--verified-dir=VERIFIED_DIR', '--vd=VERIFIED_DIR', 'Verified files directory') do |verified_dir|
        options[:verified_files_path] = verified_dir
      end
      opts.on('--file-validate', '--fv', 'Validate the file instead of processing it') do
        options[:validate_file] = true
      end
      opts.on('--all-validate', '--av', 'Validate all files') do
        options[:validate_all] = true
      end
      opts.on('-s=SETUP', '--setup-shellspec=SETUP', 'Shellspec setup commands') do |setup|
        options[:setup_shellspec] << setup
      end
      opts.on('-t=TEARDOWN', '--teardown-shellspec=TEARDOWN', 'Shellspec teardown commands') do |teardown|
        options[:teardown_shellspec] << teardown
      end
    end
  end

  def validate_options(parser, options)
    options[:file] = nil

    # validate all is the only usage case where no file is specified
    return options if options[:validate_all]

    file = ARGV.shift
    if file.nil?
      warn parser  # prints the banner + option summary to stderr
      return nil
    end

    unless File.exist?(file)
      warn parser  # prints the banner + option summary to stderr
      warn "File not found: #{file}"
      return nil
    end

    options[:file] = file
    options
  end

  def process_args
    require 'optparse'
    options = { setup_shellspec: [], teardown_shellspec: [], validate_file: false, validate_all: false, verified_files_path: nil }
    parser = options_parser(options)

    begin
      parser.parse!  # consumes recognized options from ARGV, leaves the rest
    rescue OptionParser::InvalidOption => e
      warn e.message
      warn parser
      return nil
    end

    options = validate_options(parser, options)
    return nil if options.nil?

    { file: options[:file], validate_file: options[:validate_file], validate_all: options[:validate_all],
      setup_shellspec: options[:setup_shellspec], teardown_shellspec: options[:teardown_shellspec], verified_files_path_option: options[:verified_files_path] }
  end
end

# Script execution
RunTestGenerator.run if __FILE__ == $PROGRAM_NAME
