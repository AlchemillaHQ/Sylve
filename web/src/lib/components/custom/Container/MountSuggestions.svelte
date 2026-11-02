<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { getCatalogTemplate } from '$lib/api/container/catalog';
	import type { ContainerMount } from '$lib/types/container/container';
	import { isAPIResponse } from '$lib/utils/http';
	import { resource } from 'runed';

	interface Props {
		catalogApp: string;
		node: string;
		mounts: ContainerMount[];
		onAdd: (mount: ContainerMount) => void;
		class?: string;
	}

	let { catalogApp, node, mounts, onAdd, class: className = '' }: Props = $props();

	const template = resource(
		() => `${node}/${catalogApp}`,
		async () => {
			const [source, id] = catalogApp.split('/');
			const result = await getCatalogTemplate(source, id, node);
			return isAPIResponse(result) ? null : result;
		},
		{ initialValue: null }
	);
	let directories = $derived(
		(template.current?.variables ?? []).filter((variable) => variable.target)
	);
</script>

{#if template.current && directories.length > 0}
	<div
		class="flex items-start gap-2 rounded-md border border-blue-500/30 bg-blue-500/10 px-3 py-2 text-sm {className}"
	>
		<span
			class="icon-[mdi--information-outline] mt-0.5 h-4 w-4 shrink-0 text-blue-600 dark:text-blue-400"
		></span>
		<div class="min-w-0 space-y-1.5">
			<p class="font-medium text-blue-600 dark:text-blue-400">
				Where {template.current.app.name} expects its data
			</p>
			<div class="flex flex-wrap gap-1.5">
				{#each directories as directory (directory.name)}
					{#if mounts.some((mount) => mount.target === directory.target)}
						<span
							class="text-muted-foreground inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs"
							title="Mounted"
						>
							<span class="icon-[mdi--check] h-3.5 w-3.5 text-green-600"></span>
							{directory.label || directory.name}
							<code class="text-[11px]">{directory.target}</code>
						</span>
					{:else}
						<button
							type="button"
							class="hover:bg-background inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs"
							title="Add a volume at {directory.target}"
							onclick={() =>
								onAdd({
									type: 'volume',
									source: directory.volume,
									target: directory.target,
									owner: directory.owner || undefined,
									readOnly: false
								})}
						>
							<span class="icon-[gg--add] h-3.5 w-3.5"></span>
							{directory.label || directory.name}
							<code class="text-[11px]">{directory.target}</code>
						</button>
					{/if}
				{/each}
			</div>
		</div>
	</div>
{/if}
