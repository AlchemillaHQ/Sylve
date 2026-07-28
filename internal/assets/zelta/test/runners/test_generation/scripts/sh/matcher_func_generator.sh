#!/bin/sh

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Check for required arguments
if [ $# -ne 2 ]; then
    printf "Usage: %s <output_to_match_file> <matcher_function_name>\n" "$0" >&2
    exit 1
fi

input_file="$1"
func_name="$2"

# Check if input file exists
if [ ! -f "$input_file" ]; then
    printf "Error: File '%s' not found\n" "$input_file" >&2
    exit 1
fi

awk -v func_name="$func_name" \
    -f "$SCRIPT_DIR/../awk/generate_case_stmt_func.awk" "$input_file"
