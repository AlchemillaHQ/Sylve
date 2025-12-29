<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { createSwitch, deleteSwitch, getSwitches, updateSwitch } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Iface } from '$lib/types/network/iface';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { generateIPOptions, generateNetworkOptions } from '$lib/utils/network/object';
	import { generateTableData } from '$lib/utils/network/switch/standard';
	import { isValidMTU, isValidVLAN } from '$lib/utils/numbers';
	import { isValidSwitchName } from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		interfaces: Iface[];
		switches: SwitchList;
		objects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

	const networkInterfaces = resource(
		() => 'network-interfaces',
		async (key, prevKey, { signal }) => {
			const res = await getInterfaces();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.interfaces }
	);

	const switches = resource(
		() => 'network-switches',
		async (key, prevKey, { signal }) => {
			const res = await getSwitches();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.switches }
	);

	const networkObjects = resource(
		() => 'network-objects',
		async (key, prevKey, { signal }) => {
			const res = await getNetworkObjects();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.objects }
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
			defaultRoute: false
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
			defaultRoute: false
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

	let reload = $state(false);

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
		if (confirmModals.active === 'newSwitch' || confirmModals.active === 'editSwitch') {
			const activeModal = confirmModals[confirmModals.active];
			if (!isValidSwitchName(activeModal.name)) {
				toast.error('Invalid switch name', {
					position: 'bottom-center'
				});

				return;
			}

			if (
				activeModal.mtu !== '' &&
				activeModal.mtu !== null &&
				!isValidMTU(parseInt(activeModal.mtu))
			) {
				toast.error('Invalid MTU', {
					position: 'bottom-center'
				});

				return;
			}

			if (
				activeModal.vlan !== '' &&
				activeModal.vlan !== null &&
				!isValidVLAN(parseInt(activeModal.vlan))
			) {
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

			activeModal.network4 = comboBoxes.ipv4.value;
			activeModal.gwAddress4 = comboBoxes.ipv4Gw.value;
			activeModal.network6 = comboBoxes.ipv6.value;
			activeModal.gwAddress6 = comboBoxes.ipv6Gw.value;

			if (confirmModals.active === 'newSwitch') {
				const created = await createSwitch(
					activeModal.name,
					parseInt(activeModal.mtu),
					parseInt(activeModal.vlan),
					Number(activeModal.network4),
					Number(activeModal.gwAddress4),
					Number(activeModal.network6),
					Number(activeModal.gwAddress6),
					activeModal.private,
					activeModal.dhcp,
					comboBoxes.ports.value,
					activeModal.disableIPv6,
					activeModal.slaac,
					activeModal.defaultRoute
				);

				reload = true;

				if (isAPIResponse(created) && created.status === 'success') {
					toast.success(`Switch ${confirmModals.newSwitch.name} created`, {
						position: 'bottom-center'
					});
				} else {
					toast.error('Error creating switch', {
						position: 'bottom-center'
					});
				}
			} else {
				activeModal.network4 = comboBoxes.ipv4.value;
				activeModal.network6 = comboBoxes.ipv6.value;
				activeModal.gwAddress4 = comboBoxes.ipv4Gw.value;
				activeModal.gwAddress6 = comboBoxes.ipv6Gw.value;

				const edited = await updateSwitch(
					activeRow?.id as number,
					parseInt(activeModal.mtu),
					parseInt(activeModal.vlan),
					Number(activeModal.network4),
					Number(activeModal.gwAddress4),
					Number(activeModal.network6),
					Number(activeModal.gwAddress6),
					activeModal.private,
					comboBoxes.ports.value,
					activeModal.disableIPv6,
					activeModal.slaac,
					activeModal.dhcp,
					activeModal.defaultRoute
				);

				reload = true;

				if (isAPIResponse(edited) && edited.status === 'success') {
					toast.success(`Switch ${confirmModals.editSwitch.name} updated`, {
						position: 'bottom-center'
					});
				} else {
					toast.error('Error updating switch', {
						position: 'bottom-center'
					});
				}
			}

			resetModal(true);
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

	function handleEdit() {
		if (activeRow && Object.keys(activeRow).length > 0) {
			confirmModals.active = 'editSwitch';
			confirmModals.editSwitch.open = true;
			confirmModals.editSwitch.oldName = activeRow.name;
			confirmModals.editSwitch.name = activeRow.name;
			confirmModals.editSwitch.mtu = activeRow.mtu as string;
			confirmModals.editSwitch.vlan = activeRow.vlan === '-' ? '' : (activeRow.vlan as string);

			if (activeRow.networkObj) {
				if (activeRow.networkObj.id) {
					comboBoxes.ipv4.value = activeRow.networkObj.id.toString();
				}
			}

			if (activeRow.network6Obj) {
				if (activeRow.network6Obj.id) {
					comboBoxes.ipv6.value = activeRow.network6Obj.id.toString();
				}
			}

			if (activeRow.gatewayAddressObj) {
				if (activeRow.gatewayAddressObj.id) {
					comboBoxes.ipv4Gw.value = activeRow.gatewayAddressObj.id.toString();
				}
			}

			if (activeRow.gateway6AddressObj) {
				if (activeRow.gateway6AddressObj.id) {
					comboBoxes.ipv6Gw.value = activeRow.gateway6AddressObj.id.toString();
				}
			}

			confirmModals.editSwitch.disableIPv6 = (activeRow.disableIPv6 as boolean) || false;
			confirmModals.editSwitch.private = (activeRow.private as boolean) || false;
			confirmModals.editSwitch.dhcp = (activeRow.dhcp as boolean) || false;
			confirmModals.editSwitch.slaac = (activeRow.slaac as boolean) || false;
			confirmModals.editSwitch.defaultRoute = (activeRow.defaultRoute as boolean) || false;

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

		confirmModals.editSwitch.name = '';
		confirmModals.editSwitch.mtu = '';
		confirmModals.editSwitch.vlan = '';
		confirmModals.editSwitch.address = '0';
		confirmModals.editSwitch.address6 = '0';
		confirmModals.editSwitch.disableIPv6 = false;
		confirmModals.editSwitch.private = false;
		confirmModals.editSwitch.dhcp = false;
		confirmModals.editSwitch.slaac = false;

		comboBoxes.ports.value = [];

		activeRows = null;
	}

	$effect(() => {
		if (confirmModals.newSwitch.slaac) {
			confirmModals.newSwitch.disableIPv6 = false;
		}

		if (confirmModals.editSwitch.slaac) {
			confirmModals.editSwitch.disableIPv6 = false;
		}
	});

	$effect(() => {
		if (confirmModals.newSwitch.disableIPv6) {
			confirmModals.newSwitch.slaac = false;
		}

		if (confirmModals.editSwitch.disableIPv6) {
			confirmModals.editSwitch.slaac = false;
		}
	});

	$effect(() => {
		if (confirmModals.newSwitch.dhcp) {
			comboBoxes.ipv4.value = '';
			comboBoxes.ipv4Gw.value = '';
			confirmModals.newSwitch.defaultRoute = false;
		}
	});

	$effect(() => {
		if (confirmModals.newSwitch.slaac) {
			comboBoxes.ipv6.value = '';
			comboBoxes.ipv6Gw.value = '';
		}
	});

	$effect(() => {
		if (confirmModals.editSwitch.dhcp) {
			comboBoxes.ipv4.value = '';
			comboBoxes.ipv4Gw.value = '';
			confirmModals.editSwitch.defaultRoute = false;
		}
	});

	$effect(() => {
		if (confirmModals.editSwitch.slaac) {
			comboBoxes.ipv6.value = '';
			comboBoxes.ipv6Gw.value = '';
		}
	});
</script>

{#snippet button(type: string)}
	{#if activeRow && Object.keys(activeRow).length > 0}
		{#if type === 'edit'}
			<Button onclick={handleEdit} size="sm" variant="outline" class="h-6.5">
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>

					<span>Edit</span>
				</div>
			</Button>
		{:else if type === 'delete'}
			<Button onclick={handleDelete} size="sm" variant="outline" class="h-6.5">
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
					<span>Delete</span>
				</div>
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
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
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
			class="w-[90%] gap-4 p-5 lg:max-w-2xl"
			onInteractOutside={(e) => e.preventDefault()}
			onEscapeKeydown={(e) => e.preventDefault()}
		>
			<div class="flex items-center justify-between">
				<Dialog.Header>
					<Dialog.Title>
						<div class="flex items-center">
							<span class="icon-[clarity--network-switch-line] mr-2 h-6 w-6"></span>
							{#if confirmModals.active === 'editSwitch'}
								{`Edit Standard Switch - ${confirmModals.editSwitch.oldName}`}
							{:else}
								{'Create Standard Switch'}
							{/if}
						</div>
					</Dialog.Title>
				</Dialog.Header>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => resetModal(false)}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => resetModal(true)}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</div>

			{#if confirmModals.active === 'newSwitch'}
				<CustomValueInput
					label={'Name'}
					placeholder="public"
					bind:value={confirmModals[confirmModals.active].name}
					classes="flex-1 space-y-1.5"
				/>
			{/if}

			<div class="flex gap-4">
				<CustomValueInput
					label={'MTU'}
					placeholder="1280"
					bind:value={confirmModals[confirmModals.active].mtu}
					classes="flex-1 space-y-1.5"
					type="number"
				/>

				<CustomValueInput
					label={'VLAN'}
					placeholder="0"
					bind:value={confirmModals[confirmModals.active].vlan}
					classes="flex-1 space-y-1.5"
					type="number"
				/>
			</div>

			<div class="flex gap-4">
				<CustomComboBox
					bind:open={comboBoxes.ipv4.open}
					label={'IPv4 Network'}
					bind:value={comboBoxes.ipv4.value}
					data={generateNetworkOptions(networkObjects.current, 'IPv4')}
					classes="flex-1 space-y-1"
					placeholder="Select IPv4 Network"
					width="w-3/4"
					disabled={confirmModals[confirmModals.active].dhcp ? true : false}
					multiple={false}
				></CustomComboBox>

				<CustomComboBox
					bind:open={comboBoxes.ipv4Gw.open}
					label={'IPv4 Gateway'}
					bind:value={comboBoxes.ipv4Gw.value}
					data={generateIPOptions(networkObjects.current, 'IPv4')}
					classes="flex-1 space-y-1"
					placeholder="Select IPv4 Gateway"
					width="w-3/4"
					disabled={confirmModals[confirmModals.active].dhcp ? true : false}
					multiple={false}
				></CustomComboBox>
			</div>

			<div class="flex gap-4">
				<CustomComboBox
					bind:open={comboBoxes.ipv6.open}
					label={'IPv6 Network'}
					bind:value={comboBoxes.ipv6.value}
					data={generateNetworkOptions(networkObjects.current, 'IPv6')}
					classes="flex-1 space-y-1"
					placeholder="Select IPv6 Network"
					width="w-3/4"
					disabled={confirmModals[confirmModals.active].disableIPv6 ||
					confirmModals[confirmModals.active].slaac
						? true
						: false}
					multiple={false}
				></CustomComboBox>

				<CustomComboBox
					bind:open={comboBoxes.ipv6Gw.open}
					label={'IPv6 Gateway'}
					bind:value={comboBoxes.ipv6Gw.value}
					data={generateIPOptions(networkObjects.current, 'IPv6')}
					classes="flex-1 space-y-1"
					placeholder="Select IPv6 Gateway"
					width="w-3/4"
					disabled={confirmModals[confirmModals.active].disableIPv6 ||
					confirmModals[confirmModals.active].slaac
						? true
						: false}
					multiple={false}
				></CustomComboBox>
			</div>

			{#if confirmModals.active === 'newSwitch'}
				<CustomComboBox
					bind:open={comboBoxes.ports.open}
					label={'Ports'}
					bind:value={comboBoxes.ports.value}
					data={generateComboboxOptions(useablePorts)}
					classes="flex-1 space-y-1"
					placeholder="Select ports"
					multiple={true}
					width="w-3/4"
				></CustomComboBox>
			{:else}
				<CustomComboBox
					bind:open={comboBoxes.ports.open}
					label={'Ports'}
					bind:value={comboBoxes.ports.value}
					data={generateComboboxOptions(useablePorts, activeRow?.portsOnly)}
					classes="flex-1 space-y-1"
					placeholder="Select ports"
					multiple={true}
					width="w-3/4"
				></CustomComboBox>
			{/if}

			<div class="flex items-center gap-2">
				<CustomCheckbox
					label={'Private'}
					bind:checked={confirmModals[confirmModals.active].private}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				<CustomCheckbox
					label={'DHCP'}
					bind:checked={confirmModals[confirmModals.active].dhcp}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				<CustomCheckbox
					label={'SLAAC'}
					bind:checked={confirmModals[confirmModals.active].slaac}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				<CustomCheckbox
					label={'Disable IPV6'}
					bind:checked={confirmModals[confirmModals.active].disableIPv6}
					classes="flex items-center gap-2 mt-1"
				></CustomCheckbox>

				{#if !confirmModals[confirmModals.active].dhcp}
					<CustomCheckbox
						label={'Default Route'}
						bind:checked={confirmModals[confirmModals.active].defaultRoute}
						classes="flex items-center gap-2 mt-1"
					></CustomCheckbox>
				{/if}
			</div>

			<Dialog.Footer class="flex justify-between gap-2 ">
				<div class="flex gap-2">
					{#if confirmModals.active === 'editSwitch'}
						<Button onclick={confirmAction} type="submit" size="sm" class="w-full lg:w-28"
							>{'Save'}</Button
						>
					{:else}
						<Button onclick={confirmAction} type="submit" size="sm" class="w-full lg:w-28"
							>{'Create'}</Button
						>
					{/if}
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

<AlertDialog
	open={confirmModals.deleteSwitch.open}
	names={{ parent: 'switch', element: confirmModals.deleteSwitch.name }}
	actions={{
		onConfirm: async () => {
			const result = await deleteSwitch(confirmModals.deleteSwitch.id);
			reload = true;
			if (isAPIResponse(result) && result.status === 'success') {
				toast.success(`Switch ${confirmModals.deleteSwitch.name} deleted`, {
					position: 'bottom-center'
				});
			} else {
				if (result && result.error) {
					if (result.error === 'switch_in_use_by_vm') {
						toast.error('Switch is in use by a VM', { position: 'bottom-center' });
					} else {
						toast.error('Error deleting switch', { position: 'bottom-center' });
					}
				}
			}

			resetModal(true);
		},
		onCancel: () => {
			resetModal(true);
		}
	}}
></AlertDialog>
