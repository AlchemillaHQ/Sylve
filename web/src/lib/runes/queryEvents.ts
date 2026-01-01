/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

type RefetchCallback = () => void;

const focusSubscribers = new Set<RefetchCallback>();
const onlineSubscribers = new Set<RefetchCallback>();

let listenersInitialized = false;

function ensureListeners() {
	if (listenersInitialized) return;
	if (typeof window === 'undefined' || typeof document === 'undefined') return;

	listenersInitialized = true;

	const handleFocusOrVisible = () => {
		if (document.visibilityState !== 'visible') return;
		for (const cb of focusSubscribers) cb();
	};

	window.addEventListener('focus', handleFocusOrVisible);
	document.addEventListener('visibilitychange', handleFocusOrVisible);

	window.addEventListener('online', () => {
		for (const cb of onlineSubscribers) cb();
	});
}

export function subscribeToFocus(cb: RefetchCallback): () => void {
	ensureListeners();
	focusSubscribers.add(cb);
	return () => {
		focusSubscribers.delete(cb);
	};
}

export function subscribeToOnline(cb: RefetchCallback): () => void {
	ensureListeners();
	onlineSubscribers.add(cb);
	return () => {
		onlineSubscribers.delete(cb);
	};
}
