/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { APIResponse } from '$lib/types/common';
import { getAPIErrorText } from '$lib/utils/http';
import { toast } from 'svelte-sonner';

export interface ContainerError {
	title: string;
	detail: string;
}

const titles: Record<string, string> = {
	lifecycle_task_in_progress: 'Another action is already running for this container',
	container_service_not_enabled: 'Containers are not enabled on this node',
	container_not_found: 'Container not found',
	container_not_running: "The container isn't running",
	container_creation_pending: 'The container is still being created',
	container_name_already_in_use: 'Another container already has this name',
	container_id_already_in_use: 'Another container already has this ID',
	invalid_container_name: 'Names can only use letters, numbers and hyphens',
	invalid_container_id: 'Invalid container ID',
	invalid_hostname: 'Hostnames can only use letters, numbers and hyphens',
	pool_not_usable: "This pool isn't available for containers",
	invalid_image: 'Enter an image to run',
	invalid_os: 'Invalid image OS',
	invalid_work_dir: 'The working directory must be an absolute path',
	invalid_env_name: 'Invalid environment variable name',
	invalid_restart_policy: 'Invalid restart policy',
	invalid_cores: 'Too many CPU cores',
	invalid_memory: 'Invalid memory limit',
	invalid_ip: "This address can't be used",
	invalid_address_outside_subnet: 'The address is outside the network',
	host_network_cannot_use_ip_or_ports:
		"Containers on the host network can't have their own address or published ports",
	switch_network_cannot_publish_ports:
		"Containers on a switch are reached at their own address and can't publish ports",
	invalid_port_protocol: 'Ports must be TCP or UDP',
	invalid_port_mapping: 'Invalid port mapping',
	duplicate_host_port: 'A host port is published twice',
	invalid_mount_target: 'Each mount needs its own absolute path in the container',
	invalid_mount_type: 'Invalid mount type',
	invalid_mount_path: 'The host directory must exist',
	invalid_mount_dataset: 'Choose a ZFS dataset to mount',
	invalid_volume_name: 'Volume names can only use letters, numbers, dots, dashes and underscores',
	invalid_volume_permissions: 'Invalid volume owner or mode',
	dataset_not_found: 'ZFS dataset not found',
	invalid_annotation: 'Invalid jail permission',
	invalid_extra_host: 'Extra hosts must look like name:address',
	invalid_shm_size: 'Invalid shared memory size',
	container_network_not_found: 'Network not found',
	container_network_in_use: 'Containers still use this network',
	invalid_network_name: 'Invalid network name',
	invalid_subnet: 'Invalid subnet',
	subnet_in_use: 'This subnet is already in use',
	no_free_subnet: 'No free subnet is left for a new network',
	switch_not_found: 'Switch not found',
	switch_network_requires_subnet_and_gateway: 'Networks on a switch need a subnet and gateway',
	invalid_registry_credential: 'Enter a registry, username and password',
	podman_not_installed: "Podman isn't installed on this node",
	failed_to_apply_container_limit: "Couldn't apply the CPU or memory limit",
	container_limits_require_racct:
		'CPU and memory limits need kern.racct.enable=1 in /boot/loader.conf and a reboot',
	catalog_unreachable: "Couldn't reach the daemonless catalog",
	catalog_setting_required: 'A required setting is empty',
	catalog_stack_exists: "This app's stack already exists on this node",
	catalog_app_not_found: 'App not found in the catalog',
	invalid_catalog_manifest: "The app's catalog entry couldn't be read"
};

const podmanTitles: Record<string, string> = {
	start: "Couldn't start the container",
	create: "Couldn't create the container",
	pull: "Couldn't pull the image",
	stop: "Couldn't stop the container",
	rm: "Couldn't remove the container",
	rmi: "Couldn't remove the image",
	logs: "Couldn't read the container's logs"
};

const podmanHints: [RegExp, (match: RegExpMatchArray) => string][] = [
	[
		/cannot listen on the (TCP|UDP) port: listen \w+ .*?:(\d+): bind: address already in use/i,
		(match) =>
			`Port ${match[2]}/${match[1].toLowerCase()} is already in use on this host. Choose another host port on the Network tab.`
	],
	[
		/manifest unknown|name unknown|reference does not exist|repository .* not found/i,
		() => "The image or tag doesn't exist in the registry. Check its name."
	],
	[
		/unauthorized|authentication required|requested access to the resource is denied/i,
		() => 'The registry refused access. Add a login under Containers → Registries.'
	],
	[
		/no such host|i\/o timeout|connection refused|network is unreachable|TLS handshake timeout/i,
		() => "Couldn't reach the registry. Check the node's network connection and DNS."
	],
	[
		/no image found in (image index|manifest list) for architecture|does not match the expected platform/i,
		() => "The image isn't built for this host. Try another tag, or set the image OS."
	],
	[/no space left on device/i, () => 'The pool is out of space.']
];

const words = (code: string) => code.charAt(0).toUpperCase() + code.slice(1).replaceAll('_', ' ');

export function describeContainerError(error: string | APIResponse): ContainerError {
	const text = (typeof error === 'string' ? error : getAPIErrorText(error)).trim();
	const match = text.match(/^([a-z][a-z0-9_]*)(?::\s*([\s\S]*))?$/);
	if (!match) return { title: text, detail: '' };

	const [, code, rest = ''] = match;
	const member = code === 'container_stack_failed' && rest.match(/^([\w.-]+): ([\s\S]*)$/);
	if (member) {
		const problem = describeContainerError(member[2]);
		return { ...problem, title: `${member[1]}: ${problem.title}` };
	}
	let detail = (rest.match(/^Error: [\s\S]*/m)?.[0] ?? rest)
		.replace(/unable to \w+ container "[0-9a-f]{64}": /g, '')
		.replace(/^Error: /, '')
		.trim();
	const hint = podmanHints.find(([pattern]) => pattern.test(detail));
	if (hint) detail = hint[1](detail.match(hint[0])!);

	const command = code.match(/^podman_(\w+)_failed$/)?.[1];
	const title = command
		? (podmanTitles[command] ?? 'Podman reported an error')
		: (titles[code] ?? words(code));
	return { title, detail: detail.charAt(0).toUpperCase() + detail.slice(1) };
}

export function toastContainerError(error: string | APIResponse) {
	const { title, detail } = describeContainerError(error);
	toast.error(title, { description: detail || undefined, position: 'bottom-center' });
}
