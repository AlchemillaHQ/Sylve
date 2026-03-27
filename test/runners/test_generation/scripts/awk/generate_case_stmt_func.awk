#!/usr/bin/awk -f

# ignore blank lines
/^$/ { next }

# ignore comment lines starting with #
/^[[:space:]]*#/ { next }

# Process data lines
{
    # normalize whitespace to a single space
    gsub(/[[:space:]]+/, " ", $0)

    # remove trailing spaces
    sub(/[[:space:]]+$/, "", $0)

    lines[count++] = $0
}

END {
    print func_name "() {"
    print "  while IFS= read -r line; do"
    print "    # normalize whitespace, remove leading/trailing spaces"

    # Use printf with %s format specifier to avoid \$ escapes
    dollar = "$"
    printf "    normalized=%s(printf '%%s' \"%sline\" | tr -s '[:space:]' ' ' | sed 's/^[[:space:]]*//; s/[[:space:]]*%s//')\n", dollar, dollar, dollar

    print "    # check line against expected output"
    print "    case \"$normalized\" in"

    line_continue = "\"|\\"
    case_end = "\")"

    for (i = 0; i < count; i++) {
        line_end = (i + 1 == count) ? case_end : line_continue
        print "        \"" lines[i] line_end
    }

    print "        ;;"
    print "      *)"
    print "        printf \"Unexpected line format : %s\\n\" \"$line\" >&2"
    print "        printf \"Comparing to normalized: %s\\n\" \"$normalized\" >&2"
    print "        return 1"
    print "        ;;"
    print "    esac"
    print "  done"
    print "  return 0"
    print "}"
}
