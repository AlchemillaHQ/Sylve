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
	import Icon from '@iconify/svelte';
	import { useQueries } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		vms: VM[];
		vm: VM;
		domain: VMDomain;
		interfaces: Iface[];
		switches: SwitchList;
		node: string;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();
	const results = useQueries([
		{
			queryKey: ['networkInterfaces'],
			queryFn: async () => {
				return await getInterfaces();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.interfaces,
			onSuccess: (data: Iface[]) => {
				updateCache('networkInterfaces', data);
			}
		},
		{
			queryKey: ['networkSwitches'],
			queryFn: async () => {
				return await getSwitches();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.switches,
			onSuccess: (data: SwitchList) => {
				updateCache('networkSwitches', data);
			}
		},
		{
			queryKey: ['vms'],
			queryFn: async () => {
				return getVMs();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.vms,
			onSuccess: (data: VM[]) => {
				updateCache('vms', data);
			}
		},
		{
			queryKey: [`vm-domain-${data.vm.vmId}`],
			queryFn: async () => {
				return await getVMDomain(data.vm.vmId);
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.domain,
			onSuccess: (uData: VMDomain) => {
				updateCache(`vm-domain-${data.vm.vmId}`, uData);
			}
		},
		{
			queryKey: ['networkObjects'],
			queryFn: async () => {
				return await getNetworkObjects();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.networkObjects,
			onSuccess: (data: NetworkObject[]) => {
				updateCache('networkObjects', data);
			}
		}
	]);

	let interfaces = $derived($results[0].data || []);
	let switches = $derived($results[1].data || {});
	let vms = $derived($results[2].data || []);
	let vm = $derived(vms.find((vm) => vm.vmId === Number(data.node)));
	let domain = $derived(($results[3].data as VMDomain) || {});
	let networkObjects = $derived($results[4].data || []);

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
					sw = switches.standard?.find((s) => s.id === network.switchId) ?? null;
				} else if (network.switchType === 'manual') {
					sw = switches.manual?.find((s) => s.id === network.switchId) ?? null;
				}

				if (sw) {
					const macObj = networkObjects.find((obj) => obj.id === network.macId);
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
			...(switches.standard ?? []).map((s) => ({
				...s,
				uid: `standard-${s.id}`
			})),
			...(switches.manual ?? []).map((s) => ({
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
	{#if domain && domain.status === 'Shutoff'}
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
					<Icon icon="gg:remove" class="mr-1 h-4 w-4" />
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
			title={domain && domain.status !== 'Shutoff' ? 'VM must be shut off to attach storage' : ''}
			disabled={domain && domain.status !== 'Shutoff'}
		>
			<div class="flex items-center">
				<Icon icon="gg:add" class="mr-1 h-4 w-4" />
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
			let response = await detachNetwork(vm?.vmId as number, properties.detach.id as number);
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

<Network bind:open={properties.attach.open} {switches} {vms} {networkObjects} vm={vm ?? null} />
