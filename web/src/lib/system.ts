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

export const isMac = navigator.userAgent.includes('Mac');
export const cmdOrCtrl = isMac ? '⌘' : 'Ctrl';
export const optionOrAlt = isMac ? '⌥' : 'Alt';

export function handleCommandKeydown(e: KeyboardEvent) {
	if (e.repeat) return;
	if (e.key === '/' && (e.metaKey || e.ctrlKey)) {
		e.preventDefault();
		storage.openCommands = !storage.openCommands;
	}
}
