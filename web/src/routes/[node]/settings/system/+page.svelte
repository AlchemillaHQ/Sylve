<script lang="ts">
	import { storage } from '$lib';
	import { getPools } from '$lib/api/zfs/pool';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { type AvailableService, type BasicSettings } from '$lib/types/system/settings';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';
	import SingleValueDialog from '$lib/components/custom/Dialog/SingleValue.svelte';
	import { sameElements } from '$lib/utils/arr';
	import { toast } from 'svelte-sonner';
	import { getBasicSettings, toggleService, updateUsablePools } from '$lib/api/system/settings';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';

	interface Data {
		pools: Zpool[];
		basicSettings: BasicSettings;
	}

	let { data }: { data: Data } = $props();

	const visible = new IsDocumentVisible();

	const pools = resource(
		() => 'zfs-pools-full',
		async (key, prevKey, { signal }) => {
			const results = await getPools(true);
			updateCache(key, results);
			return results;
		},
		{
			initialValue: data.pools
		}
	);

	const basicSettings = resource(
		() => 'system-basic-settings',
		async (key, prevKey, { signal }) => {
			const results = await getBasicSettings();
			storage.enabledServices = results.services;
			updateCache(key, results);
			return results;
		},
		{
			initialValue: data.basicSettings
		}
	);

	let poolOptions = $derived.by(() => {
		return pools.current.map((pool) => ({ label: pool.name, value: pool.name }));
	});

	function refetch() {
		pools.refetch();
		basicSettings.refetch();
	}

	useInterval(3000, {
		callback: async () => {
			if (visible.current && !storage.idle) {
				refetch();
			}
		}
	});

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				refetch();
				reload = false;
			}
		}
	);

	let query: string = $state('');
	let tableData = $derived.by(() => {
		const columns: Column[] = [
			{
				field: 'property',
				title: 'Property'
			},
			{
				field: 'value',
				title: 'Value',
				formatter: (cell: CellComponent) => {
					const row = cell.getRow();
					const property = row.getData().property;
					const value = cell.getValue();

					if (property === 'ZFS Pools' && Array.isArray(value)) {
						return value.join(', ');
					}

					return value;
				}
			}
		];

		const rows: Row[] = [
			{
				id: generateNanoId(basicSettings.current.pools.join('-')),
				property: 'ZFS Pools',
				value: basicSettings.current.pools
			},
			{
				id: generateNanoId('dhcp-server'),
				property: 'DHCP Server',
				value: basicSettings.current.services.includes('dhcp-server') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('wol-server'),
				property: 'WoL Server',
				value: basicSettings.current.services.includes('wol-server') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('samba-server'),
				property: 'Samba Server',
				value: basicSettings.current.services.includes('samba-server') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('virtualization'),
				property: 'Virtualization',
				value: basicSettings.current.services.includes('virtualization') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('jails'),
				property: 'Jails',
				value: basicSettings.current.services.includes('jails') ? 'Enabled' : 'Disabled'
			}
		];

		return { columns, rows };
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : null);

	let modals = $state({
		zfsPools: {
			open: false,
			values: basicSettings.current.pools.join(',')
		},
		dhcpServer: {
			open: false,
			enabled: basicSettings.current.services.includes('dhcp-server')
		},
		wolServer: {
			open: false,
			enabled: basicSettings.current.services.includes('wol-server')
		},
		sambaServer: {
			open: false,
			enabled: basicSettings.current.services.includes('samba-server')
		},
		virtualization: {
			open: false,
			enabled: basicSettings.current.services.includes('virtualization')
		},
		jails: {
			open: false,
			enabled: basicSettings.current.services.includes('jails')
		}
	});

	const toastOpts = {
		duration: 5000,
		position: 'bottom-center' as const
	};

	async function saveZFSPools() {
		if (modals.zfsPools.open) {
			const newPools = modals.zfsPools.values
				.split(',')
				.map((p) => p.trim())
				.filter((p) => p.length > 0);

			if (newPools.length === 0) {
				toast.error('At least one ZFS Pool must be selected', toastOpts);
				return;
			}

			if (sameElements(newPools, basicSettings.current.pools)) {
				toast.info('No changes made to ZFS Pools', toastOpts);

				return;
			} else {
				const missingPools = basicSettings.current.pools.filter((pool) => !newPools.includes(pool));
				if (missingPools.length > 0) {
					toast.error('Cannot remove initialized ZFS Pools', toastOpts);
					return;
				}
			}

			const response = await updateUsablePools(newPools);
			reload = true;
			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to update ZFS Pools', toastOpts);
			} else {
				toast.success('ZFS Pools updated successfully', toastOpts);
				modals.zfsPools.open = false;
				modals.zfsPools.values = newPools.join(',');
			}
		}
	}
