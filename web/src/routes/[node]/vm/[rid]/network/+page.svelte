<script lang="ts">
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import { detachNetwork } from '$lib/api/vm/network';
	import { getVmByIdResult } from '$lib/api/vm/vm';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Network from '$lib/components/custom/VM/Hardware/Network.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { NetworkObject } from '$lib/types/network/object';
	import {
		emptySwitchList,
		isSwitchList,
		type ManualSwitch,
		type StandardSwitch,
		type SwitchList
	} from '$lib/types/network/switch';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { resource, watch } from 'runed';
	import { getContext, onMount, untrack } from 'svelte';

	interface Data {
		vm: VM;
		switches: SwitchList;
		rid: number;
		node: string;
		networkObjects: NetworkObject[];
		loadErrors: APIResponse[];
	}

	let { data }: { data: Data } = $props();
	const initialData = untrack(() => data);
	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');

	const lastSwitchesByNode: Record<string, SwitchList> = Object.create(null);
	lastSwitchesByNode[initialData.node] = initialData.switches;
	const switches = resource(
		() => data.node,
		async (node) => {
			const result = await getSwitches(node);
			if (!isSwitchList(result)) {
				handleAPIError(result);
				return lastSwitchesByNode[node] ?? emptySwitchList();
			}
			lastSwitchesByNode[node] = result;
			await updateCache('network-switches', result, node);
			return result;
		},
		{
			initialValue: initialData.switches
		}
	);

	const vmIdentity = (node: string, rid: number) => `${node}\u0000${rid}`;
	const lastVMByIdentity: Record<string, VM> = Object.create(null);
	lastVMByIdentity[vmIdentity(initialData.node, initialData.rid)] = initialData.vm;
	const vm = resource(
		() => [data.node, data.rid] as const,
		async ([node, rid]) => {
			const result = await getVmByIdResult(rid, { hostname: node });
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastVMByIdentity[vmIdentity(node, rid)] ?? data.vm;
			}
			lastVMByIdentity[vmIdentity(node, rid)] = result;
			await updateCache(`vm-${rid}`, result, node);
			return result;
		},
		{
			initialValue: initialData.vm
		}
	);

	const lastNetworkObjectsByNode: Record<string, NetworkObject[]> = Object.create(null);
	lastNetworkObjectsByNode[initialData.node] = initialData.networkObjects;
	const networkObjects = resource(
		() => data.node,
		async (node) => {
			const result = await getNetworkObjects(node);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastNetworkObjectsByNode[node] ?? [];
			}
			lastNetworkObjectsByNode[node] = result;
			await updateCache('network-objects', result, node);
			return result;
		},
		{
			initialValue: initialData.networkObjects
		}
	);

	function refreshData() {
		vm.refetch();
		switches.refetch();
		networkObjects.refetch();
	}

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	let isLifecycleActive = $derived(!!domain.current?.pendingAction);
	let isDomainShutoff = $derived(
		!isLifecycleActive &&
			String(domain.current?.status || '')
				.trim()
				.toLowerCase() === 'shutoff'
	);

	function generateTableData() {
		const rows: Row[] = [];
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{
				field: 'enabled',
				title: 'Status',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return value
						? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
						: renderWithIcon('mdi:close-circle', 'Disabled', 'text-red-500');
				}
			},
			{ field: 'name', title: 'Name' },
			{ field: 'mac', title: 'MAC Address' },
			{
				field: 'emulation',
				title: 'Emulation',
				formatter(cell: CellComponent) {
					const value = cell.getValue();
					if (value === 'virtio') return 'VirtIO';
					if (value === 'e1000') return 'E1000';
					return value;
				}
			}
		];

		for (const network of vm.current.networks) {
			let sw: StandardSwitch | ManualSwitch | null = null;
			if (network.switchType === 'standard') {
				sw =
					switches.current.standard.find((candidate) => candidate.id === network.switchId) ?? null;
			} else if (network.switchType === 'manual') {
				sw = switches.current.manual.find((candidate) => candidate.id === network.switchId) ?? null;
			}

			const macObject =
				networkObjects.current.find((object) => object.id === network.macId) ?? null;
			const macAddress = macObject?.entries?.[0]?.value || network.mac || '';
			rows.push({
				id: network.id,
				name: sw?.name || `Unknown ${network.switchType} switch (${network.switchId})`,
				mac: macObject
					? `${macObject.name} (${macAddress || 'No address'})`
					: macAddress || 'Unknown MAC',
				macObject,
				emulation: network.emulation || 'Unknown',
				enabled: network.enable
			});
		}

		return { rows, columns };
	}

	let table = $derived(generateTableData());
	let activeRows: Row[] = $state([]);
	let query = $state('');
	let usable = $derived([
		...switches.current.standard.map((networkSwitch) => ({
			...networkSwitch,
			uid: `standard-${networkSwitch.id}`
		})),
		...switches.current.manual.map((networkSwitch) => ({
			...networkSwitch,
			uid: `manual-${networkSwitch.id}`
		}))
	]);

	function createPageOptions() {
		return {
			attach: { open: false },
			detach: {
				open: false,
				id: null as number | null,
				name: ''
			},
			edit: {
				open: false,
				id: null as number | null
			}
		};
	}

	let properties = $state(createPageOptions());
	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (!value) return;
			refreshData();
			reload = false;
		}
	);
</script>

{#snippet button(type: string)}
	{#if isDomainShutoff}
		{#if type === 'detach' && activeRows.length === 1}
			<Button
				onclick={() => {
					properties.detach.open = true;
					properties.detach.id = activeRows[0].id as number;
					properties.detach.name = activeRows[0].name as string;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[gg--remove]" size="h-4 w-4" gap="gap-1" title="Detach" />
			</Button>
		{/if}

		{#if type === 'edit' && activeRows.length === 1}
			<Button
				onclick={() => {
					properties.edit.open = true;
					properties.edit.id = activeRows[0].id as number;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button
			onclick={() => {
				if (usable.length === 0) {
					toast.error('No network switches are available to attach', {
						position: 'bottom-center'
					});
					return;
				}
				properties.attach.open = true;
			}}
			size="sm"
			class="h-6"
			title={!isDomainShutoff ? 'VM must be shut off to attach network' : ''}
			disabled={!isDomainShutoff}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-1" title="New" />
		</Button>

		{@render button('edit')}
		{@render button('detach')}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name="networks-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

<AlertDialog
	open={properties.detach.open}
	customTitle={`This will detach the VM <b>${escapeHTML(vm.current.name)}</b> from the switch <b>${escapeHTML(properties.detach.name)}</b>. The MAC object will NOT be deleted.`}
	actions={{
		onConfirm: async () => {
			const response = await detachNetwork(data.rid, properties.detach.id as number, {
				hostname: data.node
			});
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to detach network', { position: 'bottom-center' });
			} else {
				activeRows = [];
				toast.success('Network detached', { position: 'bottom-center' });
				reload = true;
			}
			properties.detach.open = false;
		},
		onCancel: () => {
			properties = createPageOptions();
		}
	}}
/>

{#if properties.attach.open}
	<Network
		bind:open={properties.attach.open}
		node={data.node}
		switches={switches.current}
		networkObjects={networkObjects.current}
		vm={vm.current}
		networkId={null}
		bind:reload
	/>
{/if}

{#if properties.edit.open}
	<Network
		bind:open={properties.edit.open}
		node={data.node}
		switches={switches.current}
		networkObjects={networkObjects.current}
		vm={vm.current}
		networkId={properties.edit.id}
		bind:reload
	/>
{/if}
