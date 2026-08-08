<script lang="ts">
	import {
		addPPTDevice,
		getPCIDevices,
		getPPTDevices,
		importPPTDevice,
		preparePPTDevice,
		removePPTDevice
	} from '$lib/api/system/pci';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { APIResponse } from '$lib/types/common';
	import { type PCIDevice, type PPTDevice } from '$lib/types/system/pci';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
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
		async (key, _previousKey, { data: previousDevices }): Promise<PPTDevice[]> => {
			const result = await getPPTDevices();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return previousDevices ?? [];
			}
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.pptDevices
		}
	);

	// svelte-ignore state_referenced_locally
	let pciDevices = resource(
		() => 'pci-devices',
		async (key, _previousKey, { data: previousDevices }): Promise<PCIDevice[]> => {
			const result = await getPCIDevices();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return previousDevices ?? [];
			}
			updateCache(key, result);
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

	function prepareDevice(domain: string, deviceId: string) {
		const device = activeRow ? activeRow[0].device : '';
		const vendor = activeRow ? activeRow[0].vendor : '';

		modalState.isOpen = true;
		modalState.title = `Prepare passthrough for <b>${device}</b> by <b>${vendor}</b>? This updates /boot/loader.conf and applies after reboot.`;
		modalState.action = 'prepare';
		modalState.add.domain = domain;
		modalState.add.deviceId = deviceId;
	}

	function importDevice(domain: string, deviceId: string) {
		const device = activeRow ? activeRow[0].device : '';
		const vendor = activeRow ? activeRow[0].vendor : '';

		modalState.isOpen = true;
		modalState.title = `Import <b>${device}</b> by <b>${vendor}</b> into Sylve passthrough management? This keeps current ppt state and adds it to the database.`;
		modalState.action = 'import';
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

	function responseHasError(response: APIResponse, code: string): boolean {
		return (
			response.error === code || (Array.isArray(response.error) && response.error.includes(code))
		);
	}

	function removalNeedsReboot(response: APIResponse): boolean {
		return (
			typeof response.data === 'object' &&
			response.data !== null &&
			'rebootRequired' in response.data &&
			response.data.rebootRequired === true
		);
	}

	async function confirmPassthroughAction() {
		let result: APIResponse;
		let successMessage = '';
		let errorMessage = '';
		let shouldReload = false;

		switch (modalState.action) {
			case 'add':
				result = await addPPTDevice(modalState.add.domain, modalState.add.deviceId);
				successMessage = 'Device added to passthrough';
				errorMessage = responseHasError(result, 'passthrough_device_requires_import')
					? 'Device is already attached to ppt; import it instead'
					: responseHasError(result, 'passthrough_device_already_managed')
						? 'Device is already managed for passthrough'
						: 'Failed to add device to passthrough';
				shouldReload = true;
				break;
			case 'prepare':
				result = await preparePPTDevice(modalState.add.domain, modalState.add.deviceId);
				successMessage = 'Device prepared for passthrough. Reboot required.';
				errorMessage = 'Failed to prepare device for passthrough';
				break;
			case 'import':
				result = await importPPTDevice(modalState.add.domain, modalState.add.deviceId);
				successMessage =
					result.message === 'device_already_managed'
						? 'Device is already managed for passthrough'
						: 'Device imported into passthrough management';
				errorMessage = responseHasError(result, 'passthrough_device_not_attached')
					? 'Device is not currently attached to ppt'
					: 'Failed to import device into passthrough management';
				shouldReload = true;
				break;
			case 'remove':
				result = await removePPTDevice(modalState.remove.id);
				successMessage = removalNeedsReboot(result)
					? 'Passthrough removed. Reboot required to restore the host driver.'
					: 'Device removed from passthrough';
				errorMessage = responseHasError(result, 'passthrough_device_in_use')
					? 'Device is assigned to a VM and cannot be removed'
					: 'Failed to remove device from passthrough';
				shouldReload = true;
				break;
			default:
				return;
		}

		if (result.status !== 'success') {
			toast.error(errorMessage, { position: 'bottom-center' });
			return;
		}

		if (shouldReload) reload = true;
		toast.success(successMessage, { position: 'bottom-center' });
		modalState.isOpen = false;
	}
</script>

{#snippet button(type: string)}
	{#if activeRow !== null && activeRow.length === 1}
		{#if type === 'enable-passthrough' && activeRow[0].domain === 0 && !activeRow[0].name.startsWith('ppt') && !activeRow[0].pptId}
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

		{#if type === 'prepare-passthrough' && activeRow[0].domain === 0 && !activeRow[0].name.startsWith('ppt')}
			<Button
				onclick={() =>
					activeRow && prepareDevice(activeRow[0].domain.toString(), activeRow[0].deviceId)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--clock] mr-1 h-4 w-4"></span>
					<span>Prepare Passthrough</span>
				</div>
			</Button>
		{/if}

		{#if type === 'import-passthrough' && activeRow[0].domain === 0 && activeRow[0].name.startsWith('ppt') && !activeRow[0].pptId}
			<Button
				onclick={() =>
					activeRow && importDevice(activeRow[0].domain.toString(), activeRow[0].deviceId)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[wpf--connected] mr-1 h-4 w-4"></span>

					<span>Import Passthrough</span>
				</div>
			</Button>
		{/if}

		{#if type === 'disable-passthrough' && activeRow[0].pptId}
			<Button
				onclick={() => activeRow && removeDevice(Number(activeRow[0].pptId))}
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
		{@render button('prepare-passthrough')}
		{@render button('import-passthrough')}
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
	keepOpenOnConfirm={true}
	loadingLabel="Applying..."
	actions={{
		onConfirm: confirmPassthroughAction,
		onCancel: () => {
			modalState.isOpen = false;
		}
	}}
></AlertDialog>
