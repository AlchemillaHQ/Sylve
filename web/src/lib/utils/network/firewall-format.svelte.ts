/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { FirewallTrafficRule } from '$lib/types/network/firewall';
import { escapeHTML } from '$lib/utils/string';
import { renderWithIcon } from '$lib/utils/table';

function badge(label: string, color: string): string {
	return `<span class="inline-flex items-center text-xs font-mono px-1 rounded border ${color} leading-tight">${escapeHTML(label)}</span>`;
}

function familyBadge(family: string): string {
	if (!family || family === 'any') return '';
	return family === 'inet'
		? badge('IPv4', 'text-blue-400 border-blue-400/50')
		: badge('IPv6', 'text-violet-400 border-violet-400/50');
}

function protoBadge(protocol: string): string {
	if (!protocol || protocol === 'any') return '';
	const colors: Record<string, string> = {
		tcp: 'text-cyan-400 border-cyan-400/50',
		udp: 'text-amber-400 border-amber-400/50',
		tcp_udp: 'text-teal-400 border-teal-400/50',
		icmp: 'text-pink-400 border-pink-400/50'
	};
	const color = colors[protocol] || 'text-muted-foreground border-muted-foreground/50';
	const label = protocol === 'tcp_udp' ? 'TCP/UDP' : protocol.toUpperCase();
	return badge(label, color);
}

function endpointParts(addr: string, isObj: boolean): string {
	if (!addr || addr === 'any') return renderWithIcon('mdi:earth', 'Any', 'text-sky-400');
	return isObj
		? renderWithIcon('mdi:tag-outline', escapeHTML(addr), 'text-purple-400')
		: renderWithIcon('mdi:ip-network', escapeHTML(addr), 'text-indigo-400');
}

export function formatFirewallSource(
	addr: string,
	isObj: boolean,
	family: string,
	port = ''
): string {
	const parts: string[] = [];
	const fb = familyBadge(family);
	if (fb) parts.push(fb);
	parts.push(endpointParts(addr, isObj));
	if (port) parts.push(renderWithIcon('mdi:pound', escapeHTML(port), 'text-zinc-400'));
	return `<span class="flex items-center gap-1.5">${parts.join('<span class="text-muted-foreground/40 text-xs">·</span>')}</span>`;
}

export function formatFirewallDestination(
	addr: string,
	isObj: boolean,
	protocol: string,
	port: string
): string {
	const parts: string[] = [];
	const pb = protoBadge(protocol);
	if (pb) parts.push(pb);
	parts.push(endpointParts(addr, isObj));
	if (port) parts.push(renderWithIcon('mdi:pound', escapeHTML(port), 'text-zinc-400'));
	return `<span class="flex items-center gap-1.5">${parts.join('<span class="text-muted-foreground/40 text-xs">·</span>')}</span>`;
}

export function formatFirewallAction(
	rule: Pick<
		FirewallTrafficRule,
		'action' | 'direction' | 'quick' | 'log' | 'statePolicy' | 'blockResponse'
	>
): string {
	const isPass = rule.action === 'pass';
	const parts = [
		renderWithIcon(
			isPass ? 'mdi:check-circle-outline' : 'mdi:close-octagon-outline',
			isPass ? 'Pass' : 'Block',
			isPass ? 'text-green-500' : 'text-red-400'
		),
		renderWithIcon(
			rule.direction === 'in' ? 'mdi:arrow-down-circle-outline' : 'mdi:arrow-up-circle-outline',
			rule.direction === 'in' ? 'In' : 'Out',
			rule.direction === 'in' ? 'text-blue-400' : 'text-orange-400'
		)
	];
	if (rule.quick) parts.push(badge('QUICK', 'text-emerald-400 border-emerald-400/50'));
	if (rule.log) parts.push(badge('LOG', 'text-amber-400 border-amber-400/50'));
	let response = '';
	if (rule.action === 'block') {
		response = { default: '', drop: 'Drop', return: 'Return' }[rule.blockResponse];
	} else if (rule.statePolicy === 'keep') {
		parts.push(badge('Keep State', 'text-blue-400 border-blue-400/50'));
	} else if (rule.statePolicy === 'none') {
		parts.push(badge('No State', 'text-zinc-400 border-zinc-400/50'));
	}
	return `<span class="flex items-center gap-1.5">${parts.join('<span class="text-muted-foreground/50">·</span>')}${response ? `<span class="text-xs text-muted-foreground">${response}</span>` : ''}</span>`;
}
