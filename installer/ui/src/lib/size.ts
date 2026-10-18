const UNIT_TO_MULTIPLIER: Record<string, number> = {
	'': 1,
	b: 1,
	byte: 1,
	bytes: 1,

	k: 1024,
	kb: 1024,
	ki: 1024,
	kib: 1024,

	m: 1024 ** 2,
	mb: 1024 ** 2,
	mi: 1024 ** 2,
	mib: 1024 ** 2,

	g: 1024 ** 3,
	gb: 1024 ** 3,
	gi: 1024 ** 3,
	gib: 1024 ** 3,

	t: 1024 ** 4,
	tb: 1024 ** 4,
	ti: 1024 ** 4,
	tib: 1024 ** 4
};

const SIZE_INPUT_RE = /^(\d+(?:\.\d+)?)\s*([a-z]*)$/i;

const SIZE_UNITS: [string, number][] = [
	['TiB', 1024 ** 4],
	['GiB', 1024 ** 3],
	['MiB', 1024 ** 2],
	['KiB', 1024]
];

export function parseSizeInputToBytes(text: string): number | null {
	const cleaned = text.trim().replace(/[_,]/g, '');
	if (cleaned === '') return 0;

	const match = cleaned.match(SIZE_INPUT_RE);
	if (!match) return null;

	const multiplier = UNIT_TO_MULTIPLIER[match[2].toLowerCase()];
	if (multiplier === undefined) return null;

	const value = Number(match[1]);
	if (!Number.isFinite(value) || value < 0) return null;

	const bytes = Math.round(value * multiplier);
	return Number.isSafeInteger(bytes) ? bytes : null;
}

export function formatSizeBytes(bytes: number): string {
	if (!Number.isFinite(bytes) || bytes <= 0) return '0';

	for (const [suffix, unit] of SIZE_UNITS) {
		if (bytes >= unit && bytes % unit === 0) {
			return `${bytes / unit} ${suffix}`;
		}
	}

	for (const [suffix, unit] of SIZE_UNITS) {
		if (bytes >= unit) {
			const value = bytes / unit;
			return value < 10 ? `${value.toFixed(1)} ${suffix}` : `${Math.round(value)} ${suffix}`;
		}
	}
	return `${Math.round(bytes)} B`;
}

export function normalizeSizeInput(text: string): string | null {
	const bytes = parseSizeInputToBytes(text);
	if (bytes === null) return null;
	return formatSizeBytes(bytes);
}
