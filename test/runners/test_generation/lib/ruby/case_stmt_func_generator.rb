# frozen_string_literal: true

# Generates a shell function containing a case statement that validates
# stdin lines against a set of expected output patterns.
#
# Equivalent to scripts/awk/generate_case_stmt_func.awk
#
# Usage:
#   CaseStmtFuncGenerator.from_file("expected_output.txt", "my_matcher")
#   CaseStmtFuncGenerator.generate(lines_array, "my_matcher")
require_relative 'env_substitutor'

class CaseStmtFuncGenerator
  DEFAULT_ENV_VAR_NAMES = 'SANDBOX_ZELTA_TGT_DS:SANDBOX_ZELTA_SRC_DS:SANDBOX_ZELTA_TGT_EP:SANDBOX_ZELTA_SRC_EP'

  def self.matcher_function_from_file(path, func_name, clean_output, env_var_names = DEFAULT_ENV_VAR_NAMES)
    new(env_var_names).matcher_function_from_file(path, func_name, clean_output)
  end

  def self.format_expected_error(stderr_file, env_var_names = DEFAULT_ENV_VAR_NAMES)
    new(env_var_names).format_expected_error(stderr_file)
  end

  def initialize(env_var_names = DEFAULT_ENV_VAR_NAMES)
    @env_substitutor = EnvSubstitutor.new(env_var_names)
  end

  def matcher_function_from_file(path, func_name, clean_output)
    lines = File.readlines(path, chomp: true)
    function_lines = generate_function(lines, func_name)
    clean_up_output(function_lines, clean_output)
  end

  def format_expected_error(stderr_file)
    lines = read_stderr_file(stderr_file)
    lines.map! { |line| normalize_output_line(line, true) }
    lines.join("\n")
  end

  private

  def read_stderr_file(stderr_file)
    File.readlines(stderr_file).map(&:chomp)
  rescue StandardError => e
    puts "Warning: Could not read stderr file #{stderr_file}: #{e.message}"
    []
  end

  def clean_up_output_line(line, remove_timestamps)
    # Normalize whitespace
    normalized = line.gsub(/\s+/, ' ').strip


    if remove_timestamps
      # Replace timestamp patterns (both @zelta_ and _zelta_ prefixes)
      normalized.gsub!(/@zelta_\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}/, '@zelta_"*"')
      normalized.gsub!(/_zelta_\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}/, '_zelta_"*"')

      # replace timestamp for generated zelta policy files is YYYY-MM-DD_HH.MM.SS with *
      normalized.gsub!(/_[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}\.[0-9]{2}\.[0-9]{2}/, '_"*"')
    end

    # Escape backticks
    normalized.gsub!('`', '\\\`')

    # Wildcard time and quantity sent
    if normalized =~ /(\d+[KMGT]? sent, )(\d+ streams)( received in \d+\.\d+ seconds)/
      stream_count = $2
      normalized.gsub!(/\d+[KMGT]? sent, \d+ streams received in \d+\.\d+ seconds/,
                       "\"*\" sent, #{stream_count} received in \"*\" seconds")
    end
    normalized
  end

  def normalize_output_line(line, remove_timestamps)
    @env_substitutor.substitute(
      clean_up_output_line(line, remove_timestamps)
    )
  end

  def clean_up_output(lines, remove_timestamps)
    lines.map do |line|
      # Only process lines that look like case patterns (contain quoted strings)
      if line =~ /^\s*".*"(?:\)|\|\\)$/
        # Extract the quoted content, normalize it, and reconstruct the line
        if line =~ /^(\s*)"(.*)"(\)|\|\\)$/
          indent = $1
          pattern = $2
          suffix = $3
          normalized = normalize_output_line(pattern, remove_timestamps)
          "#{indent}\"#{normalized}\"#{suffix}\n"
        else
          line
        end
      else
        line
      end
    end
  end

  def generate_function(lines, func_name)
    patterns = lines
               .reject { |l| l.strip.empty? || l.match?(/\A\s*#/) }
               .map { |l|                   l.gsub(/\s+/, ' ').strip }

    build_function(patterns, func_name)
  end

  def build_function(patterns, func_name)
    buf = []
    buf << "#{func_name}() {"
    buf << '  while IFS= read -r line; do'
    buf << '    # normalize whitespace, remove leading/trailing spaces'
    buf << "    normalized=$(printf '%s' \"$line\" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
    buf << '    # check line against expected output'
    buf << '    case "$normalized" in'

    patterns.each_with_index do |pattern, i|
      last = (i == patterns.length - 1)
      buf << "        \"#{pattern}\"#{last ? ')' : '|\\'}"
    end

    buf << '        ;;'
    buf << '      *)'
    buf << '        printf "Unexpected line format : %s\n" "$line" >&2'
    buf << '        printf "Comparing to normalized: %s\n" "$normalized" >&2'
    buf << '        return 1'
    buf << '        ;;'
    buf << '    esac'
    buf << '  done'
    buf << '  return 0'
    buf << '}'
    buf << ''
  end
end
