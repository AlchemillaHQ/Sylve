#!/usr/bin/awk

BEGIN {
	RESET = "\033[0m"
	GRAY = "\033[38;5;240m"
	CYAN = "\033[36m"
	RESET = "\033[0m"
	BLACK = "\033[30m"
	RED = "\033[31m"
	GREEN = "\033[32m"
	YELLOW = "\033[33m"
	BLUE = "\033[34m"
	MAGENTA = "\033[35m"
	CYAN = "\033[36m"
	WHITE = "\033[37m"
}
NR == 1 {
	# Parse header to get column positions
	n = split($0, headers)
	for (i = 1; i <= n; i++) {
		col_start[i] = index($0, headers[i])
		if (i < n)
			col_end[i] = index($0, headers[i+1]) - 1
	}
	col_end[n] = length($0)

	print #CYAN $0 RESET
	next
}
{
	# Extract columns by position
	for (i = 1; i <= n; i++) {
		len = col_end[i] - col_start[i] + 1
		col[i] = substr($0, col_start[i], len)
		# Trim trailing spaces for easier matching
		gsub(/[[:space:]]+$/, "", col[i])
	}

	# Now col[1], col[2], col[3] are your columns
	# Color the whole line
	if (NR % 2 == 0)
		print RED $0 RESET
	else
		print GREEN $0 RESET
}
