<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { createSwitch, deleteSwitch, getSwitches, updateSwitch } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Iface } from '$lib/types/network/iface';
	import type { NetworkObject } from '$lib/types/network/object';
	import { emptySwitchList, isSwitchList, type SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { generateIPOptions, generateNetworkOptions } from '$lib/utils/network/object';
	import { generateTableData } from '$lib/utils/network/switch/standard';
	import { isValidMTU, isValidVLAN } from '$lib/utils/numbers';
	import { isValidIPv4, isValidIPv6, isValidSwitchName } from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		interfaces: Iface[] | APIResponse;
		switches: SwitchList | APIResponse;
		objects: NetworkObject[] | APIResponse;
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	let lastGoodInterfaces = Array.isArray(data.interfaces) ? data.interfaces : ([] as Iface[]);
	// svelte-ignore state_referenced_locally
	let lastGoodNetworkObjects = Array.isArray(data.objects) ? data.objects : ([] as NetworkObject[]);
	// svelte-ignore state_referenced_locally
	let lastGoodSwitches = isSwitchList(data.switches) ? data.switches : emptySwitchList();

	const networkInterfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const res = await getInterfaces();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodInterfaces;
			}
			lastGoodInterfaces = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodInterfaces }
	);

	const switches = resource(
		() => 'network-switches',
		async (key) => {
			const res = await getSwitches();
			if (!isSwitchList(res)) {
				handleAPIError(res);
				return lastGoodSwitches;
			}
			lastGoodSwitches = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodSwitches }
	);

	const networkObjects = resource(
		() => 'network-objects',
		async (key) => {
			const res = await getNetworkObjects();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodNetworkObjects;
			}
			lastGoodNetworkObjects = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodNetworkObjects }
	);

	let query: string = $state('');
	let useablePorts = $derived.by(() => {
		let available: string[] = [];

		if (networkInterfaces.current) {
			for (const iface of networkInterfaces.current) {
				available.push(iface.name);
			}
		}

		return available.filter((item, index) => available.indexOf(item) === index);
	});

	let confirmModals = $state({
		active: '' as 'newSwitch' | 'editSwitch' | 'deleteSwitch',
		newSwitch: {
			open: false,
			name: '',
			mtu: '',
			vlan: '',
			network4: '0',
			gwAddress4: '0',
			network6: '0',
			gwAddress6: '0',
			disableIPv6: false,
			private: false,
			ports: [] as string[],
			dhcp: false,
			slaac: false,
			defaultRoute: false,
			disableBridgeOffloads: true
		},
		editSwitch: {
			oldName: '',
			open: false,
			name: '',
			mtu: '',
			vlan: '',
			address: '0',
			address6: '0',
			network4: '0',
			gwAddress4: '0',
			network6: '0',
			gwAddress6: '0',
			disableIPv6: false,
			private: false,
			ports: [] as string[],
			dhcp: false,
			slaac: false,
			defaultRoute: false,
			disableBridgeOffloads: false
		},
		deleteSwitch: {
			open: false,
			name: '',
			id: 0
		}
	});

	let comboBoxes = $state({
		ipv4: {
			open: false,
			value: ''
		},
		ipv4Gw: {
			open: false,
			value: ''
		},
		ipv6: {
			open: false,
			value: ''
		},
		ipv6Gw: {
			open: false,
			value: ''
		},
		ports: {
			open: false,
			value: []
		}
	});

	const singleEntryNetworkObjects = $derived(
		networkObjects.current.filter((object) => object.entries?.length === 1)
	);
	const ipv4NetworkOptions = $derived(generateNetworkOptions(singleEntryNetworkObjects, 'IPv4'));
	const ipv4GatewayOptions = $derived(generateIPOptions(networkObjects.current, 'IPv4'));
	const ipv6NetworkOptions = $derived(generateNetworkOptions(singleEntryNetworkObjects, 'IPv6'));
	const ipv6GatewayOptions = $derived(generateIPOptions(networkObjects.current, 'IPv6'));

	function splitObjectOrManual(
		value: string | string[],
		options: { label: string; value: string }[]
	): { id: number; manual: string } {
		const v = (Array.isArray(value) ? '' : (value ?? '')).trim();
		if (!v) {
			return { id: 0, manual: '' };
		}
		if (options.some((o) => o.value === v)) {
			return { id: Number(v), manual: '' };
		}
		return { id: 0, manual: v };
	}

	let reload = $state(false);
	let saving = $state(false);

	watch(
		() => reload,
		(current) => {
			if (current) {
				networkInterfaces.refetch();
				switches.refetch();
				networkObjects.refetch();
				reload = false;
			}
		}
	);

	async function confirmAction() {
		if (saving) return;

		if (confirmModals.active === 'newSwitch' || confirmModals.active === 'editSwitch') {
			const activeModal = confirmModals[confirmModals.active];
			const normalizedName = activeModal.name.trim();
			if (!isValidSwitchName(normalizedName)) {
				toast.error('Invalid switch name', {
					position: 'bottom-center'
				});

				return;
			}

			const mtuInput = String(activeModal.mtu ?? '').trim();
			const mtu = mtuInput === '' ? 1500 : Number.parseInt(mtuInput, 10);
			if (!Number.isFinite(mtu) || !isValidMTU(mtu)) {
				toast.error('Invalid MTU', {
					position: 'bottom-center'
				});

				return;
			}

			const vlanInput = String(activeModal.vlan ?? '').trim();
			const vlan = vlanInput === '' ? 0 : Number.parseInt(vlanInput, 10);
			if (!Number.isFinite(vlan) || !isValidVLAN(vlan)) {
				toast.error('Invalid VLAN', {
					position: 'bottom-center'
				});

				return;
			}

			if (
				(confirmModals.active === 'newSwitch' || confirmModals.active === 'editSwitch') &&
				!activeModal.dhcp &&
				activeModal.defaultRoute
			) {
				const existingSwitch = switches.current?.standard?.find(
					(sw) =>
						sw.defaultRoute && !(confirmModals.active === 'editSwitch' && sw.id === activeRow?.id)
				);

				if (existingSwitch) {
					toast.error('There is already a switch with a default route', {
						position: 'bottom-center'
					});
					return;
				}
			}

			const net4 = splitObjectOrManual(comboBoxes.ipv4.value, ipv4NetworkOptions);
			const gw4 = splitObjectOrManual(comboBoxes.ipv4Gw.value, ipv4GatewayOptions);
			const net6 = splitObjectOrManual(comboBoxes.ipv6.value, ipv6NetworkOptions);
			const gw6 = splitObjectOrManual(comboBoxes.ipv6Gw.value, ipv6GatewayOptions);

			const manual = {
				network4: net4.manual,
				gateway4: gw4.manual,
				network6: net6.manual,
				gateway6: gw6.manual
			};

			if (manual.network4 && !isValidIPv4(manual.network4, true)) {
				toast.error('Invalid IPv4 network — expected CIDR, e.g. 192.168.1.1/24', {
					position: 'bottom-center'
				});
				return;
			}
			if (manual.gateway4 && !isValidIPv4(manual.gateway4)) {
				toast.error('Invalid IPv4 gateway address', { position: 'bottom-center' });
				return;
			}
			if (manual.network6 && !isValidIPv6(manual.network6, true)) {
				toast.error('Invalid IPv6 network — expected CIDR, e.g. 2001:db8::1/64', {
					position: 'bottom-center'
				});
				return;
			}
			if (manual.gateway6 && !isValidIPv6(manual.gateway6)) {
				toast.error('Invalid IPv6 gateway address', { position: 'bottom-center' });
				return;
			}

			saving = true;
			try {
				if (confirmModals.active === 'newSwitch') {
					const created = await createSwitch(
						normalizedName,
						mtu,
						vlan,
						net4.id,
						gw4.id,
						net6.id,
						gw6.id,
						activeModal.private,
						comboBoxes.ports.value,
						activeModal.disableIPv6,
						activeModal.slaac,
						activeModal.dhcp,
						activeModal.defaultRoute,
						activeModal.disableBridgeOffloads,
						manual
					);

					if (isAPIResponse(created)) {
						handleAPIError(created);
						toast.error('Error creating switch', { position: 'bottom-center' });
						return;
					}

					toast.success(`Switch ${normalizedName} created`, {
						position: 'bottom-center'
					});
				} else {
					const edited = await updateSwitch(
						activeRow?.id as number,
						mtu,
						vlan,
						net4.id,
						gw4.id,
						net6.id,
						gw6.id,
						activeModal.private,
						comboBoxes.ports.value,
						activeModal.disableIPv6,
						activeModal.slaac,
						activeModal.dhcp,
						activeModal.defaultRoute,
						activeModal.disableBridgeOffloads,
						manual
					);

					if (edited.status !== 'success') {
						handleAPIError(edited);
						toast.error('Error updating switch', { position: 'bottom-center' });
						return;
					}

					toast.success(`Switch ${confirmModals.editSwitch.name} updated`, {
						position: 'bottom-center'
					});
				}

				reload = true;
				resetModal(true);
			} finally {
				saving = false;
			}
		}
	}

	let tableData = $derived(generateTableData(switches.current));
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	function handleDelete() {
		if (activeRow && Object.keys(activeRow).length > 0) {
			confirmModals.active = 'deleteSwitch';
			confirmModals.deleteSwitch.open = true;
			confirmModals.deleteSwitch.name = activeRow.name;
			confirmModals.deleteSwitch.id = activeRow.id as number;
		}
	}

	function deleteErrorMessage(error: APIResponse['error']): string {
		if (typeof error !== 'string') return 'Error deleting switch';
		switch (error) {
			case 'standard_switch_in_use_by_vm':
				return 'Switch is in use by a VM';
			case 'standard_switch_in_use_by_jail':
				return 'Switch is in use by a jail';
			case 'standard_switch_in_use_by_dhcp_config':
				return 'Switch is enabled in the DHCP configuration';
			case 'standard_switch_in_use_by_dhcp_range':
				return 'Switch is in use by a DHCP range';
			case 'standard_switch_in_use_by_static_route':
				return 'Switch is referenced by a static route';
			case 'standard_switch_in_use_by_firewall':
				return 'Switch is referenced by a firewall rule';
			case 'standard_switch_in_use_by_dynamic_dns':
				return 'Switch is used by Dynamic DNS';
			case 'standard_switch_in_use_by_mdns':
				return 'Switch is used by mDNS';
			case 'standard_switch_in_use_by_samba':
				return 'Switch is used by Samba';
			case 'standard_switch_in_use_by_wireguard':
				return 'Switch is used by WireGuard';
			case 'standard_switch_runtime_member_conflict':
				return 'Switch still contains an unmanaged interface';
			case 'standard_switch_not_found':
				return 'Switch no longer exists';
			default:
				return 'Error deleting switch';
		}
	}

	function handleEdit() {
		if (activeRow && Object.keys(activeRow).length > 0) {
			confirmModals.active = 'editSwitch';
			confirmModals.editSwitch.open = true;
			confirmModals.editSwitch.oldName = activeRow.name;
			confirmModals.editSwitch.name = activeRow.name;
			confirmModals.editSwitch.mtu = String(activeRow.mtu ?? '');
			confirmModals.editSwitch.vlan = activeRow.vlan === '-' ? '' : String(activeRow.vlan ?? '');

			comboBoxes.ipv4.value = '';
			comboBoxes.ipv4Gw.value = '';
			comboBoxes.ipv6.value = '';
			comboBoxes.ipv6Gw.value = '';

			if (activeRow.networkObj && activeRow.networkObj.id) {
				comboBoxes.ipv4.value = activeRow.networkObj.id.toString();
			} else if (activeRow.networkManual) {
				comboBoxes.ipv4.value = activeRow.networkManual as string;
			}

			if (activeRow.network6Obj && activeRow.network6Obj.id) {
				comboBoxes.ipv6.value = activeRow.network6Obj.id.toString();
			} else if (activeRow.network6Manual) {
				comboBoxes.ipv6.value = activeRow.network6Manual as string;
			}

			if (activeRow.gatewayAddressObj && activeRow.gatewayAddressObj.id) {
				comboBoxes.ipv4Gw.value = activeRow.gatewayAddressObj.id.toString();
			} else if (activeRow.gatewayManual) {
				comboBoxes.ipv4Gw.value = activeRow.gatewayManual as string;
			}

			if (activeRow.gateway6AddressObj && activeRow.gateway6AddressObj.id) {
				comboBoxes.ipv6Gw.value = activeRow.gateway6AddressObj.id.toString();
			} else if (activeRow.gateway6Manual) {
				comboBoxes.ipv6Gw.value = activeRow.gateway6Manual as string;
			}

			confirmModals.editSwitch.disableIPv6 = (activeRow.disableIPv6 as boolean) || false;
			confirmModals.editSwitch.private = (activeRow.private as boolean) || false;
			confirmModals.editSwitch.dhcp = (activeRow.dhcp as boolean) || false;
			confirmModals.editSwitch.slaac = (activeRow.slaac as boolean) || false;
			confirmModals.editSwitch.defaultRoute = (activeRow.defaultRoute as boolean) || false;
			confirmModals.editSwitch.disableBridgeOffloads =
				(activeRow.disableBridgeOffloads as boolean) || false;

			comboBoxes.ports.value = activeRow.ports.map((port: { name: string }) => port.name);
		}
	}

	function resetModal(close: boolean = true) {
		if (close) {
			confirmModals.newSwitch.open = false;
			confirmModals.deleteSwitch.open = false;
			confirmModals.editSwitch.open = false;
		}

		confirmModals.newSwitch.name = '';
		confirmModals.newSwitch.mtu = '';
		confirmModals.newSwitch.vlan = '';
		confirmModals.newSwitch.disableIPv6 = false;
		confirmModals.newSwitch.private = false;
		confirmModals.newSwitch.dhcp = false;
		confirmModals.newSwitch.slaac = false;
		confirmModals.newSwitch.defaultRoute = false;
		confirmModals.newSwitch.disableBridgeOffloads = true;

		confirmModals.editSwitch.name = '';
		confirmModals.editSwitch.mtu = '';
		confirmModals.editSwitch.vlan = '';
		confirmModals.editSwitch.address = '0';
		confirmModals.editSwitch.address6 = '0';
		confirmModals.editSwitch.disableIPv6 = false;
		confirmModals.editSwitch.private = false;
		confirmModals.editSwitch.dhcp = false;
		confirmModals.editSwitch.slaac = false;
		confirmModals.editSwitch.defaultRoute = false;
		confirmModals.editSwitch.disableBridgeOffloads = false;

		comboBoxes.ipv4.value = '';
		comboBoxes.ipv4Gw.value = '';
		comboBoxes.ipv6.value = '';
		comboBoxes.ipv6Gw.value = '';
		comboBoxes.ports.value = [];

		if (close) {
			activeRows = null;
		}
	}

	watch(
		[() => confirmModals.newSwitch.slaac, () => confirmModals.editSwitch.slaac],
		(nwSLAAC, editSLAAC) => {
			if (nwSLAAC) {
				confirmModals.newSwitch.disableIPv6 = false;
			}

			if (editSLAAC) {
				confirmModals.editSwitch.disableIPv6 = false;
			}
		}
	);

	watch(
		[() => confirmModals.newSwitch.disableIPv6, () => confirmModals.editSwitch.disableIPv6],
		(nwDisableIPv6, editDisableIPv6) => {
			if (nwDisableIPv6) {
				confirmModals.newSwitch.slaac = false;
			}

			if (editDisableIPv6) {
				confirmModals.editSwitch.slaac = false;
			}
		}
	);

	watch(
		[() => confirmModals.newSwitch.dhcp, () => confirmModals.editSwitch.dhcp],
		(nwDHCP, editDHCP) => {
			if (nwDHCP || editDHCP) {
				comboBoxes.ipv4.value = '';
				comboBoxes.ipv4Gw.value = '';

				if (nwDHCP) {
					confirmModals.newSwitch.defaultRoute = false;
				} else if (editDHCP) {
					confirmModals.editSwitch.defaultRoute = false;
				}
			}
		}
	);

	watch(
		[() => confirmModals.newSwitch.slaac, () => confirmModals.editSwitch.slaac],
		(nwSLAAC, editSLAAC) => {
			if (nwSLAAC || editSLAAC) {
				comboBoxes.ipv6.value = '';
				comboBoxes.ipv6Gw.value = '';
			}
		}
	);
