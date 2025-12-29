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
import { getUsername } from '$lib/utils/auth';
import { createReactiveStorage } from '$lib/utils/storage';

type ReactiveTab = {
	id: string;
	title: string;
};

type ReactiveTerminal = {
	isOpen: boolean;
	isMinimized: boolean;
	title: string;
	tabs: ReactiveTab[];
	activeTabId: string;
};

export const terminalStore = createReactiveStorage<ReactiveTerminal>(
	[
		['isOpen', { storage: 'local' }],
		['isMinimized', { storage: 'local' }],
		['title', { storage: 'local' }],
		['tabs', { storage: 'local' }],
		['activeTabId', { storage: 'local' }]
	],
	{
		prefix: 'sylve-terminal:',
		storage: 'local'
	}
);

export function getDefaultTitle() {
	return `${getUsername()}@${storage.hostname}:~`;
}

export function openTerminal() {
	if (terminalStore.tabs?.length > 0) {
		terminalStore.isOpen = true;
		terminalStore.isMinimized = false;
		return;
	}

	const tabId = `sylve-${terminalStore.tabs?.length || 0 + 1}`;
	terminalStore.title = 'Terminal';
	terminalStore.tabs = [
		{
			id: tabId,
			title: getDefaultTitle()
		}
	];
	terminalStore.activeTabId = tabId;
	terminalStore.isOpen = true;
	terminalStore.isMinimized = false;
}
