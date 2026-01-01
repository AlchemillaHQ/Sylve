/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { storage } from '$lib';
import { parseJwt } from './string';

export function getUsername(): string {
	try {
		const token = storage.token;
		if (!token) return 'unknown';

		const decoded = parseJwt(token);
		return decoded.custom_claims.username;
	} catch (e) {
		return 'unknown';
	}
}
