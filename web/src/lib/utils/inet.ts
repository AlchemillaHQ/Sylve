/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { Address4 } from 'ip-address';
import { IPv6, IPv6CidrRange } from 'ip-num';

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

    return true;
}

export function isValidIPv6Range(start: string, end: string, network: string, prefix: number): boolean {
    try {
        const cidrRange = IPv6CidrRange.fromCidr(`${network}/${prefix}`);
        const startAddr = new IPv6(start);
        const endAddr = new IPv6(end);

        if (startAddr.getValue() > endAddr.getValue()) {
            console.log('Error: Start address is greater than end address');
            return false;
        }

        const isStartInSubnet = cidrRange.contains(startAddr);
        const isEndInSubnet = cidrRange.contains(endAddr);

        if (!isStartInSubnet || !isEndInSubnet) {
            console.log(`Error: Range not in subnet. StartIn: ${isStartInSubnet}, EndIn: ${isEndInSubnet}`);
            return false;
        }

        return true;

    } catch (err) {
        console.error('IP Validation Error:', err);
        return false;
    }
}