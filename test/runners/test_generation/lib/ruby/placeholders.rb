# Performs variable substitution in a string using the values from an object's instance variables.
# The substitution is performed using the %{variable_name} syntax.
#
# Usage examples
# Placeholders.substitute("run %{when_command}", my_obj)
# Placeholders.substitute("run %{when_command}", my_obj, exclusions: [:internal_state])
# Placeholders.substitute("run %{when_command}", my_obj, inclusions: [:when_command, :runner])

module Placeholders
  def self.substitute(string, source, inclusions: nil, exclusions: nil)
    raise ArgumentError, 'Cannot specify both inclusions and exclusions' if inclusions && exclusions

    vars = if source.is_a?(Hash)
             filter_hash(source, inclusions, exclusions)
           else
             extract_vars_from_object(source, inclusions, exclusions)
           end

    print "Substituting variables in string: #{string}\n"
    print "Using variables: #{vars.inspect}\n"
    string.gsub(/%\{(\w+)\}/) { vars[$1] || vars[$1.to_sym] }
  end

  class << self
    def filter_hash(hash, inclusions, exclusions)
      return hash if inclusions.nil? && exclusions.nil?

      hash.select do |key, _|
        key_matches = key_matches_filter?(key, inclusions, exclusions)
        puts "Filtering key: #{key}" if key_matches
        key_matches
      end
    end

    def key_matches_filter?(key, inclusions, exclusions)
      key_variants = [key, key.to_s, key.to_s.to_sym]

      if inclusions
        key_variants.any? { |k| inclusions.include?(k) }
      elsif exclusions
        key_variants.none? { |k| exclusions.include?(k) }
      else
        true
      end
    end

    def extract_vars_from_object(obj, inclusions, exclusions)
      obj.instance_variables.each_with_object({}) do |var, hash|
        var_name = var.to_s.delete('@')
        next unless var_matches_filter?(var_name, inclusions, exclusions)

        hash[var_name] = obj.instance_variable_get(var)
      end
    end

    def var_matches_filter?(var_name, inclusions, exclusions)
      var_variants = [var_name, var_name.to_sym]

      if inclusions
        var_variants.any? { |v| inclusions.include?(v) }
      elsif exclusions
        var_variants.none? { |v| exclusions.include?(v) }
      else
        true
      end
    end
  end
end
