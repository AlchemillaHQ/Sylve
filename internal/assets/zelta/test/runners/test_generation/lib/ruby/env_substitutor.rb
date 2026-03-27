# frozen_string_literal: true

# EnvSubstitutor - Handles environment variable substitution in test output
class EnvSubstitutor
  attr_reader :sorted_env_map

  def initialize(env_var_names)
    @sorted_env_map = build_sorted_env_map(env_var_names)
  end

  def substitute(line)
    # Substitute env var names for values (longest first)
    # Use a placeholder to prevent already-substituted values from being re-matched
    replaced = line
    placeholder_map = {}
    @sorted_env_map.each_with_index do |(name, value), idx|
      placeholder = "__ENV_PLACEHOLDER_#{idx}__"
      replaced.gsub!(value, placeholder)
      placeholder_map[placeholder] = "${#{name}}"
    end

    # Replace placeholders with actual env var references
    placeholder_map.each do |placeholder, replacement|
      replaced.gsub!(placeholder, replacement)
    end
    replaced
  end

  private

  def build_sorted_env_map(env_var_names)
    # Parse and sort env vars by value length (descending)
    env_map = env_var_names.split(':').each_with_object({}) do |name, hash|
      hash[name] = ENV[name] if ENV[name]&.length&.positive?
    end

    # Sort by value length descending to replace longest matches first
    env_map.sort_by { |_name, value| -value.length }
  end
end
