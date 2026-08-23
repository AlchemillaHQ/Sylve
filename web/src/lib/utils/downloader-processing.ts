/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

const tarArchiveSuffixes = [
	'.tar',
	'.tar.gz',
	'.tgz',
	'.tar.bz2',
	'.tbz',
	'.tbz2',
	'.tar.xz',
	'.txz',
	'.tar.zst',
	'.tar.zstd',
	'.tzst',
	'.tar.z'
];

export const tarRawConversionError =
	'Tar archives cannot be converted to RAW. Disable RAW conversion, or use a compressed disk image.';

function normalizedSourceName(source: string): string {
	const withoutFragment = source.trim().split('#', 1)[0];
	const withoutQuery = withoutFragment.split('?', 1)[0].replaceAll('\\', '/');
	const base = withoutQuery.slice(withoutQuery.lastIndexOf('/') + 1);
	try {
		return decodeURIComponent(base).toLowerCase();
	} catch {
		return base.toLowerCase();
	}
}

export function isTarArchiveName(source: string): boolean {
	const name = normalizedSourceName(source);
	return tarArchiveSuffixes.some((suffix) => name.endsWith(suffix));
}

export function getDownloaderProcessingOptionsError(
	source: string,
	automaticExtraction: boolean,
	automaticRawConversion: boolean
): string {
	if (automaticExtraction && automaticRawConversion && isTarArchiveName(source)) {
		return tarRawConversionError;
	}
	return '';
}