</script>

{#snippet button(type: string)}
	{#if activeRow && Object.keys(activeRow).length > 0}
		{#if type === 'edit'}
			<Button onclick={handleEdit} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{:else if type === 'delete'}
			<Button onclick={handleDelete} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => {
				confirmModals.active = 'newSwitch';
				confirmModals.newSwitch.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	<TreeTable
		name="tt-switches"
		data={tableData}
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

{#if confirmModals.active === 'newSwitch' || confirmModals.active === 'editSwitch'}
	<Dialog.Root bind:open={confirmModals[confirmModals.active].open}>
		<Dialog.Content
			class="w-[90%] gap-4 p-6 lg:max-w-2xl overflow-hidden"
			showCloseButton={true}
			showResetButton={true}
			onReset={() => resetModal(false)}
			onClose={() => resetModal(false)}
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[clarity--network-switch-line]"
						size="h-6 w-6"
						gap="gap-2"
						title={confirmModals.active === 'editSwitch'
							? `Edit Standard Switch - ${confirmModals.editSwitch.oldName}`
							: 'Create Standard Switch'}
					/>
				</Dialog.Title>
			</Dialog.Header>

			{#if confirmModals.active === 'newSwitch'}
				<CustomValueInput
					label="Name"
					placeholder="public"
					bind:value={confirmModals[confirmModals.active].name}
					classes="flex-1 space-y-1.5"
				/>
			{/if}

			<div class="flex gap-4 min-w-0">
				<CustomValueInput
					label="MTU"
					placeholder="1280"
					bind:value={confirmModals[confirmModals.active].mtu}
					classes="flex-1 space-y-1.5"
					type="number"
				/>

				<CustomValueInput
					label="VLAN"
					placeholder="0"
					bind:value={confirmModals[confirmModals.active].vlan}
					classes="flex-1 space-y-1.5"
					type="number"
				/>
			</div>

			<div class="flex gap-4 min-w-0">
				<CustomComboBox
					bind:open={comboBoxes.ipv4.open}
					label="IPv4 Network"
					bind:value={comboBoxes.ipv4.value}
					data={ipv4NetworkOptions}
					classes="flex-1 space-y-1"
					placeholder="Select object or type CIDR (192.168.1.1/24)"
					width="w-full"
					disabled={confirmModals[confirmModals.active].dhcp ? true : false}
					multiple={false}
					allowCustom={true}
				></CustomComboBox>

				<CustomComboBox
					bind:open={comboBoxes.ipv4Gw.open}
					label="IPv4 Gateway"
					bind:value={comboBoxes.ipv4Gw.value}
					data={ipv4GatewayOptions}
					classes="flex-1 space-y-1"
					placeholder="Select object or type IP (192.168.1.254)"
					width="w-full"
					disabled={confirmModals[confirmModals.active].dhcp ? true : false}
					multiple={false}
					allowCustom={true}
				></CustomComboBox>
			</div>

			<div class="flex gap-4 min-w-0">
				<CustomComboBox
					bind:open={comboBoxes.ipv6.open}
					label="IPv6 Network"
					bind:value={comboBoxes.ipv6.value}
					data={ipv6NetworkOptions}
					classes="flex-1 space-y-1"
					placeholder="Select object or type CIDR (2001:db8::1/64)"
					width="w-full"
					disabled={confirmModals[confirmModals.active].disableIPv6 ||
					confirmModals[confirmModals.active].slaac
						? true
						: false}
					multiple={false}
					allowCustom={true}
				></CustomComboBox>

				<CustomComboBox
					bind:open={comboBoxes.ipv6Gw.open}
					label="IPv6 Gateway"
					bind:value={comboBoxes.ipv6Gw.value}
					data={ipv6GatewayOptions}
					classes="flex-1 space-y-1"
					placeholder="Select object or type IP (2001:db8::1)"
					width="w-full"
					disabled={confirmModals[confirmModals.active].disableIPv6 ||
					confirmModals[confirmModals.active].slaac
						? true
						: false}
					multiple={false}
					allowCustom={true}
				></CustomComboBox>
			</div>

			{#if confirmModals.active === 'newSwitch'}
				<CustomComboBox
					bind:open={comboBoxes.ports.open}
					label="Ports"
					bind:value={comboBoxes.ports.value}
					data={generateComboboxOptions(useablePorts)}
					classes="flex-1 space-y-1"
					placeholder="Select ports"
					multiple={true}
					width="w-full"
				></CustomComboBox>
			{:else}
				<CustomComboBox
					bind:open={comboBoxes.ports.open}
					label="Ports"
					bind:value={comboBoxes.ports.value}
					data={generateComboboxOptions(useablePorts, activeRow?.portsOnly)}
					classes="flex-1 space-y-1"
					placeholder="Select ports"
					multiple={true}
					width="w-full"
				></CustomComboBox>
			{/if}

			<div class="grid grid-cols-3 items-center gap-x-4 gap-y-2">
				<CustomCheckbox
					label="Private"
					bind:checked={confirmModals[confirmModals.active].private}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				<CustomCheckbox
					label="DHCP"
					bind:checked={confirmModals[confirmModals.active].dhcp}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				<CustomCheckbox
					label="SLAAC"
					bind:checked={confirmModals[confirmModals.active].slaac}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				<CustomCheckbox
					label="Disable IPV6"
					bind:checked={confirmModals[confirmModals.active].disableIPv6}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				<CustomCheckbox
					label="Disable Bridge Offloads"
					bind:checked={confirmModals[confirmModals.active].disableBridgeOffloads}
					classes="flex items-center gap-2 mt-1"
					title="Disables bridge-sensitive TOE, TX checksum, TSO, LRO, and MEXTPG capabilities on selected ports before bridge attachment. Recommended to prevent link flaps when taps or epairs are added and removed. Enabling it can briefly interrupt port traffic once; disabling the option only stops enforcement but does not re-enable capabilities."
				></CustomCheckbox>

				{#if !confirmModals[confirmModals.active].dhcp}
					<CustomCheckbox
						label="Default Route"
						bind:checked={confirmModals[confirmModals.active].defaultRoute}
						classes="flex items-center gap-2 mt-1"
					></CustomCheckbox>
				{/if}
			</div>

			<Dialog.Footer class="flex justify-between gap-2 ">
				<div class="flex gap-2">
					{#if confirmModals.active === 'editSwitch'}
						<Button
							onclick={confirmAction}
							type="submit"
							size="sm"
							class="w-full lg:w-28"
							disabled={saving}
						>
							{#if saving}
								<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
								Saving…
							{:else}
								Save
							{/if}
						</Button>
					{:else}
						<Button
							onclick={confirmAction}
							type="submit"
							size="sm"
							class="w-full lg:w-28"
							disabled={saving}
						>
							{#if saving}
								<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
								Creating…
							{:else}
								Create
							{/if}
						</Button>
					{/if}
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={confirmModals.deleteSwitch.open}
	keepOpenOnConfirm={true}
	names={{ parent: 'switch', element: confirmModals.deleteSwitch.name }}
	actions={{
		onConfirm: async () => {
			const result = await deleteSwitch(confirmModals.deleteSwitch.id);
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error(deleteErrorMessage(result.error), { position: 'bottom-center' });
				return;
			}

			toast.success(`Switch ${confirmModals.deleteSwitch.name} deleted`, {
				position: 'bottom-center'
			});
			reload = true;
			resetModal(true);
		},
		onCancel: () => {
			resetModal(true);
		}
	}}
></AlertDialog>
