<script lang="ts">
	import { getJailById } from '$lib/api/jail/jail';
	import { getSwitches } from '$lib/api/network/switch';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { Button } from '$lib/components/ui/button/index.js';
	import Inherit from '$lib/components/custom/Jail/Network/Inherit.svelte';
	import { untrack } from 'svelte';

	interface Data {
		ctId: number;
		jail: Jail;
		jailState: JailState;
		switches: SwitchList;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

	const jail = resource(
		() => `jail-${data.ctId}`,
		async (key, prevKey, { signal }) => {
			const jail = await getJailById(data.ctId, 'ctid');
			updateCache(key, jail);
			return jail;
		},
		{
			initialValue: data.jail
		}
	);

	const jState = resource(
		() => `jail-${data.ctId}-state`,
		async (key, prevKey, { signal }) => {
			const jail = await getJailById(data.ctId, 'ctid');
			updateCache(key, jail);
			return jail;
		},
		{
			initialValue: data.jail
		}
	);

	const networkSwitches = resource(
		() => `network-switches`,
		async (key, prevKey, { signal }) => {
			const switches = await getSwitches();
			updateCache(key, switches);
			return switches;
		},
		{
			initialValue: data.switches
		}
	);

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			untrack(() => {
				jail.refetch();
				jState.refetch();
				reload = false;
			});
		}
	});

	let modals = $state({
		inherit: {
			open: false
		}
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button
			onclick={() => {
				modals.inherit.open = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5"
		>
			<div class="flex items-center">
				{#if jail.current.inheritIPv4 || jail.current.inheritIPv6}
					<span class="icon-[mdi--close-network] mr-1 h-4 w-4"></span>
					<span>Disinherit Network</span>
				{:else}
					<span class="icon-[mdi--plus-network] mr-1 h-4 w-4"></span>
					<span>Inherit Network</span>
				{/if}
			</div>
		</Button>
	</div>
</div>

{#if modals.inherit.open}
	<Inherit bind:open={modals.inherit.open} jail={jail.current} bind:reload />
{/if}
