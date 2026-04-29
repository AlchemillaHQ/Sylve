<script lang="ts">
	import { deleteNetwork, getJailById } from '$lib/api/jail/jail';
	import { getSwitches } from '$lib/api/network/switch';
	import type { Jail } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { Button } from '$lib/components/ui/button/index.js';
	import Inherit from '$lib/components/custom/Jail/Network/Inherit.svelte';
	import { untrack } from 'svelte';
	import { ipGatewayFormatter, macFormtter } from '$lib/utils/jail/network';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { getNetworkObjects } from '$lib/api/network/object';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import { toast } from 'svelte-sonner';
	import Form from '$lib/components/custom/Jail/Network/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Data {
		ctId: number;
		jail: Jail;
		switches: SwitchList;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const jail = resource(
		() => `jail-${data.ctId}`,
		async (key) => {
			const jail = await getJailById(data.ctId, 'ctid');
			updateCache(key, jail);
			return jail;
		},
		{
			initialValue: data.jail
		}
	);

	// svelte-ignore state_referenced_locally
	const jState = resource(
		() => `jail-${data.ctId}-state`,
		async (key) => {
			const jail = await getJailById(data.ctId, 'ctid');
			updateCache(key, jail);
			return jail;
		},
		{
			initialValue: data.jail
		}
	);

	// svelte-ignore state_referenced_locally
	const networkSwitches = resource(
		() => `network-switches`,
		async (key) => {
			const switches = await getSwitches();
			updateCache(key, switches);
			return switches;
		},
		{
			initialValue: data.switches
		}
	);

	// svelte-ignore state_referenced_locally
	const networkObjects = resource(
		() => `network-objects`,
		async (key) => {
			const objects = await getNetworkObjects();
			updateCache(key, objects);
			return objects;
		},
		{
			initialValue: data.networkObjects
		}
	);

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			untrack(() => {
				jail.refetch();
				jState.refetch();
				networkSwitches.refetch();
				networkObjects.refetch();
				reload = false;
			});
		}
	});

	let modals = $state({
		create: {
			open: false
		},
		inherit: {
			open: false
		},
		delete: {
			open: false
		},
		edit: {
			open: false,
			id: null as number | null
		}
	});

	let inherited = $derived.by(() => {
		if (jail) {
			return jail.current.inheritIPv4 || jail.current.inheritIPv6;
		}

		return false;
	});

	let table = $derived.by(() => {
		const columns: Column[] = [
			{
				title: 'Name',
				field: 'name'
			},
			{
				title: 'Switch',
				field: 'switch'
			},
			{
				title: 'MAC',
				field: 'mac'
			},
			{
				title: 'IPv4',
				field: 'ipv4',
				formatter: 'html'
			},
			{
				title: 'IPv6',
				field: 'ipv6',
				formatter: 'html'
			}
		];

		if (jail) {
			if (inherited) {
				return {
					rows: [],
					columns
				};
			} else {
				const rows: Row[] = [];
				for (const network of jail.current.networks) {
					let ipv4 = '';
					let ipv6 = '';

					if (network.dhcp) {
						ipv4 = 'DHCP';
					} else {
						if (network.ipv4Id && network.ipv4GwId) {
							if (!isAPIResponse(networkObjects.current)) {
								ipv4 = ipGatewayFormatter(networkObjects.current, network.ipv4Id, network.ipv4GwId);
							}
						} else {
							ipv4 = '-';
						}
					}

					if (network.slaac) {
						ipv6 = 'SLAAC';
					} else {
						if (network.ipv6Id && network.ipv6GwId) {
							if (!isAPIResponse(networkObjects.current)) {
								ipv6 = ipGatewayFormatter(networkObjects.current, network.ipv6Id, network.ipv6GwId);
							}
						} else {
							ipv6 = '-';
						}
					}

					let name = '';
					if (network.switchType === 'standard') {
						name =
							networkSwitches.current.standard?.find((sw) => sw.id === network.switchId)?.name ||
							'';
					} else {
						name =
							networkSwitches.current.manual?.find((sw) => sw.id === network.switchId)?.name || '';
					}

					if (!isAPIResponse(networkObjects.current)) {
						rows.push({
							name: network.name,
							id: network.id,
							switch: name,
							mac: macFormtter(networkObjects.current, network.macId || 0),
							ipv4,
							ipv6
						});
					}
				}

				return {
					rows,
					columns
				};
			}
		}

		return {
			rows: [],
			columns
		};
	});

	let activeRows: Row[] = $state([] as Row[]);
	let activeRow: Row | null = $derived(
		activeRows.length > 0 ? (activeRows[0] as Row) : ({} as Row)
	);

	let query: string = $state('');

	async function handleSwitchDelete() {
		if (!jail) return;

		const response = await deleteNetwork(data.ctId, Number(activeRow?.id ?? 0));
		reload = true;
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to delete network', {
				position: 'bottom-center'
			});
		} else {
			toast.success('Network deleted', {
				position: 'bottom-center'
			});
		}

		modals.delete.open = false;
		activeRows = [];
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		{#if !inherited}
			<Button size="sm" class="h-6" onclick={() => (modals.create.open = !modals.create.open)}>
				<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-1" title="New" />
			</Button>
		{/if}

		<Button
			onclick={() => {
				modals.inherit.open = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5 {activeRows.length > 0 ? 'hidden' : ''}"
		>
			{#if jail.current.inheritIPv4 || jail.current.inheritIPv6}
				<SpanWithIcon
					icon="icon-[mdi--close-network]"
					size="h-4 w-4"
					gap="gap-1"
					title="Disinherit Network"
				/>
			{:else}
				<SpanWithIcon
					icon="icon-[mdi--plus-network]"
					size="h-4 w-4"
					gap="gap-1"
					title="Inherit Network"
				/>
			{/if}
		</Button>

		{#if activeRows.length > 0}
			<Button
				size="sm"
				class="h-6"
				variant="outline"
				onclick={() => {
					if (jail && activeRow) {
						modals.edit.open = true;
						modals.edit.id = activeRow.id as number;
					}
				}}
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit" />
			</Button>

			<Button
				size="sm"
				class="h-6"
				variant="outline"
				onclick={async () => {
					if (jail && activeRow) {
						modals.delete.open = true;
					}
				}}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
			</Button>
		{/if}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name="jail-networks-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if modals.inherit.open}
	<Inherit bind:open={modals.inherit.open} jail={jail.current} bind:reload />
{/if}

{#if modals.delete.open}
	<AlertDialog
		open={modals.delete.open}
		customTitle={`This will detach the jail from the switch <b>${activeRow.switch}</b>`}
		actions={{
			onConfirm: async () => {
				handleSwitchDelete();
			},
			onCancel: () => {
				modals.delete.open = false;
			}
		}}
	></AlertDialog>
{/if}

{#if modals.create.open && !isAPIResponse(networkSwitches.current) && !isAPIResponse(networkObjects.current) && jail}
	<Form
		bind:open={modals.create.open}
		jail={jail.current}
		bind:reload
		networkObjects={networkObjects.current}
		networkSwitches={networkSwitches.current}
		networkId={null}
	/>
{/if}

{#if modals.edit.open && !isAPIResponse(networkSwitches.current) && !isAPIResponse(networkObjects.current) && jail}
	<Form
		bind:open={modals.edit.open}
		jail={jail.current}
		bind:reload
		networkObjects={networkObjects.current}
		networkSwitches={networkSwitches.current}
		networkId={modals.edit.id}
	/>
{/if}
