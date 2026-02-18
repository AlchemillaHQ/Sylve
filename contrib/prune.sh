DSNAME="$1"
KEEP_MIN=10
KEEP_DAYS=90
[ -z "$1" ] && exit
KEEP_DAYS=$(($(date +%s)-60*60*24*KEEP_DAYS))

zfs list -Hprtsnap -oname,creation -d1 -screation "$DSNAME" | awk -v min=$KEEP_MIN -v days=$KEEP_DAYS '
{
	if ($2<days) {
		x[++y]=$1
		if (x[y-min]) z = x[y-min]
	}
}
END {
	if (z) {
		sub(/@/, "@%", z)
		print "zfs destroy -vrn \"" z "\""
	}
}'
