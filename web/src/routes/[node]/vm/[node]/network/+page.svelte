<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import { detachNetwork } from '$lib/api/vm/network';
	import { getVMDomain, getVMs } from '$lib/api/vm/vm';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
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
	import { resource, useInterval } from 'runed';
	import { untrack } from 'svelte';
	import { storage } from '$lib';

	interface Data {
		vms: VM[];
		vm: VM;
		domain: VMDomain;
		interfaces: Iface[];
		switches: SwitchList;
		rid: string;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

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

	const vms = resource(
		() => 'vms',
		async (key) => {
			const result = await getVMs();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.vms
		}
	);

	const domain = resource(
		() => `vm-domain-${data.vm.rid}`,
		async (key) => {
			const result = await getVMDomain(data.vm.rid);
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.domain
		}
	);

	const networkObjects = resource(
		() => 'networkObjects',
		async (key) => {
			const result = await getNetworkObjects();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.networkObjects
		}
	);

	useInterval(() => 1000, {
		callback: () => {
			if (storage.visible) {
				interfaces.refetch();
				switches.refetch();
				vms.refetch();
				domain.refetch();
				networkObjects.refetch();
			}
		}
	});

	$effect(() => {
		if (storage.visible) {
			untrack(() => {
				interfaces.refetch();
				switches.refetch();
				vms.refetch();
				domain.refetch();
				networkObjects.refetch();
			});
		}
	});

	let vm = $derived(vms.current.find((vm) => vm.rid === Number(data.rid)));

	function generateTableData() {
		const rows: Row[] = [];
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'name', title: 'Name' },
			{ field: 'mac', title: 'MAC Address' },
			{
				field: 'emulation',
				title: 'Emulation',
				formatter(cell: CellComponent, formatterParams, onRendered) {
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

		if (vm?.networks) {
			for (const network of vm.networks) {
				let sw: StandardSwitch | ManualSwitch | null = null;
				if (network.switchType === 'standard') {
					sw = switches.current.standard?.find((s) => s.id === network.switchId) ?? null;
				} else if (network.switchType === 'manual') {
					sw = switches.current.manual?.find((s) => s.id === network.switchId) ?? null;
				}

				if (sw) {
					const macObj = networkObjects.current.find((obj) => obj.id === network.macId);
					const mac =
						macObj && macObj.entries && macObj.entries.length > 0
							? macObj.entries[0].value
							: undefined;

					const row: Row = {
						id: network.id,
						name: sw.name || 'Unknown Switch',
						mac: `${macObj?.name} (${mac})` || 'Unknown MAC',
						macObject: macObj || null,
						emulation: network.emulation || 'Unknown'
					};

					rows.push(row);
				}
			}
		}

		return { rows, columns };
	}

	let table = $derived(generateTableData());
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query = $state('');
	let usable = $derived.by(() => {
		const used = new Set((vm?.networks ?? []).map((n) => `${n.switchType}-${n.switchId}`));
		return [
			...(switches.current.standard ?? []).map((s) => ({
				...s,
				uid: `standard-${s.id}`
			})),
			...(switches.current.manual ?? []).map((s) => ({
				...s,
				uid: `manual-${s.id}`
			}))
		].filter((s) => !used.has(s.uid));
	});

	let options = {
		attach: {
			open: false
		},
		detach: {
			open: false,
			id: null as number | null,
			name: ''
		}
	};

	let properties = $state(options);
</script>

{#snippet button(type: string)}
	{#if domain && domain.current.status === 'Shutoff'}
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
				<div class="flex items-center">
					<span class="icon-[gg--remove] mr-1 h-4 w-4"></span>

					<span>Detach</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button
			onclick={() => {
				if (vm) {
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
			title={domain && domain.current.status !== 'Shutoff'
				? 'VM must be shut off to attach storage'
				: ''}
			disabled={domain && domain.current.status !== 'Shutoff'}
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('detach')}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name={'networks-tt'}
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

<AlertDialog
	open={properties.detach.open}
	customTitle={`This will detach the VM <b>${vm?.name}</b> from the switch <b>${properties.detach.name}</b>`}
	actions={{
		onConfirm: async () => {
			let response = await detachNetwork(vm?.rid as number, properties.detach.id as number);
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

<Network
	bind:open={properties.attach.open}
	switches={switches.current}
	vms={vms.current}
	networkObjects={networkObjects.current}
	vm={vm ?? null}
/>
