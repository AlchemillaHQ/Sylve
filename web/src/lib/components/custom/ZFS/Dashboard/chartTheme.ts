export function getZFSChartTheme(dark: boolean) {
	if (typeof document === 'undefined') {
		return {
			foreground: dark ? '#e4e4e7' : '#3f3f46',
			muted: dark ? '#a1a1aa' : '#71717a',
			grid: dark ? 'rgba(255,255,255,0.08)' : 'rgba(24,24,27,0.08)',
			tooltip: dark ? '#343434' : '#fafaf9',
			tooltipBorder: dark ? '#525252' : '#d6d3d1',
			read: '#0ea5e9',
			write: '#8b5cf6'
		};
	}

	const styles = getComputedStyle(document.documentElement);
	const token = (name: string, fallback: string) =>
		styles.getPropertyValue(name).trim() || fallback;
	return {
		foreground: token('--foreground', dark ? '#e4e4e7' : '#3f3f46'),
		muted: token('--muted-foreground', dark ? '#a1a1aa' : '#71717a'),
		grid: token('--zfs-chart-grid', dark ? 'rgba(255,255,255,0.08)' : 'rgba(24,24,27,0.08)'),
		tooltip: token('--popover', dark ? '#343434' : '#fafaf9'),
		tooltipBorder: token('--border', dark ? '#525252' : '#d6d3d1'),
		read: token('--zfs-read', '#0ea5e9'),
		write: token('--zfs-write', '#8b5cf6')
	};
}
