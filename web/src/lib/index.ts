/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Locales } from './types/common';
import { createReactiveStorage } from './utils/storage';

type SharedStorage = {
	token: string | null;
	oldToken: string | null;
	language: Locales | null;
	hostname: string | null;
	nodeId: string | null;
	clusterToken: string | null;
	fileExplorerCurrentPath: string | null;
	visible: boolean | null;
};

export const storage = createReactiveStorage<SharedStorage>(
	[
		['token', { storage: 'local' }],
		['oldToken', { storage: 'local' }],
		['language', { storage: 'local' }],
		['hostname', { storage: 'local' }],
		['nodeId', { storage: 'local' }],
		['clusterToken', { storage: 'local' }],
		['fileExplorerCurrentPath', { storage: 'local' }],
		['visible', { storage: 'local' }]
	],
	{
		prefix: 'sylve:',
		storage: 'local'
	}
);

export const languageArr: { value: Locales; label: string }[] = [
	{ value: 'en', label: 'English' },
	{ value: 'mal', label: 'മലയാളം' },
	{ value: 'hi', label: 'हिन्दी' }
];
