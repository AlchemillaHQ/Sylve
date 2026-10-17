<script lang="ts">
	import { storage } from '$lib';
	import { getPools } from '$lib/api/zfs/pool';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { type AvailableService, type BasicSettings } from '$lib/types/system/settings';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { IsDocumentVisible, resource, useInterval } from 'runed';
	import type { CellComponent } from 'tabulator-tables';
	import SingleValueDialog from '$lib/components/custom/Dialog/SingleValue.svelte';
	import { sameElements } from '$lib/utils/arr';
	import { toast } from 'svelte-sonner';
	import { getBasicSettings, setServiceEnabled, updateUsablePools } from '$lib/api/system/settings';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import { setEnabledServicesForHostname } from '$lib/utils/enabled-services';
	import { onDestroy, onMount } from 'svelte';

	interface Data {
		pools: Zpool[];
		basicSettings: BasicSettings;
	}

	let { data }: { data: Data } = $props();

	const visible = new IsDocumentVisible();

	// svelte-ignore state_referenced_locally
	const pools = resource(
		() => 'zfs-pools-full',
		async (key, _previousKey, { signal }): Promise<Zpool[]> => {
			const results = await getPools(true);
			if (signal.aborted) {
				throw new DOMException('Obsolete ZFS pools request', 'AbortError');
			}

			updateCache(key, results);
			return results;
		},
		{
			initialValue: data.pools,
			lazy: true
		}
	);

	// svelte-ignore state_referenced_locally
	let basicSettings = $state(data.basicSettings);
	let settingsGeneration = 0;

	async function refetchBasicSettings() {
		const generation = ++settingsGeneration;
		const results = await getBasicSettings();
		if (generation !== settingsGeneration) return;

		if (isAPIResponse(results)) {
			handleAPIError(results);
			return;
		}

		basicSettings = results;
		setEnabledServicesForHostname(storage.hostname, results.services);
		updateCache('system-basic-settings', results);
	}

	onMount(() => {
		void refetch();
	});

	onDestroy(() => {
		settingsGeneration++;
	});

	let poolOptions = $derived.by(() => {
		return pools.current.map((pool) => ({ label: pool.name, value: pool.name }));
	});

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
				id: generateNanoId('zfs-pools'),
				property: 'ZFS Pools',
				value: basicSettings.pools
			},
			{
				id: generateNanoId('dhcp-server'),
				property: 'DHCP Server',
				value: basicSettings.services.includes('dhcp-server') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('wol-server'),
				property: 'WoL Server',
				value: basicSettings.services.includes('wol-server') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('samba-server'),
				property: 'Samba Server',
				value: basicSettings.services.includes('samba-server') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('virtualization'),
				property: 'Virtualization',
				value: basicSettings.services.includes('virtualization') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('jails'),
				property: 'Jails',
				value: basicSettings.services.includes('jails') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('firewall'),
				property: 'Firewall',
				value: basicSettings.services.includes('firewall') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('wireguard'),
				property: 'WireGuard',
				value: basicSettings.services.includes('wireguard') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('iscsi'),
				property: 'iSCSI',
				value: basicSettings.services.includes('iscsi') ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId('mdns'),
				property: 'mDNS Discovery',
				value: basicSettings.services.includes('mdns') ? 'Enabled' : 'Disabled'
			}
		];

		return { columns, rows };
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : null);

	let modals = $state({
		zfsPools: {
			open: false,
			values: ''
		},
		'dhcp-server': {
			open: false
		},
		'wol-server': {
			open: false
		},
		'samba-server': {
			open: false
		},
		virtualization: {
			open: false
		},
		jails: {
			open: false
		},
		firewall: {
			open: false
		},
		wireguard: {
			open: false
		},
		iscsi: {
			open: false
		},
		mdns: {
			open: false
		}
	});

	const toastOpts = {
		duration: 5000,
		position: 'bottom-center' as const
	};

	let refreshing = false;

	async function refetch() {
		if (refreshing) return;

		refreshing = true;
		try {
			await Promise.all([pools.refetch(), refetchBasicSettings()]);
		} finally {
			refreshing = false;
		}
	}

	const anyModalOpen = $derived(
		modals.zfsPools.open ||
			modals['dhcp-server'].open ||
			modals['wol-server'].open ||
			modals['samba-server'].open ||
			modals.virtualization.open ||
			modals.jails.open ||
			modals.firewall.open ||
			modals.wireguard.open ||
			modals.iscsi.open ||
			modals.mdns.open
	);

	useInterval(3000, {
		callback: async () => {
			if (visible.current && !storage.idle && !anyModalOpen && !loading) {
				await refetch();
			}
		}
	});

	let loading = $state(false);

	async function saveZFSPools() {
		if (modals.zfsPools.open) {
			const newPools = Array.from(
				new Set(
					modals.zfsPools.values
						.split(',')
						.map((p) => p.trim())
						.filter((p) => p.length > 0)
				)
			);

			if (newPools.length === 0) {
				toast.error('At least one ZFS Pool must be selected', toastOpts);
				return;
			}

			if (sameElements(newPools, basicSettings.pools)) {
				toast.info('No changes made to ZFS Pools', toastOpts);
				return;
			}

			loading = true;
			try {
				const response = await updateUsablePools(newPools);
				if (response.status !== 'success') {
					handleAPIError(response);
					toast.error('Failed to update ZFS Pools', toastOpts);
					return;
				}

				const updatedSettings = { ...basicSettings, pools: newPools };
				settingsGeneration++;
				basicSettings = updatedSettings;
				await updateCache('system-basic-settings', updatedSettings);
				await refetchBasicSettings();
				toast.success('ZFS Pools updated', toastOpts);
				modals.zfsPools.open = false;
			} finally {
				loading = false;
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
					modals.zfsPools.values = basicSettings.pools
						.filter((p) => pools.current.some((live) => live.name === p))
						.join(',');
					modals.zfsPools.open = true;
				} else if (activeRow?.property === 'DHCP Server') {
					modals['dhcp-server'].open = true;
				} else if (activeRow?.property === 'WoL Server') {
					modals['wol-server'].open = true;
				} else if (activeRow?.property === 'Samba Server') {
					modals['samba-server'].open = true;
				} else if (activeRow?.property === 'Virtualization') {
					modals['virtualization'].open = true;
				} else if (activeRow?.property === 'Jails') {
					modals['jails'].open = true;
				} else if (activeRow?.property === 'Firewall') {
					modals['firewall'].open = true;
				} else if (activeRow?.property === 'WireGuard') {
					modals['wireguard'].open = true;
				} else if (activeRow?.property === 'iSCSI') {
					modals['iscsi'].open = true;
				} else if (activeRow?.property === 'mDNS Discovery') {
					modals.mdns.open = true;
				}
			}}
		>
			<div class="flex items-center">
				{#if activeRow?.property === 'ZFS Pools'}
					{@render toggleButton('icon-[mdi--pencil]', `Edit ${activeRow.property}`)}
				{:else}
					{@render toggleButton(
						'icon-[ri--toggle-line]',
						`${activeRow?.value === 'Enabled' ? 'Disable' : 'Enable'} ${activeRow?.property}`
					)}
				{/if}
			</div>
		</Button>
	{/if}
{/snippet}

{#snippet serviceToggleDialog(
	serviceName: string,
	serviceKey:
		| 'dhcp-server'
		| 'wol-server'
		| 'samba-server'
		| 'virtualization'
		| 'jails'
		| 'firewall'
		| 'wireguard'
		| 'iscsi'
		| 'mdns'
)}
	{@const enabled = basicSettings.services.includes(serviceKey)}
	{@const needsArticle = !['Virtualization', 'Jails'].includes(serviceName)}
	{@const hasNetworkWarning = serviceName === 'DHCP Server' || serviceName === 'Firewall'}
	{@const displayName = needsArticle ? `the ${serviceName}` : serviceName}
	{@const networkWarning = hasNetworkWarning ? ' This may affect network configuration.' : ''}
	{@const mdnsTitle = enabled
		? 'Disabling mDNS Discovery immediately stops Bonjour discovery, including discovery of any Samba Apple and Time Machine shares.'
		: 'Enabling mDNS Discovery immediately publishes existing managed and custom records.'}

	<AlertDialog
		bind:open={modals[serviceKey].open}
		names={{ parent: serviceName, element: '' }}
		customTitle={serviceKey === 'mdns'
			? mdnsTitle
			: serviceKey === 'iscsi'
				? `You are about to ${enabled ? 'disable' : 'enable'} iSCSI targets.${enabled ? ' Initiator connections will stay active.' : ''}`
				: `You are about to ${enabled ? 'disable' : 'enable'} ${displayName}.${networkWarning} You will have to restart Sylve and/or the host system for changes to take effect.`}
		confirmLabel={serviceKey === 'mdns' && enabled ? 'Disable anyway' : 'Continue'}
		loadingLabel={enabled ? 'Disabling...' : 'Enabling...'}
		keepOpenOnConfirm
		actions={{
			onConfirm: async () => {
				const desiredEnabled = !enabled;
				const response = await setServiceEnabled(serviceKey as AvailableService, desiredEnabled);

				if (response.status === 'success') {
					const services = desiredEnabled
						? [...new Set([...basicSettings.services, serviceKey])]
						: basicSettings.services.filter((service) => service !== serviceKey);
					const updatedSettings = { ...basicSettings, services };
					settingsGeneration++;
					basicSettings = updatedSettings;
					setEnabledServicesForHostname(storage.hostname, services);
					await updateCache('system-basic-settings', updatedSettings);
					await refetchBasicSettings();
					if (response.message === 'iscsi_configuration_saved_apply_pending') {
						toast.warning('iSCSI state saved; runtime checks are pending', toastOpts);
					} else {
						toast.success(`${serviceName} ${desiredEnabled ? 'enabled' : 'disabled'}`, toastOpts);
					}
					modals[serviceKey].open = false;
				} else {
					handleAPIError(response);
					toast.error(`Failed to update ${serviceName}`, toastOpts);
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
	bind:loading
/>

{@render serviceToggleDialog('DHCP Server', 'dhcp-server')}
{@render serviceToggleDialog('WoL Server', 'wol-server')}
{@render serviceToggleDialog('Samba Server', 'samba-server')}
{@render serviceToggleDialog('Virtualization', 'virtualization')}
{@render serviceToggleDialog('Jails', 'jails')}
{@render serviceToggleDialog('Firewall', 'firewall')}
{@render serviceToggleDialog('WireGuard', 'wireguard')}
{@render serviceToggleDialog('iSCSI', 'iscsi')}
{@render serviceToggleDialog('mDNS Discovery', 'mdns')}
