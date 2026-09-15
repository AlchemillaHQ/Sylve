/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { VLANPortPolicy } from '$lib/types/network/switch';
import { escapeHTML } from '$lib/utils/string';

export interface VLANPolicySourcePort {
	name: string;
	vlanPolicy?: VLANPortPolicy;
}

const ACCESS_BADGE_COLORS = 'text-cyan-400 border-cyan-400/50';
const TRUNK_BADGE_COLORS = 'text-amber-400 border-amber-400/50';
const DEFAULT_ACCESS_BADGE_COLORS = 'text-violet-400 border-violet-400/50';
const HOST_BADGE_COLORS = 'text-emerald-400 border-emerald-400/50';
const MAX_VISIBLE_PORT_LINES = 4;
const MAX_VISIBLE_TAGGED_VLANS = 8;

function vlanBadge(label: string, colors: string, title?: string): string {
	const titleAttribute = title ? ` title="${escapeHTML(title)}"` : '';
	return `<span${titleAttribute} class="inline-flex items-center font-mono text-xs px-1 rounded border cursor-help ${colors} leading-tight">${escapeHTML(label)}</span>`;
}

export function defaultAccessVlanBadge(vlan: number, title?: string): string {
	return vlanBadge(String(vlan), DEFAULT_ACCESS_BADGE_COLORS, title);
}

export function hostVlanBadge(vlan: number, hostInterface?: string): string {
	const title = hostInterface ? `Host VLAN interface: ${hostInterface}` : 'Host VLAN interface';
	return vlanBadge(String(vlan), HOST_BADGE_COLORS, title);
}

export function vlanNoneCell(title: string): string {
	return `<span class="text-muted-foreground cursor-help" title="${escapeHTML(title)}">None</span>`;
}

export function vlanPolicyText(policy?: VLANPortPolicy): string {
	if (policy?.mode === 'access') {
		return `Access ${policy.untaggedVlan ?? '-'}`;
	}
	if (policy?.mode === 'trunk') {
		const native = policy.untaggedVlan === undefined ? '' : ` native ${policy.untaggedVlan}`;
		const tagged = (policy.taggedVlans ?? []).join(',');
		return `Trunk${native}${tagged ? `, tagged ${tagged}` : ''}`;
	}
	return '';
}

function portPolicyText(port: VLANPolicySourcePort): string {
	const policy = vlanPolicyText(port.vlanPolicy);
	return policy ? `${port.name} ${policy}` : port.name;
}

function vlanPolicyParts(policy?: VLANPortPolicy): string[] {
	if (policy?.mode === 'access') {
		return [
			vlanBadge(`ACCESS ${policy.untaggedVlan ?? '-'}`, ACCESS_BADGE_COLORS, 'Untagged access VLAN')
		];
	}
	if (policy?.mode !== 'trunk') return [];

	const parts = [
		vlanBadge('TRUNK', TRUNK_BADGE_COLORS, 'Tagged trunk with an optional native VLAN')
	];
	const taggedVlans = policy.taggedVlans ?? [];

	if (policy.untaggedVlan !== undefined) {
		parts.push(
			`<span class="font-mono text-xs cursor-help" title="Native (untagged) VLAN">native ${escapeHTML(String(policy.untaggedVlan))}</span>`
		);
	}

	if (taggedVlans.length > 0) {
		const visible = taggedVlans.slice(0, MAX_VISIBLE_TAGGED_VLANS);
		const hiddenCount = taggedVlans.length - visible.length;
		const hiddenSuffix = hiddenCount > 0 ? ` +${hiddenCount}` : '';
		const taggedTitle =
			hiddenCount > 0 ? `Allowed tagged VLANs: ${taggedVlans.join(',')}` : 'Allowed tagged VLANs';
		parts.push(
			`<span class="font-mono text-xs cursor-help" title="${escapeHTML(taggedTitle)}">${escapeHTML(visible.join(','))}${hiddenSuffix}</span>`
		);
	}

	return parts;
}

const POLICY_PART_SEPARATOR = '<span class="text-muted-foreground/50">·</span>';

export function formatVlanPolicy(policy?: VLANPortPolicy): string {
	const parts = vlanPolicyParts(policy);
	if (parts.length === 0) return '-';

	return `<span class="inline-flex items-center gap-1.5">${parts.join(POLICY_PART_SEPARATOR)}</span>`;
}

function formatPortPolicy(port: VLANPolicySourcePort): string {
	const parts = [`<span>${escapeHTML(port.name)}</span>`, ...vlanPolicyParts(port.vlanPolicy)];
	return `<span class="inline-flex items-center gap-1.5">${parts.join(POLICY_PART_SEPARATOR)}</span>`;
}

export function formatPortsCell(ports: VLANPolicySourcePort[]): string {
	const visible = ports.slice(0, MAX_VISIBLE_PORT_LINES);
	const hidden = ports.slice(MAX_VISIBLE_PORT_LINES);
	const lines = visible.map((port) => formatPortPolicy(port)).join('<br/>');
	if (hidden.length === 0) return lines;

	const hiddenDetail = escapeHTML(hidden.map((port) => portPolicyText(port)).join(', '));
	return `${lines}<br/><span class="text-muted-foreground text-xs cursor-help" title="${hiddenDetail}">+${hidden.length} more</span>`;
}
