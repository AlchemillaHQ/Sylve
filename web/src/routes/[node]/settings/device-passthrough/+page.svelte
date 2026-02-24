<script lang="ts">
	import { addPPTDevice, getPCIDevices, getPPTDevices, removePPTDevice } from '$lib/api/system/pci';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import { type PCIDevice, type PPTDevice } from '$lib/types/system/pci';
	import { updateCache } from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/system/pci';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		pciDevices: PCIDevice[];
		pptDevices: PPTDevice[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let pptDevices = resource(
		() => 'ppt-devices',
		async () => {
			const result = await getPPTDevices();
			updateCache('ppt-devices', result);
			return result;
		},
		{
			initialValue: data.pptDevices
		}
	);

	// svelte-ignore state_referenced_locally
	let pciDevices = resource(
		() => 'pci-devices',
		async () => {
			const result = await getPCIDevices();
			updateCache('pci-devices', result);
			return result;
		},
		{
			initialValue: data.pciDevices
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				pciDevices.refetch();
				pptDevices.refetch();
				reload = false;
			}
		}
	);

	let tableData = $derived(generateTableData(pciDevices.current, pptDevices.current));
	let tableName: string = 'device-passthrough-tt';
	let query: string = $state('');
	let activeRow: Row[] | null = $state(null);

	let modalState = $state({
		isOpen: false,
		title: '',
		action: '',
		add: {
			domain: '',
			deviceId: ''
		},
		remove: {
			id: 0
		}
	});

	function addDevice(domain: string, deviceId: string) {
		const device = activeRow ? activeRow[0].device : '';
		const vendor = activeRow ? activeRow[0].vendor : '';

		modalState.isOpen = true;
		modalState.title = `Are you sure you want to pass through <b>${device}</b> by <b>${vendor}</b>? This will make it unavailable to the host.`;
		modalState.action = 'add';
		modalState.add.domain = domain;
		modalState.add.deviceId = deviceId;
	}

	function removeDevice(id: number) {
		const device = activeRow ? activeRow[0].device : '';
		const vendor = activeRow ? activeRow[0].vendor : '';
		modalState.isOpen = true;
		modalState.title = `Are you sure you want to remove passthrough for <b>${device}</b> by <b>${vendor}</b>? This will make it available to the host again.`;
		modalState.action = 'remove';
		modalState.remove.id = id;
	}
</script>

{#snippet button(type: string)}
	{#if activeRow !== null && activeRow.length === 1}
		{#if type === 'enable-passthrough' && !activeRow[0].name.startsWith('ppt')}
			<Button
				onclick={() =>
					activeRow && addDevice(activeRow[0].domain.toString(), activeRow[0].deviceId)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[wpf--disconnected] mr-1 h-4 w-4"></span>

					<span>Enable Passthrough</span>
				</div>
			</Button>
		{/if}

		{#if type === 'disable-passthrough' && activeRow[0].name.startsWith('ppt')}
			<Button
				onclick={() => activeRow && removeDevice(activeRow[0].pptId)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[wpf--connected] mr-1 h-4 w-4"></span>

					<span>Disable Passthrough</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		{@render button('enable-passthrough')}
		{@render button('disable-passthrough')}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name={tableName}
			bind:parentActiveRow={activeRow}
			bind:query
			multipleSelect={false}
		/>
	</div>
</div>

<AlertDialog
	open={modalState.isOpen}
	names={{ parent: '', element: modalState?.title || '' }}
	customTitle={modalState.title}
	actions={{
		onConfirm: async () => {
			if (modalState.action === 'add') {
				const result = await addPPTDevice(modalState.add.domain, modalState.add.deviceId);
				reload = true;
				if (result.status === 'success') {
					toast.success('Device added to passthrough', {
						position: 'bottom-center'
					});
				} else {
					toast.error('Failed to add device to passthrough', {
						position: 'bottom-center'
					});
				}

				modalState.isOpen = false;
			}

			if (modalState.action === 'remove') {
				const result = await removePPTDevice(modalState.remove.id.toString());
				reload = true;
				if (result.status === 'success') {
					toast.success('Device removed from passthrough', {
						position: 'bottom-center'
					});
				} else {
					let message = '';
					if (
						typeof result.error === 'string'
							? result.error.endsWith('in_use_by_vm')
							: Array.isArray(result.error) &&
								result.error.some((e) => typeof e === 'string' && e.endsWith('in_use_by_vm'))
					) {
						message = 'Device is in use by a VM, failed to remove';
					} else {
						message = 'Failed to remove device from passthrough';
					}

					toast.error(message, {
						position: 'bottom-center'
					});
				}

				modalState.isOpen = false;
			}
		},
		onCancel: () => {
			modalState.isOpen = false;
		}
	}}
></AlertDialog>
