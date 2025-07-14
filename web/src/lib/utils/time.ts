/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import cronstrue from 'cronstrue';
import { format, formatDistance, formatRelative, fromUnixTime, parseISO, subDays } from 'date-fns';
import { formatInTimeZone } from 'date-fns-tz';

/**
 * Converts a Unix timestamp (in seconds) to a human-readable "X time ago" string
 *
 * @param seconds - Unix timestamp in seconds since epoch
 * @returns Formatted time ago string (e.g., "9 hours ago")
 */
export function secondsToHoursAgo(seconds: number | undefined): string {
	if (!seconds) return 'Unknown';

	// If the value is small (like 32893), it's likely seconds of uptime rather than a Unix timestamp
	if (seconds < 86400 * 365) {
		// Less than a year in seconds
		// Convert uptime seconds to a date by subtracting from now
		const uptimeDate = new Date(Date.now() - seconds * 1000);
		return formatDistance(uptimeDate, new Date(), { addSuffix: true });
	}

	// Otherwise treat as a Unix timestamp
	return formatDistance(fromUnixTime(seconds), new Date(), { addSuffix: true });
}

export function dateToAgo(date: Date | string): string {
	if (typeof date === 'string' && date.length > 4 && parseInt(date.substring(0, 4)) < 1999) {
		return 'Never';
	}

	if (typeof date === 'string') {
		date = parseISO(date);
	}

	const now = new Date();
	const diff = Math.abs(now.getTime() - date.getTime());

	if (diff < 1000 * 60) {
		return 'Just now';
	} else if (diff < 1000 * 60 * 60) {
		return formatDistance(date, now, { addSuffix: true });
	} else if (diff < 1000 * 60 * 60 * 24) {
		return formatDistance(date, now, { addSuffix: true });
	} else {
		return format(date, 'MMM dd, yyyy');
	}
}

export function getLastUsage(time: string): string {
	const formatted = convertDbTime(time);

	if (formatted.startsWith('2001-01-01')) {
		return 'Never';
	}

	return formatted;
}

export function convertDbTime(time: string): string {
	try {
		const userTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
		const date = new Date(time);
		const zonedDate = formatInTimeZone(date, userTimeZone, 'yyyy-MM-dd hh:mm a');

		return zonedDate;
	} catch (e) {
		return time;
	}
}

export function formatUptime(seconds: number): string {
	const units = [
		{ label: 'y', secs: 12 * 30 * 24 * 60 * 60 },
		{ label: 'mo', secs: 30 * 24 * 60 * 60 },
		{ label: 'd', secs: 24 * 60 * 60 },
		{ label: 'h', secs: 60 * 60 },
		{ label: 'm', secs: 60 },
		{ label: 's', secs: 1 }
	];

	const parts = [];

	for (const { label, secs } of units) {
		if (seconds >= secs) {
			const val = Math.floor(seconds / secs);
			seconds %= secs;
			parts.push(`${val}${label}`);
		}
	}

	return parts.length ? parts.join(' ') : '0s';
}

export function cronToHuman(cron: string): string {
	try {
		return cronstrue.toString(cron, { throwExceptionOnParseError: true });
	} catch (e) {
		return '';
	}
}
