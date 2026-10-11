<script lang="ts">
	import { storage } from '$lib';
	import { getNetworkGlobalConfig } from '$lib/api/network/global-config';
	import Config from '$lib/components/custom/Network/GlobalConfig/Config.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { NetworkGlobalConfig } from '$lib/types/network/global-config';
	import { useInterval, watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';

	let {
		data
	}: { data: { node: string; config: NetworkGlobalConfig | null; loadFailed: boolean } } = $props();
	const initial = untrack(() => data);
	let config = $state(initial.config);
	let loadFailed = $state(initial.loadFailed);
	let query = $state('');
	let modalOpen = $state(false);
	let reload = $state(false);
	let refreshing = $state(false);
	let polling = $state(false);
	let generation = 0;
	onDestroy(() => {
		generation++;
	});

	watch.pre(
		() => data,
		(next) => {
			generation++;
			config = next.config;
			loadFailed = next.loadFailed;
			refreshing = false;
			polling = false;
			reload = false;
			modalOpen = false;
			query = '';
		}
	);

	async function refresh(background = false) {
		if (refreshing || polling) return;
		if (background) polling = true;
		else refreshing = true;
		const node = data.node;
		const requestGeneration = generation;
		const canApply = () =>
			generation === requestGeneration && (!background || (!reload && !modalOpen));
		try {
			const result = await getNetworkGlobalConfig({ hostname: node });
			if (!canApply()) return;
			config = result;
			loadFailed = false;
		} catch {
			if (canApply()) loadFailed = true;
		} finally {
			if (generation === requestGeneration) {
				if (background) polling = false;
				else refreshing = false;
			}
		}
	}
	watch([() => reload, () => refreshing, () => polling], ([requested, refreshing, polling]) => {
		if (requested && !refreshing && !polling) {
			reload = false;
			void refresh();
		}
	});
	useInterval(3000, {
		callback: () => {
			if (storage.visible && !refreshing && !polling && !reload && !modalOpen) void refresh(true);
		}
	});

	function gateway(value: string | null): string {
		return value === null ? 'Unavailable' : value || 'None';
	}
	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property', formatter: 'plaintext' },
			{ title: 'Value', field: 'value', formatter: 'plaintext' }
		],
		rows: config
			? [
					{ id: 'hostname', property: 'Hostname (Read-only)', value: config.hostname },
					{
						id: 'mode',
						property: 'DNS Mode',
						value: config.dnsMode === 'manual' ? 'Manual (Sylve-managed)' : 'OS-managed'
					},
					{
						id: 'servers',
						property: 'Configured DNS Servers',
						value: config.dnsServers.join(', ') || 'OS-managed'
					},
					{
						id: 'effective-servers',
						property: 'Effective DNS Servers',
						value: config.effectiveDnsServers.join(', ') || 'None'
					},
					{
						id: 'search',
						property: 'Configured Search Domains',
						value: config.searchDomainsOverride
							? config.searchDomains.join(', ') || 'None (Search disabled)'
							: 'OS-managed'
					},
					{
						id: 'effective-search',
						property: 'Effective Search Domains',
						value: config.effectiveSearchDomains.join(', ') || 'None'
					},
					{
						id: 'aliases',
						property: 'Managed Host Aliases',
						value:
							config.hostAliases
								.map(
									(alias) => `${alias.address} — ${[alias.hostname, ...alias.aliases].join(', ')}`
								)
								.join('; ') || 'None'
					},
					{
						id: 'gateway4',
						property: 'IPv4 Default Gateway (Read-only)',
						value: gateway(config.ipv4Gateway)
					},
					{
						id: 'gateway6',
						property: 'IPv6 Default Gateway (Read-only)',
						value: gateway(config.ipv6Gateway)
					}
				]
			: []
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			size="sm"
			class="h-6"
			disabled={!config || loadFailed || refreshing}
			onclick={() => (modalOpen = true)}
			><SpanWithIcon
				icon="icon-[hugeicons--system-update-01]"
				size="h-4 w-4"
				gap="gap-2"
				title="Update"
			/></Button
		>
	</div>
	{#if loadFailed}<p class="text-destructive px-3 py-2 text-xs" role="alert">
			Unable to load the current global configuration. Changes are disabled until the connection
			recovers.
		</p>{/if}
	{#each config?.warnings ?? [] as warning, index (index)}<p
			class="text-destructive px-3 py-1 text-xs"
			role="alert"
		>
			{warning}
		</p>{/each}
	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name="network-global-config-tt"
			multipleSelect={false}
			borderTop={loadFailed || (config?.warnings.length ?? 0) > 0}
			bind:query
		/>
	</div>
</div>
{#if config && modalOpen}<Config bind:open={modalOpen} bind:reload {config} node={data.node} />{/if}
