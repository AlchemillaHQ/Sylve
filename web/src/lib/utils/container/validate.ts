/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { ContainerCreateRequest, ContainerSpec } from '$lib/types/container/container';

export interface SpecProblem {
	message: string;
	tab: 'Basic' | 'Image' | 'Network' | 'Storage' | 'Resources';
}

const nameRegex = /^[A-Za-z0-9][A-Za-z0-9-]{0,62}$/;
const envRegex = /^[A-Za-z_][A-Za-z0-9_]*$/;
const volumeRegex = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$/;

const isPort = (value: number) => Number.isInteger(value) && value > 0 && value < 65536;

export function specProblem(spec: ContainerSpec): SpecProblem | null {
	if (!spec.image.trim()) return { message: 'Choose an image to run', tab: 'Image' };
	if (spec.workDir && !spec.workDir.startsWith('/')) {
		return { message: 'The working directory must be an absolute path', tab: 'Image' };
	}
	const names = new Set<string>();
	for (const { name } of spec.env.filter((env) => env.name.trim())) {
		if (!envRegex.test(name.trim())) {
			return { message: `Invalid environment variable name: ${name}`, tab: 'Image' };
		}
		if (names.has(name.trim())) {
			return { message: `The environment variable ${name} is set twice`, tab: 'Image' };
		}
		names.add(name.trim());
	}

	if (spec.hostname && !nameRegex.test(spec.hostname)) {
		return { message: 'Hostnames can only use letters, numbers and hyphens', tab: 'Network' };
	}
	const published = new Set<string>();
	for (const port of spec.ports) {
		if (!isPort(Number(port.hostPort)) || !isPort(Number(port.containerPort))) {
			return { message: 'Ports must be numbers from 1 to 65535', tab: 'Network' };
		}
		const key = `${port.hostPort}/${port.protocol}`;
		if (!['tcp', 'udp'].includes(port.protocol)) {
			return { message: `Ports must be TCP or UDP, not ${port.protocol}`, tab: 'Network' };
		}
		if (published.has(key)) return { message: `Port ${key} is published twice`, tab: 'Network' };
		published.add(key);
	}

	const targets = new Set<string>();
	for (const [index, mount] of spec.mounts.entries()) {
		const which = mount.target ? `the mount at ${mount.target}` : `mount ${index + 1}`;
		if (!mount.target.startsWith('/') || mount.target === '/') {
			return { message: `Give ${which} an absolute path in the container`, tab: 'Storage' };
		}
		if (targets.has(mount.target)) {
			return { message: `Two mounts use ${mount.target}`, tab: 'Storage' };
		}
		targets.add(mount.target);
		if (mount.type === 'dataset' && !mount.source) {
			return { message: `Choose a ZFS dataset for ${which}`, tab: 'Storage' };
		}
		if (mount.type === 'volume' && !volumeRegex.test(mount.source)) {
			return {
				message: `Give ${which} a volume name of letters, numbers, dots, dashes or underscores`,
				tab: 'Storage'
			};
		}
		if (mount.type === 'path' && !mount.source.startsWith('/')) {
			return { message: `Give ${which} an absolute host directory`, tab: 'Storage' };
		}
	}
	return null;
}

export function createProblem(form: ContainerCreateRequest): SpecProblem | null {
	if (!nameRegex.test(form.name)) {
		return { message: 'Names can only use letters, numbers and hyphens', tab: 'Basic' };
	}
	const id = Number(form.ocId);
	if (!Number.isInteger(id) || id < 1 || id > 9999) {
		return { message: 'The container ID must be from 1 to 9999', tab: 'Basic' };
	}
	if (!form.pool) return { message: 'Choose a pool', tab: 'Basic' };
	return specProblem(form);
}
