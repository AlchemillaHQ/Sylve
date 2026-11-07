# frozen_string_literal: true

class SchemaValidator
  require 'json-schema'
  require 'yaml'

  class Result < Data.define(:valid, :errors, :parsed_data) do
    alias_method :valid?, :valid
  end
  end

  attr_reader :schema_doc

  def initialize(yaml_schema_path)
    # load the schema file one time
    @schema_doc = YAML.safe_load_file(yaml_schema_path) # string keys — see below
  end

  def validate_file(data_path)
    data   = YAML.safe_load_file(data_path)
    errors = JSON::Validator.fully_validate(@schema_doc, data) #, strict: true)

    # noinspection RubyArgCount
    result = Result.new(
      valid: errors.nil? || errors.empty?,
      errors: errors,
      parsed_data: errors.empty? ? data : nil
    )
    show_validation_result(result, data_path)
    result
  end

  def validate_all(data_files_glob)
    failed_files = []
    validated_files = []
    Dir[data_files_glob].each do |path|
      result = validate_file(path)
      show_validation_result(result, path, validated_files, failed_files)
    end
    puts "\nValidated files: #{validated_files.length}"
    puts "Failed files: #{failed_files.length}"
    failed_files.empty?
  end

  private

  def show_validation_result(result, path, validated_files = nil, failed_files = nil)
    if result.valid
      puts "#{path}: OK"
      validated_files << path if validated_files
    else
      failed_files << path if failed_files
      puts "\n#{path} validation failed, errors:"
      result.errors.each { |e| puts "  #{e}" }
      puts "\n\n"
    end
  end
end