</script>

{#snippet toggleButton(icon: string, label: string)}
	<span class="{icon} mr-1 h-4 w-4"></span>
	<span>{label}</span>
{/snippet}

{#snippet buttons()}
	{#if activeRows?.length === 1}
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			onclick={() => {
				if (activeRow?.property === 'ZFS Pools') {
					modals.zfsPools.open = true;
				} else if (activeRow?.property === 'DHCP Server') {
					modals.dhcpServer.open = true;
				} else if (activeRow?.property === 'WoL Server') {
					modals.wolServer.open = true;
				} else if (activeRow?.property === 'Samba Server') {
					modals.sambaServer.open = true;
				} else if (activeRow?.property === 'Virtualization') {
					modals.virtualization.open = true;
				} else if (activeRow?.property === 'Jails') {
					modals.jails.open = true;
				}
			}}
		>
			<div class="flex items-center">
				{#if activeRow?.property === 'ZFS Pools'}
					{@render toggleButton('icon-[mdi--pencil]', `Edit ${activeRow.property}`)}
				{:else}
					{@render toggleButton('icon-[ri--toggle-line]', `Toggle ${activeRow?.property}`)}
				{/if}
			</div>
		</Button>
	{/if}
{/snippet}

{#snippet serviceToggleDialog(
	serviceName: string,
	serviceKey: 'dhcpServer' | 'wolServer' | 'sambaServer' | 'virtualization' | 'jails',
	enabled: boolean
)}
	{@const needsArticle = !['Virtualization', 'Jails'].includes(serviceName)}
	{@const hasNetworkWarning = serviceName === 'DHCP Server'}
	{@const displayName = needsArticle ? `the ${serviceName}` : serviceName}
	{@const networkWarning = hasNetworkWarning ? 'this may affect network configurations, ' : ''}

	<AlertDialog
		bind:open={modals[serviceKey].open}
		names={{ parent: serviceName, element: '' }}
		customTitle={`You are about to ${enabled ? 'disable' : 'enable'} ${displayName}, ${networkWarning}you will have to restart Sylve and or the host system for changes to take effect`}
		actions={{
			onConfirm: async () => {
				const toggled = await toggleService(serviceKey as AvailableService);
				reload = true;

				if (toggled.status === 'success') {
					modals[serviceKey].enabled = !modals[serviceKey].enabled;
					toast.success(
						`${serviceName} ${modals[serviceKey].enabled ? 'enabled' : 'disabled'}`,
						toastOpts
					);
					modals[serviceKey].open = false;
				} else {
					handleAPIError(toggled);
					toast.error(`Failed to toggle ${serviceName}`, toastOpts);
				}
			},
			onCancel: () => {
				modals[serviceKey].open = false;
			}
		}}
	/>
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		{@render buttons()}
	</div>

	<TreeTable
		name="system-basic-settings-tt"
		data={tableData}
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={false}
	/>
</div>

<SingleValueDialog
	bind:open={modals.zfsPools.open}
	title="ZFS Pools"
	type="combobox"
	placeholder="Enter ZFS Pools"
	bind:value={modals.zfsPools.values}
	onSave={() => {
		saveZFSPools();
	}}
	options={poolOptions}
/>

{@render serviceToggleDialog('DHCP Server', 'dhcpServer', modals.dhcpServer.enabled)}
{@render serviceToggleDialog('WoL Server', 'wolServer', modals.wolServer.enabled)}
{@render serviceToggleDialog('Samba Server', 'sambaServer', modals.sambaServer.enabled)}
{@render serviceToggleDialog('Virtualization', 'virtualization', modals.virtualization.enabled)}
{@render serviceToggleDialog('Jails', 'jails', modals.jails.enabled)}
