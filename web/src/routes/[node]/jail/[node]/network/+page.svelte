<script lang="ts">
	import { page } from '$app/state';
	import {
		addNetwork,
		deleteNetwork,
		disinheritHostNetwork,
		getJails,
		inheritHostNetwork
	} from '$lib/api/jail/jail';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { ipGatewayFormatter, macFormtter } from '$lib/utils/jail/network';
	import {
		generateIPOptions,
		generateMACOptions,
		generateNetworkOptions
	} from '$lib/utils/network/object';
	import { createQueries } from '@tanstack/svelte-query';
	import { toast } from 'svelte-sonner';

	interface Data {
		jails: Jail[];
		jailStates: JailState[];
		jail: Jail;
		switches: SwitchList;
		networkObjects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();
	const ctId = page.url.pathname.split('/')[3];
	const results = createQueries(() => ({
		queries: [
			{
				queryKey: ['jail-list'],
				queryFn: async () => {
					return await getJails();
				},
				refetchInterval: 1000,
				keepPreviousData: true,
				initialData: data.jails,
				onSuccess: (data: Jail[]) => {
					updateCache('jail-list', data);
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
		]
	}));

	let jails = $derived(results[0].data || []);
	let jail = $derived.by(() => {
		if (jails.length > 0) {
			const found = jails.find((j) => j.ctId === parseInt(ctId));
			return found || data.jail;
		}

		return data.jail;
	});

	let switches = $derived(results[1].data as SwitchList);
	let networkObjects = $derived(results[2].data || []);
	let inherited = $derived.by(() => {
		if (jail) {
			return jail.inheritIPv4 || jail.inheritIPv6;
		}

		return false;
	});

	let usable = $derived.by(() => {
		return [...(switches.standard || []), ...(switches.manual || [])].filter((s) => {
			return !jail?.networks.some((n) => n.switchId === s.id);
		});
	});

	let options = {
		inherit: {
			open: false,
			ipv4: false,
			ipv6: false
		},
		add: {
			open: false,
			sw: {
				open: false,
				value: ''
			},
			ipv4: {
				open: false,
				value: '',
				options: generateNetworkOptions(data.networkObjects, 'ipv4')
			},
			ipv6: {
				open: false,
				value: '',
				options: generateNetworkOptions(data.networkObjects, 'ipv6')
			},
			ipv4Gw: {
				open: false,
				value: '',
				options: generateIPOptions(data.networkObjects, 'ipv4')
			},
			ipv6Gw: {
				open: false,
				value: '',
				options: generateIPOptions(data.networkObjects, 'ipv6')
			},
			mac: {
				open: false,
				value: '',
				options: generateMACOptions(data.networkObjects)
			},
			dhcp: false,
			slaac: false
		},
		delete: {
			open: false,
			title: ''
		}
	};

	let modals = $state(options);
	let query = $state('');
	let activeRows: Row[] = $state([] as Row[]);
	let activeRow: Row | null = $derived(
		activeRows.length > 0 ? (activeRows[0] as Row) : ({} as Row)
	);

	let table = $derived.by(() => {
		const columns: Column[] = [
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
				for (const network of jail.networks) {
					let ipv4 = '';
					let ipv6 = '';

					if (network.dhcp) {
						ipv4 = 'DHCP';
					} else {
						if (network.ipv4Id && network.ipv4GwId) {
							ipv4 = ipGatewayFormatter(networkObjects, network.ipv4Id, network.ipv4GwId);
						} else {
							ipv4 = '-';
						}
					}

					if (network.slaac) {
						ipv6 = 'SLAAC';
					} else {
						if (network.ipv6Id && network.ipv6GwId) {
							ipv6 = ipGatewayFormatter(networkObjects, network.ipv6Id, network.ipv6GwId);
						} else {
							ipv6 = '-';
						}
					}

					let name = '';
					if (network.switchType === 'standard') {
						name = switches.standard?.find((sw) => sw.id === network.switchId)?.name || '';
					} else {
						name = switches.manual?.find((sw) => sw.id === network.switchId)?.name || '';
					}

					rows.push({
						id: network.id,
						switch: name,
						mac: macFormtter(networkObjects, network.macId || 0),
						ipv4,
						ipv6
					});
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

	async function inherit() {
		if (!jail) return;
		if (!modals.inherit.ipv4 && !modals.inherit.ipv6) {
			toast.error('You must select at least one protocol to inherit', {
				position: 'bottom-center'
			});

			return;
		}

		const response = await inheritHostNetwork(jail.ctId, modals.inherit.ipv4, modals.inherit.ipv6);
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to inherit network', {
				position: 'bottom-center'
			});
		} else {
			toast.success('Host network inherited', {
				position: 'bottom-center'
			});
		}

		modals.inherit.open = false;
	}

	async function disinherit() {
		if (!jail) return;
		const response = await disinheritHostNetwork(jail.ctId);
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to disinherit network', {
				position: 'bottom-center'
			});
		} else {
			toast.success('Host network disinherited', {
				position: 'bottom-center'
			});
		}

		modals.inherit.open = false;
	}

	async function addSwitch() {
		if (!jail) return;
		let error = '';

		if (!modals.add.sw.value) {
			error = 'Switch is required';
		} else if (
			!modals.add.ipv4.value &&
			!modals.add.ipv6.value &&
			!modals.add.dhcp &&
			!modals.add.slaac
		) {
			error = 'At least one network configuration is required';
		} else if (modals.add.ipv4.value && !modals.add.ipv4Gw.value && !modals.add.dhcp) {
			error = 'IPv4 Gateway is required when IPv4 network is selected';
		} else if (modals.add.ipv6.value && !modals.add.ipv6Gw.value && !modals.add.slaac) {
			error = 'IPv6 Gateway is required when IPv6 network is selected';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		const response = await addNetwork(
			jail.ctId,
			modals.add.sw.value,
			parseInt(modals.add.mac.value),
			parseInt(modals.add.ipv4.value || '0'),
			parseInt(modals.add.ipv4Gw.value || '0'),
			parseInt(modals.add.ipv6.value || '0'),
			parseInt(modals.add.ipv6Gw.value || '0'),
			modals.add.dhcp,
			modals.add.slaac
		);

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to add network', {
				position: 'bottom-center'
			});

			return;
		} else {
			toast.success('Network added', {
				position: 'bottom-center'
			});
		}

		modals.add.open = false;
	}

	async function handleSwitchDelete() {
		if (!jail) return;

		const response = await deleteNetwork(jail.ctId, Number(activeRow?.id ?? 0));
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

	$effect(() => {
		if (modals.add.dhcp) {
			modals.add.ipv4.value = '';
			modals.add.ipv4Gw.value = '';
		}

		if (modals.add.slaac) {
			modals.add.ipv6.value = '';
			modals.add.ipv6Gw.value = '';
		}
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		{#if !inherited}
			<Button
				onclick={() => {
					if (usable.length === 0) {
						toast.error('No available switches to add', {
							position: 'bottom-center'
						});
						return;
					}

					modals.add.open = true;
				}}
				size="sm"
				class="h-6"
			>
				<div class="flex items-center">
					<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

					<span>New</span>
				</div>
			</Button>
		{/if}

		{#if activeRows.length <= 0}
			<Button
				onclick={() => {
					modals.inherit.open = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					{#if inherited}
						<span class="icon-[mdi--close-network] mr-1 h-4 w-4"></span>
						<span>Disinherit Network</span>
					{:else}
						<span class="icon-[mdi--plus-network] mr-1 h-4 w-4"></span>
						<span>Inherit Network</span>
					{/if}
				</div>
			</Button>
		{:else}
			<Button
				onclick={() => {
					modals.delete.title = activeRow?.switch ?? '';
					modals.delete.open = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--minus-network] mr-1 h-4 w-4"></span>
					<span>Detach</span>
				</div>
			</Button>
		{/if}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name={'jail-networks-tt'}
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

<!-- Inherit/Disinherit -->
<Dialog.Root bind:open={modals.inherit.open}>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[mdi--network] mr-2 h-5 w-5"></span>

					{#if inherited}
						Disinherit Network
					{:else}
						Inherit Network
					{/if}
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							modals.inherit.open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if inherited}
			<span class="text-muted-foreground text-justify text-sm">
				This option will <b>disinherit the network configuration</b> from the host. Choose this if
				you want to <b>attach a custom network switch</b> to this jail or <b>disable networking</b>
				entirely.
				<b>Changes will take effect after restarting the jail.</b>
			</span>
		{:else}
			<span class="text-muted-foreground text-justify text-sm">
				This option will inherit the <b>network configuration</b> from the host. Choose this if you
				want the jail to <b>share the host's networking</b>. You can select which <b>protocols</b>
				to inherit below.
				<b>Changes will take effect after restarting the jail.</b>
			</span>

			<div>
				<div class="flex flex-row gap-2">
					<CustomCheckbox
						label="IPv4"
						bind:checked={modals.inherit.ipv4}
						classes="flex items-center gap-2"
					></CustomCheckbox>
					<CustomCheckbox
						label="IPv6"
						bind:checked={modals.inherit.ipv6}
						classes="flex items-center gap-2"
					></CustomCheckbox>
				</div>
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if !inherited}
					<Button onclick={inherit} type="submit" size="sm">Save</Button>
				{:else}
					<Button onclick={disinherit} type="submit" size="sm">Disinherit</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={modals.add.open}>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[mdi--network] mr-2 h-5 w-5"></span>

					<span>New Network</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => {
							modals = options;
							modals.add.open = true;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>

					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							modals.add.open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<CustomComboBox
				bind:open={modals.add.sw.open}
				label="Switch"
				bind:value={modals.add.sw.value}
				data={usable.map((s) => ({
					label: s.name,
					value: s.name
				}))}
				classes="flex-1 space-y-1"
				placeholder="Select Switch"
				width="w-full"
			></CustomComboBox>

			<CustomComboBox
				bind:open={modals.add.ipv4.open}
				label="IPv4 Network"
				bind:value={modals.add.ipv4.value}
				data={generateNetworkOptions(networkObjects, 'ipv4')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv4"
				width="w-full"
				disabled={modals.add.ipv4.options.length === 0 || modals.add.dhcp}
			></CustomComboBox>

			<CustomComboBox
				bind:open={modals.add.ipv4Gw.open}
				label="IPv4 Gateway"
				bind:value={modals.add.ipv4Gw.value}
				data={generateIPOptions(networkObjects, 'ipv4')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv4 Gateway"
				width="w-full"
				disabled={modals.add.ipv4Gw.options.length === 0 || modals.add.dhcp}
			></CustomComboBox>

			<CustomComboBox
				bind:open={modals.add.ipv6.open}
				label="IPv6 Network"
				bind:value={modals.add.ipv6.value}
				data={generateNetworkOptions(networkObjects, 'ipv6')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv6"
				width="w-full"
				disabled={modals.add.ipv6.options.length === 0 || modals.add.slaac}
			></CustomComboBox>

			<CustomComboBox
				bind:open={modals.add.ipv6Gw.open}
				label="IPv6 Gateway"
				bind:value={modals.add.ipv6Gw.value}
				data={generateIPOptions(networkObjects, 'ipv6')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv6 Gateway"
				width="w-full"
				disabled={modals.add.ipv6Gw.options.length === 0 || modals.add.slaac}
			></CustomComboBox>

			<CustomComboBox
				bind:open={modals.add.mac.open}
				label="MAC Address"
				bind:value={modals.add.mac.value}
				data={modals.add.mac.options}
				classes="flex-1 space-y-1"
				placeholder="Select MAC Address"
				width="w-full"
			/>
		</div>

		<div class="mt-2">
			<div class="flex flex-row gap-2">
				<CustomCheckbox
					label="DHCP"
					bind:checked={modals.add.dhcp}
					classes="flex items-center gap-2"
				></CustomCheckbox>
				<CustomCheckbox
					label="SLAAC"
					bind:checked={modals.add.slaac}
					classes="flex items-center gap-2"
				></CustomCheckbox>
			</div>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={addSwitch} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={modals.delete.open}
	customTitle={`This will detach the jail from the switch <b>${modals.delete.title}</b>`}
	actions={{
		onConfirm: async () => {
			handleSwitchDelete();
		},
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
></AlertDialog>
