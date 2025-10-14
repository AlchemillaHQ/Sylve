/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { Address4, Address6 } from 'ip-address';

export function maskToCIDR(mask: string) {
	const parts = mask.split('.').map(Number);
	let cidr = 0;

	for (const part of parts) {
		cidr += ((part >>> 0).toString(2).match(/1/g) || []).length;
	}

	return cidr;
}

export function isValidIPv4Range(start: string, end: string, network: string, mask: string) {
	const startAddr = new Address4(start);
	const endAddr = new Address4(end);
	const networkAddr = new Address4(`${network}/${maskToCIDR(mask)}`);

	if (!startAddr.isCorrect() || !endAddr.isCorrect() || !networkAddr.isCorrect()) {
		return false;
	}

	if (!startAddr.isInSubnet(networkAddr) || !endAddr.isInSubnet(networkAddr)) {
		return false;
	}

	if (startAddr.bigInteger() > endAddr.bigInteger()) {
		return false;
	}

	return true;
}

export function isValidIPv6Range(start: string, end: string, network: string, prefix: number) {
	const startAddr = new Address6(start);
	const endAddr = new Address6(end);
	const networkAddr = new Address6(`${network}/${prefix}`);

	if (!startAddr.isCorrect() || !endAddr.isCorrect() || !networkAddr.isCorrect()) {
		return false;
	}

	if (!startAddr.isInSubnet(networkAddr) || !endAddr.isInSubnet(networkAddr)) {
		return false;
	}

	if (startAddr.bigInteger() > endAddr.bigInteger()) {
		return false;
	}

	return true;
}
