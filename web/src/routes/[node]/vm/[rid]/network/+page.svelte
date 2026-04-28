<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import { detachNetwork } from '$lib/api/vm/network';
	import { getVmById } from '$lib/api/vm/vm';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Network from '$lib/components/custom/VM/Hardware/Network.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Iface } from '$lib/types/network/iface';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { ManualSwitch, StandardSwitch, SwitchList } from '$lib/types/network/switch';
	import type { VM, VMDomain } from '$lib/types/vm/vm';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { resource, useInterval, watch } from 'runed';
	import { storage } from '$lib';
	import { getContext } from 'svelte';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';

	interface Data {
		vm: VM;
		interfaces: Iface[];
		switches: SwitchList;
		rid: number;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');
	const lifecycleTask = getContext<{ current: LifecycleTask | null; refetch(): void }>(
		'vmLifecycleTask'
	);

	// svelte-ignore state_referenced_locally
	const interfaces = resource(
		() => 'networkInterfaces',
		async (key) => {
			const result = await getInterfaces();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.interfaces
		}
	);

	// svelte-ignore state_referenced_locally
	const switches = resource(
		() => 'networkSwitches',
		async (key) => {
			const result = await getSwitches();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.switches
		}
	);

	// svelte-ignore state_referenced_locally
	const vm = resource(
		() => `vm-${data.rid}`,
		async (key) => {
			const result = await getVmById(data.rid, 'rid');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.vm
		}
	);

	// svelte-ignore state_referenced_locally

	const networkObjects = resource(
		() => 'networkObjects',
		async (key) => {
			const result = await getNetworkObjects();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.networkObjects
		}
	);

	useInterval(() => 1000, {
		callback: () => {
			if (storage.visible) {
				interfaces.refetch();
				switches.refetch();
				vm.refetch();
				networkObjects.refetch();
			}
		}
	});

	watch(
		() => storage.visible,
		() => {
			interfaces.refetch();
			switches.refetch();
			vm.refetch();
			networkObjects.refetch();
		}
	);

	let isLifecycleActive = $derived(
		!!lifecycleTask.current && !!(lifecycleTask.current as LifecycleTask).action
	);
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
			{ field: 'name', title: 'Name' },
			{ field: 'mac', title: 'MAC Address' },
			{
				field: 'emulation',
				title: 'Emulation',
				formatter(cell: CellComponent) {
					const value = cell.getValue();
					if (value === 'virtio') {
						return 'VirtIO';
					} else if (value === 'e1000') {
						return 'E1000';
					}

					return value;
				}
			}
		];

		if (vm.current) {
			for (const network of vm.current.networks) {
				let sw: StandardSwitch | ManualSwitch | null = null;
				if (network.switchType === 'standard') {
					sw = switches.current.standard?.find((s) => s.id === network.switchId) ?? null;
				} else if (network.switchType === 'manual') {
					sw = switches.current.manual?.find((s) => s.id === network.switchId) ?? null;
				}

				if (sw) {
					if (Array.isArray(networkObjects.current)) {
						const macObj = networkObjects.current.find((obj) => obj.id === network.macId);
						const mac =
							macObj && macObj.entries && macObj.entries.length > 0
								? macObj.entries[0].value
								: undefined;

						const row: Row = {
							id: network.id,
							name: sw.name || 'Unknown Switch',
							mac: macObj ? `${macObj.name} (${mac})` : 'Unknown MAC',
							macObject: macObj || null,
							emulation: network.emulation || 'Unknown'
						};

						rows.push(row);
					}
				}
			}
		}

		return { rows, columns };
	}

	let table = $derived(generateTableData());
	let activeRows: Row[] | null = $state(null);
	let query = $state('');
	let usable = $derived.by(() => {
		return [
			...(switches.current.standard ?? []).map((s) => ({
				...s,
				uid: `standard-${s.id}`
			})),
			...(switches.current.manual ?? []).map((s) => ({
				...s,
				uid: `manual-${s.id}`
			}))
		];
	});

	let options = {
		attach: {
			open: false
		},
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

	let properties = $state(options);
</script>

{#snippet button(type: string)}
	{#if isDomainShutoff}
		{#if type === 'detach' && activeRows && activeRows.length === 1}
			<Button
				onclick={() => {
					if (activeRows) {
						properties.detach.open = true;
						properties.detach.id = activeRows[0].id as number;
						properties.detach.name = activeRows[0].name as string;
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[gg--remove]" size="h-4 w-4" gap="gap-1" title="Detach" />
			</Button>
		{/if}

		{#if type === 'edit' && activeRows && activeRows.length === 1}
			<Button
				onclick={() => {
					if (activeRows) {
						properties.edit.open = true;
						properties.edit.id = activeRows[0].id as number;
					}
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
				if (vm.current) {
					if (usable?.length === 0) {
						toast.error('No available/unused switches to attach to', {
							position: 'bottom-center'
						});

						return;
					}

					properties.attach.open = true;
				}
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
	customTitle={`This will detach the VM <b>${vm.current.name}</b> from the switch <b>${properties.detach.name}</b>`}
	actions={{
		onConfirm: async () => {
			let response = await detachNetwork(vm.current.rid as number, properties.detach.id as number);
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to detach network', {
					position: 'bottom-center'
				});
			} else {
				toast.success('Network detached', {
					position: 'bottom-center'
				});
			}

			activeRows = null;
			properties.detach.open = false;
		},
		onCancel: () => {
			properties.detach.open = false;
			properties = options;
		}
	}}
/>

{#if properties.attach.open && Array.isArray(networkObjects.current)}
	<Network
		bind:open={properties.attach.open}
		switches={switches.current}
		networkObjects={networkObjects.current}
		vm={vm.current ?? null}
		networkId={null}
	/>
{/if}

{#if properties.edit.open && Array.isArray(networkObjects.current)}
	<Network
		bind:open={properties.edit.open}
		switches={switches.current}
		networkObjects={networkObjects.current}
		vm={vm.current ?? null}
		networkId={properties.edit.id}
	/>
{/if}
