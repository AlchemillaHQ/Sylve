/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

// import en from '$lib/locale/en.json';
// import mal from '$lib/locale/mal.json';

// addMessages('en', en);
// addMessages('mal', mal);

export let savedLang: string = 'en';

if (typeof window !== 'undefined') {
	const stored = window.localStorage.getItem('language');
	try {
		const parsed = JSON.parse(stored || '');
		savedLang = parsed.value;
	} catch (_e) {
		savedLang = 'en';
	}
}
