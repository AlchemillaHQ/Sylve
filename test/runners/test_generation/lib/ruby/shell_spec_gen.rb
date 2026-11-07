# frozen_string_literal: true

require 'yaml'

module ShellSpecGen
  HOOK_NAMES = %w[BeforeAll AfterAll BeforeEach AfterEach
                  BeforeCall AfterCall BeforeRun AfterRun].freeze
  HOOK_INVOCATIONS = %w[sourced_script executed_script evaluated_command].freeze

  # Top-level document. `load_file` is the entry point: read YAML → build Data → pass Data around.

  # `do` block binds to Data.define (defines methods on the parent); kept to satisfy RubyMine's RBS inference.
  #  TODO: remove `do` block when RubyMine supports RBS inference.
  class Config < Data.define(:shellspec_name, :output_dir, :multi_desc, :multi_tag, :example_list, :shell_code) do
    def self.load_file(path)
      from_h(YAML.safe_load_file(path, symbolize_names: true))
    end

    def self.from_h(h)
      config = new(
        shellspec_name: h.fetch(:shellspec_name),
        output_dir: h.fetch(:output_dir),
        multi_desc: h.fetch(:multi_desc, nil),
        multi_tag: h.fetch(:multi_tag, nil),
        example_list: h.fetch(:example_list).map { ExampleDefinition.from_h(it) },
        shell_code: h.fetch(:shell_code, nil)
      )
      if config.example_list.length > 1 && config.multi_desc.nil?
        raise ArgumentError, 'multi_desc is required when there are multiple examples'
      end

      config
    end
  end # Data.define
  end # class Config

  class ExampleDefinition < Data.define(:describe_desc, :tag, :shell_code, :hooks_list, :skip_if_list, :test_list) do
    def self.from_h(h)
      new(
        describe_desc: h.fetch(:describe_desc),
        tag: h.fetch(:tag, nil),
        shell_code: h.fetch(:shell_code, nil),
        hooks_list: h.fetch(:hooks_list, []).map { Hook.from_h(it) },
        skip_if_list: h.fetch(:skip_if_list, []).map { SkipIf.from_h(it) },
        test_list: h.fetch(:test_list).map { TestDefinition.from_h(it) },
      )
    end
  end # Data.define
  end # class ExampleDefinition

  class Hook < Data.define(:name, :invocation, :command) do
    def self.from_h(h)
      new(
        name: h.fetch(:name),
        invocation: h.fetch(:invocation),
        command: h.fetch(:command),
      )
    end

    # Value validation lives here, separate from shape: enum membership is the
    # one constraint the YAML schema actually enforces, so mirror it loudly.
    def initialize(name:, invocation:, command:)
      raise ArgumentError, "unknown hook name: #{name.inspect}" unless HOOK_NAMES.include?(name)
      raise ArgumentError, "unknown invocation: #{invocation.inspect}" unless HOOK_INVOCATIONS.include?(invocation)

      super
    end
  end # Data.define
  end # class Hook

  class TestDefinition < Data.define(
    :test_name, :it_desc, :tag, :when_command,
    :remove_timestamps,
    :output_clause,:allow_no_output, :setup_scripts
  ) do
    def self.from_h(h)
      new(
        test_name: h.fetch(:test_name),
        it_desc: h.fetch(:it_desc),
        tag: h.fetch(:tag, nil),
        when_command: h.fetch(:when_command),
        remove_timestamps: h.fetch(:remove_timestamps, true),
        output_clause: h.fetch(:output_clause, nil),
        allow_no_output: h.fetch(:allow_no_output, false),
        setup_scripts: h.fetch(:setup_scripts, []), # array of plain strings; no wrapper type
      )
    end
  end # Data.define
  end # class TestDefinition

  class SkipIf < Data.define(:condition) do
    def self.from_h(h)
      new(condition: h.fetch(:condition))
    end
  end # Data.define
  end # class SkipIf
end
