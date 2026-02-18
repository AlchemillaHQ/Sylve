#!/usr/bin/env ruby
# frozen_string_literal: true

# Validate a YAML test configuration against the schema

require 'yaml'
require 'json-schema'

if ARGV.empty?
  puts "Usage: #{$PROGRAM_NAME} <yaml_file>"
  puts "\nExample:"
  puts "  #{$PROGRAM_NAME} config/test_defs/040_zelta_tests.yml"
  exit 1
end

yaml_file = ARGV[0]
script_dir = File.dirname(__FILE__)
test_gen_dir = File.expand_path(File.join(script_dir, '..'))
schema_file = File.join(test_gen_dir, 'config', 'test_config_schema.yml')

# Resolve yaml_file relative to test_generation directory if not absolute
yaml_file = File.join(test_gen_dir, yaml_file) unless yaml_file.start_with?('/')

unless File.exist?(yaml_file)
  puts "❌ YAML file not found: #{yaml_file}"
  exit 1
end

unless File.exist?(schema_file)
  puts "❌ Schema file not found: #{schema_file}"
  exit 1
end

begin
  config = YAML.load_file(yaml_file)
  schema = YAML.load_file(schema_file)

  JSON::Validator.validate!(schema, config)

  puts "✅ Valid: #{yaml_file}"
  puts "\nConfiguration:"
  puts "  shellspec_name: #{config['shellspec_name']}"
  puts "  describe_desc: #{config['describe_desc']}"
  puts "  output_dir: #{config['output_dir']}"
  puts "  tests: #{config['test_list']&.length || 0}"
  puts "  skip conditions: #{config['skip_if_list']&.length || 0}"

  exit 0
rescue JSON::Schema::ValidationError => e
  puts "❌ Validation failed: #{yaml_file}"
  puts "\nError: #{e.message}"
  exit 1
rescue StandardError => e
  puts "❌ Error: #{e.message}"
  exit 1
end
