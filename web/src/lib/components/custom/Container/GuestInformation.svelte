<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { getContainerImageInfo } from '$lib/api/container/container';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { ContainerImageInfoSchema, type ContainerDetail } from '$lib/types/container/container';
	import { SEVEN_DAYS } from '$lib/utils.js';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { cachedFetch, getAPIErrorText, isAPIResponse } from '$lib/utils/http';
	import { describeContainerError } from '$lib/utils/container/errors';
	import { formatPort } from '$lib/utils/container/ports';
	import { resource } from 'runed';

	let { container, node }: { container: ContainerDetail; node: string } = $props();
	let view = $state('container');

	const image = resource(
		() => container.runtimeId,
		async (runtimeId) => {
			if (!runtimeId) return null;
			const result = await cachedFetch(
				`container-image-${runtimeId}`,
				() => getContainerImageInfo(container.ocId, { hostname: node }),
				SEVEN_DAYS,
				false,
				node,
				ContainerImageInfoSchema
			);
			if (isAPIResponse(result)) throw new Error(getAPIErrorText(result));
			return result;
		},
		{ initialValue: null }
	);

	const short = (id: string) => id.replace(/^sha256:/, '').slice(0, 12) || '-';
	const list = (values: string[]) => values.join(', ') || 'None';

	let rows = $derived.by((): [string, string, string?][] => {
		if (view === 'container') {
			return [
				...(container.title ? [['Name', container.name] as [string, string]] : []),
				['OS', container.os === 'freebsd' ? 'FreeBSD' : 'Linux'],
				['Image', container.image],
				['Pool', container.pool],
				...(container.stack
					? [['Stack', container.stackTitle || container.stack] as [string, string]]
					: []),
				['Podman ID', short(container.runtimeId)]
			];
		}
		if (view === 'image') {
			const info = image.current;
			if (!info) return [];
			return [
				['Title', info.title || '-'],
				['Description', info.description || '-'],
				[
					'Version',
					`${info.version || '-'} (${short(info.id)} / ${short(info.digest)})`,
					`Image ID: ${info.id}\nDigest: ${info.digest}`
				],
				['Size', formatBytesBinary(info.size)],
				['Homepage', info.url || '-'],
				['Exposed Ports', list(info.exposedPorts)],
				['Volumes', list(info.volumes)]
			];
		}
		const network = container.network;
		if (!network) return [];
		const runtime = container.runtime;
		return [
			['Network', `${network.name} (${network.switchId ? 'switch' : 'NAT'})`],
			['Bridge', network.bridge],
			['Subnet', network.subnet],
			['Gateway', network.gateway],
			['Addresses', runtime?.ips.join(', ') || container.ip || '-'],
			['MAC Address', runtime?.macs.join(', ') || '-'],
			['Hostname', container.hostname || container.name],
			['Published Ports', list(container.ports.map(formatPort))]
		];
	});
</script>

{#snippet empty(icon: string, text: string)}
	<div class="text-muted-foreground flex flex-col items-center justify-center gap-2 py-6 text-sm">
		<span class="{icon} h-10 w-10"></span>
		<span>{text}</span>
	</div>
{/snippet}

<div class="px-4 pb-4">
	<Card.Root class="w-full gap-0 p-4">
		<Card.Header class="p-0">
			<Card.Description
				class="text-md flex items-center justify-between font-normal text-blue-600 dark:text-blue-500"
			>
				<div class="flex items-center gap-2">
					<span class="icon icon-[lucide--container] h-6 w-6"></span>
					<span>Guest Information</span>
				</div>
				<SimpleSelect
					options={[
						{ label: 'Container', value: 'container' },
						{ label: 'Image', value: 'image' },
						{ label: 'Network', value: 'network' }
					]}
					bind:value={view}
					classes={{ trigger: 'h-6! min-w-[100px] py-0 text-foreground' }}
					onChange={(value) => (view = value)}
				/>
			</Card.Description>
		</Card.Header>
		<Card.Content class="mt-3 p-0">
			{#if rows.length > 0}
				<Table.Root class="w-full">
					<Table.Body>
						{#each rows as [label, value, title] (label)}
							<Table.Row>
								<Table.Cell class="w-48 font-medium">{label}</Table.Cell>
								<Table.Cell class="break-all whitespace-normal" {title}>{value}</Table.Cell>
							</Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			{:else if view === 'network'}
				{@render empty(
					'icon-[mdi--server-network]',
					"This container shares the host's network stack"
				)}
			{:else if image.error}
				{@render empty(
					'icon-[mdi--alert-circle-outline]',
					describeContainerError(image.error.message).title
				)}
			{:else}
				{@render empty('icon-[mdi--loading] animate-spin', 'Loading image details…')}
			{/if}
		</Card.Content>
	</Card.Root>
</div>
