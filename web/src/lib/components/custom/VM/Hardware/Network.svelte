<script lang="ts">
	import {
		attachNetwork,
		updateNetwork as updateNetworkAPI,
		type NetworkAttachRequest,
		type NetworkUpdateRequest
	} from '$lib/api/vm/network';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { generateMACOptions } from '$lib/utils/network/object';
	import { toast } from 'svelte-sonner';

	type NetworkEmulation = NetworkAttachRequest['emulation'];

	interface Props {
		open: boolean;
		node: string;
		switches: SwitchList;
		vm: VM | null;
		networkObjects: NetworkObject[];
		networkId: number | null;
		reload: boolean;
	}

	let {
		open = $bindable(),
		node,
		switches,
		vm,
		networkObjects,
		networkId,
		reload = $bindable(false)
	}: Props = $props();
	let networks = $derived(vm?.networks ?? []);
	let selectedNetwork = $derived(
		networkId === null ? null : (networks.find((network) => network.id === networkId) ?? null)
	);
	let selectedSwitchName = $derived.by(() => {
		if (!selectedNetwork) return '';
		if (selectedNetwork.switchType === 'standard') {
			return (
				switches.standard.find((networkSwitch) => networkSwitch.id === selectedNetwork.switchId)
					?.name ?? ''
			);
		}
		if (selectedNetwork.switchType === 'manual') {
			return (
				switches.manual.find((networkSwitch) => networkSwitch.id === selectedNetwork.switchId)
					?.name ?? ''
			);
		}
		return '';
	});
	let selectedMacId = $derived(selectedNetwork?.macId ?? null);
	let usable = $derived([
		...switches.standard.map((networkSwitch) => ({
			...networkSwitch,
			uid: `standard-${networkSwitch.id}`
		})),
		...switches.manual.map((networkSwitch) => ({
			...networkSwitch,
			uid: `manual-${networkSwitch.id}`
		}))
	]);

	let usableMacs = $derived(
		networkObjects.filter(
			(object) =>
				object.type === 'Mac' &&
				object.entries?.length === 1 &&
				(object.isUsed === false ||
					object.isUsedBy === 'dhcp' ||
					(selectedMacId !== null && object.id === selectedMacId))
		)
	);

	function createAttachOptions() {
		return {
			emulation: '',
			mac: {
				open: false,
				value: ''
			},
			switchId: '',
			loading: false
		};
	}

	function createEditOptions() {
		return {
			emulation: selectedNetwork?.emulation ?? '',
			mac: {
				open: false,
				value: selectedNetwork?.macId ? selectedNetwork.macId.toString() : '0'
			},
			switchId: selectedSwitchName,
			enable: selectedNetwork?.enable ?? true,
			loading: false
		};
	}

	let properties = $state(createAttachOptions());
	let editProperties = $state(createEditOptions());
	let loading = $derived(properties.loading || editProperties.loading);

	const toastOptions = {
		position: 'bottom-center' as const
	};

	function isNetworkEmulation(value: string): value is NetworkEmulation {
		return value === 'virtio' || value === 'e1000';
	}

	function parseMacId(value: string): number | null {
		const id = Number(value);
		if (!Number.isSafeInteger(id) || id < 0) return null;
		return id;
	}

	function validVMRID(): number | null {
		const rid = vm?.rid ?? 0;
		return Number.isSafeInteger(rid) && rid > 0 ? rid : null;
	}

	function resetForm() {
		if (selectedNetwork) {
			editProperties = createEditOptions();
		} else {
			properties = createAttachOptions();
		}
	}

	async function addNetwork() {
		const rid = validVMRID();
		const macId = parseMacId(properties.mac.value);
		if (rid === null) {
			toast.error('Invalid virtual machine', toastOptions);
			return;
		}
		if (!properties.switchId) {
			toast.error('Switch is required', toastOptions);
			return;
		}
		if (!isNetworkEmulation(properties.emulation)) {
			toast.error('Emulation is required', toastOptions);
			return;
		}
		if (macId === null) {
			toast.error('Invalid MAC selection', toastOptions);
			return;
		}

		const request: NetworkAttachRequest = {
			switchName: properties.switchId,
			emulation: properties.emulation,
			macId
		};
		properties.loading = true;
		try {
			const response = await attachNetwork(rid, request, { hostname: node });
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Error attaching VM to switch', toastOptions);
				return;
			}

			toast.success('VM attached to switch', toastOptions);
			reload = true;
			properties = createAttachOptions();
			open = false;
		} catch {
			toast.error('Error attaching VM to switch', toastOptions);
		} finally {
			properties.loading = false;
		}
	}

	async function updateNetwork() {
		const rid = validVMRID();
		const macId = parseMacId(editProperties.mac.value);
		if (!selectedNetwork || rid === null) {
			toast.error('Invalid virtual machine network', toastOptions);
			return;
		}
		if (!editProperties.switchId) {
			toast.error('Switch is required', toastOptions);
			return;
		}
		if (!isNetworkEmulation(editProperties.emulation)) {
			toast.error('Emulation is required', toastOptions);
			return;
		}
		if (macId === null) {
			toast.error('Invalid MAC selection', toastOptions);
			return;
		}

		const request: NetworkUpdateRequest = {
			switchName: editProperties.switchId,
			emulation: editProperties.emulation,
			macId,
			enable: editProperties.enable
		};
		editProperties.loading = true;
		try {
			const response = await updateNetworkAPI(rid, selectedNetwork.id, request, {
				hostname: node
			});
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Error updating VM network', toastOptions);
				return;
			}

			toast.success('VM network updated', toastOptions);
			reload = true;
			editProperties = createEditOptions();
			open = false;
		} catch {
			toast.error('Error updating VM network', toastOptions);
		} finally {
			editProperties.loading = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-md overflow-hidden p-5 lg:max-w-2xl"
		showResetButton={!loading}
		showCloseButton={!loading}
		onReset={resetForm}
		onClose={() => {
			if (loading) return;
			resetForm();
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--network]"
					size="h-5 w-5"
					gap="gap-2"
					title={selectedNetwork ? `Edit - ${selectedSwitchName || 'Network'}` : 'New Network'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		{#if !selectedNetwork}
			<SimpleSelect
				label="Switch"
				placeholder="Select Switch"
				options={usable.map((networkSwitch) => ({
					value: networkSwitch.name,
					label: networkSwitch.name
				}))}
				bind:value={properties.switchId}
				onChange={(value) => (properties.switchId = value)}
				disabled={loading}
				classes={{
					parent: 'flex-1 space-y-1',
					label: 'flex h-7 items-center whitespace-nowrap text-sm',
					trigger:
						'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
				}}
			/>

			<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
				<SimpleSelect
					label="Emulation"
					placeholder="Select Emulation"
					options={[
						{ value: 'virtio', label: 'VirtIO' },
						{ value: 'e1000', label: 'E1000' }
					]}
					bind:value={properties.emulation}
					onChange={(value) => (properties.emulation = value)}
					disabled={loading}
					classes={{
						parent: 'flex-1 space-y-1',
						label: 'flex h-7 items-center whitespace-nowrap text-sm',
						trigger:
							'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				<CustomComboBox
					bind:open={properties.mac.open}
					label="MAC"
					bind:value={properties.mac.value}
					data={generateMACOptions(usableMacs)}
					classes="flex-1 space-y-1"
					placeholder="Select MAC"
					width="w-3/4"
					multiple={false}
					disabled={loading}
				></CustomComboBox>
			</div>
		{:else}
			<SimpleSelect
				label="Switch"
				placeholder="Select Switch"
				options={usable.map((networkSwitch) => ({
					value: networkSwitch.name,
					label: networkSwitch.name
				}))}
				bind:value={editProperties.switchId}
				onChange={(value) => (editProperties.switchId = value)}
				disabled={loading}
				classes={{
					parent: 'flex-1 space-y-1',
					label: 'flex h-7 items-center whitespace-nowrap text-sm',
					trigger:
						'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
				}}
			/>

			<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
				<SimpleSelect
					label="Emulation"
					placeholder="Select Emulation"
					options={[
						{ value: 'virtio', label: 'VirtIO' },
						{ value: 'e1000', label: 'E1000' }
					]}
					bind:value={editProperties.emulation}
					onChange={(value) => (editProperties.emulation = value)}
					disabled={loading}
					classes={{
						parent: 'flex-1 space-y-1',
						label: 'flex h-7 items-center whitespace-nowrap text-sm',
						trigger:
							'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
					}}
				/>

				<CustomComboBox
					bind:open={editProperties.mac.open}
					label="MAC"
					bind:value={editProperties.mac.value}
					data={generateMACOptions(usableMacs)}
					classes="flex-1 space-y-1"
					placeholder="Select MAC"
					width="w-3/4"
					multiple={false}
					disabled={loading}
				></CustomComboBox>
			</div>
			<div class="mt-1">
				<CustomCheckbox
					label="Enabled (Available to VM)"
					bind:checked={editProperties.enable}
					classes="flex items-center gap-2"
					disabled={loading}
				/>
			</div>
		{/if}
		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button
					onclick={() => {
						if (selectedNetwork) {
							updateNetwork();
						} else {
							addNetwork();
						}
					}}
					type="button"
					size="sm"
					disabled={loading}
				>
					{#if loading}
						<span class="icon-[eos-icons--loading] mr-2 h-4 w-4 animate-spin"></span>
						<span>{selectedNetwork ? 'Saving...' : 'Attaching...'}</span>
					{:else}
						<span>{selectedNetwork ? 'Save Changes' : 'Attach Network'}</span>
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
