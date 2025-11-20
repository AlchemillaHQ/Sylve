/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { localStore } from '@layerstack/svelte-stores';
import { addDays } from 'date-fns';

export const hostname = localStore('hostname', '', {
	expiry: addDays(new Date(), 1)
});

export const language = localStore('language', 'en', {
	expiry: addDays(new Date(), 720)
});

export const nodeId = localStore('nodeId', '', {
	expiry: addDays(new Date(), 1)
});

export const explorerCurrentPath = localStore('explorerCurrentPath', '/', {
	expiry: addDays(new Date(), 7) // 7 days instead of 1
});
