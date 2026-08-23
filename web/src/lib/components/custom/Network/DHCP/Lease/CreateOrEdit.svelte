<script lang="ts">
	import type { DHCPRange, Leases } from '$lib/types/network/dhcp';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { NetworkObject } from '$lib/types/network/object';
	import {
		generateDUIDOptions,
		generateIPOptions,
		generateMACOptions
	} from '$lib/utils/network/object';
	import { toast } from 'svelte-sonner';
	import { isValidIPv4, isValidIPv6, validateDnsmasqHostname } from '$lib/utils/string';
	import { createDHCPLease, modifyDHCPLease } from '$lib/api/network/dhcp';
	import { handleAPIError } from '$lib/utils/http';

	interface Props {
		open: boolean;
		reload: boolean;
		dhcpRanges: DHCPRange[];
		dhcpLeases: Leases;
		selectedLease: string | null | number;
		networkObjects: NetworkObject[];
	}

	interface ComboBoxState {
		value: string;
		open: boolean;
	}

	interface LeaseFormState {
		hostname: string;
		comments: string;
		dhcpRange: { combobox: ComboBoxState };
		identifier: { combobox: ComboBoxState };
		ip: { combobox: ComboBoxState };
	}

	let {
		open = $bindable(),
		reload = $bindable(),
		dhcpRanges,
		dhcpLeases,
		selectedLease = null,
		networkObjects
	}: Props = $props();

	function editingLease() {
		if (selectedLease === null) return null;
		return dhcpLeases.db.find((lease) => lease.id === Number(selectedLease)) ?? null;
	}

	function initialFormState(): LeaseFormState {
		const lease = editingLease();
		return {
			hostname: lease?.hostname ?? '',
			comments: lease?.comments ?? '',
			dhcpRange: {
				combobox: {
					value: lease?.dhcpRangeId.toString() ?? '',
					open: false
				}
			},
			identifier: {
				combobox: {
					value: lease?.macObjectId
						? `mac-${lease.macObjectId}`
						: lease?.duidObjectId
							? `duid-${lease.duidObjectId}`
							: '',
					open: false
				}
			},
			ip: {
				combobox: {
					value: lease?.ipObjectId ? `ip-${lease.ipObjectId}` : '',
					open: false
				}
			}
		};
	}

	let properties = $state(initialFormState());
	let saving = $state(false);

	let singleValueObjects = $derived(
		networkObjects.filter((object) => object.entries?.length === 1)
	);
	let rangeOptions = $derived(
		dhcpRanges.map((range) => ({
			value: range.id.toString(),
			label: `${range.startIp} - ${range.endIp} (${range.manualSwitch ? range.manualSwitch.bridge : range.standardSwitch?.name})`
		}))
	);
	let selectedRange = $derived(
		dhcpRanges.find((range) => range.id.toString() === properties.dhcpRange.combobox.value)
	);
	let identifierOptions = $derived.by(() => {
		if (selectedRange?.type === 'ipv4') {
			return generateMACOptions(singleValueObjects, true);
		}
		if (selectedRange?.type === 'ipv6') {
			return generateDUIDOptions(singleValueObjects, true);
		}
		return [];
	});
	let ipOptions = $derived.by(() => {
		if (!selectedRange) return [];
		const requiredPrefix = selectedRange.type === 'ipv4' ? 'mac-' : 'duid-';
		if (!properties.identifier.combobox.value.startsWith(requiredPrefix)) return [];
		return generateIPOptions(singleValueObjects, selectedRange.type, true);
	});

	function handleRangeChange(value: string | string[]) {
		if (typeof value !== 'string') return;
		const nextRange = dhcpRanges.find((range) => range.id.toString() === value);
		if (!nextRange) {
			properties.identifier.combobox.value = '';
			properties.ip.combobox.value = '';
			return;
		}

		const requiredPrefix = nextRange.type === 'ipv4' ? 'mac-' : 'duid-';
		if (!properties.identifier.combobox.value.startsWith(requiredPrefix)) {
			properties.identifier.combobox.value = '';
			properties.ip.combobox.value = '';
			return;
		}

		const validIPOptions = generateIPOptions(singleValueObjects, nextRange.type, true);
		if (!validIPOptions.some((option) => option.value === properties.ip.combobox.value)) {
			properties.ip.combobox.value = '';
		}
	}

	function handleIdentifierChange(value: string | string[]) {
		if (typeof value !== 'string') return;
		properties.ip.combobox.value = '';
	}

	function selectedObject(value: string): NetworkObject | undefined {
		const id = Number(value.split('-')[1]);
		return networkObjects.find((object) => object.id === id);
	}

	function basicTests(): boolean {
		if (!validateDnsmasqHostname(properties.hostname)) {
			toast.error('Invalid hostname', { position: 'bottom-center' });
			return false;
		}

		if (properties.comments.length > 4096) {
			toast.error('Comments must be 4096 characters or fewer', {
				position: 'bottom-center'
			});
			return false;
		}

		if (!selectedRange) {
			toast.error('Range is required', { position: 'bottom-center' });
			return false;
		}

		if (!properties.identifier.combobox.value) {
			toast.error('Identifier is required', { position: 'bottom-center' });
			return false;
		}

		if (!properties.ip.combobox.value) {
			toast.error('IP Address is required', { position: 'bottom-center' });
			return false;
		}

		const identifier = selectedObject(properties.identifier.combobox.value);
		const ip = selectedObject(properties.ip.combobox.value);
		if (!identifier || identifier.entries?.length !== 1 || !ip || ip.entries?.length !== 1) {
			toast.error('Selected objects must contain exactly one value', {
				position: 'bottom-center'
			});
			return false;
		}

		const ipValue = ip.entries[0].value;
		if (selectedRange.type === 'ipv4') {
			if (identifier.type !== 'Mac' || !properties.identifier.combobox.value.startsWith('mac-')) {
				toast.error('Identifier must be a MAC for IPv4 ranges', {
					position: 'bottom-center'
				});
				return false;
			}
			if (ip.type !== 'Host' || !isValidIPv4(ipValue)) {
				toast.error('IP Address must be an IPv4 address for IPv4 ranges', {
					position: 'bottom-center'
				});
				return false;
			}
		}

		if (selectedRange.type === 'ipv6') {
			if (identifier.type !== 'DUID' || !properties.identifier.combobox.value.startsWith('duid-')) {
				toast.error('Identifier must be a DUID for IPv6 ranges', {
					position: 'bottom-center'
				});
				return false;
			}
			if (ip.type !== 'Host' || !isValidIPv6(ipValue)) {
				toast.error('IP Address must be an IPv6 address for IPv6 ranges', {
					position: 'bottom-center'
				});
				return false;
			}
		}

		return true;
	}

	async function save() {
		if (saving || !basicTests() || !selectedRange) return;

		saving = true;
		try {
			const ipObjectId = Number(properties.ip.combobox.value.split('-')[1]);
			const identifierObjectId = Number(properties.identifier.combobox.value.split('-')[1]);
			const response = selectedLease
				? await modifyDHCPLease(
						Number(selectedLease),
						properties.hostname,
						properties.comments,
						ipObjectId,
						selectedRange.type === 'ipv4' ? identifierObjectId : null,
						selectedRange.type === 'ipv6' ? identifierObjectId : null,
						selectedRange.id
					)
				: await createDHCPLease(
						properties.hostname,
						properties.comments,
						ipObjectId,
						selectedRange.type === 'ipv4' ? identifierObjectId : null,
						selectedRange.type === 'ipv6' ? identifierObjectId : null,
						selectedRange.id
					);

			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error(selectedLease ? 'Failed to save lease' : 'Failed to create lease', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success(selectedLease ? 'Lease saved' : 'Lease created', {
				position: 'bottom-center'
			});
			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		showCloseButton={true}
		showResetButton={true}
		onReset={() => (properties = initialFormState())}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--lan-connect]"
					size="h-6 w-6"
					gap="gap-2"
					title={selectedLease ? 'Edit DHCP Lease' : 'Create DHCP Lease'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex flex-row items-end gap-2">
			<CustomValueInput
				label="Hostname"
				bind:value={properties.hostname}
				placeholder="postgres"
				classes="flex-1 min-w-0 space-y-1.5"
			/>

			<CustomComboBox
				bind:open={properties.dhcpRange.combobox.open}
				label="Range"
				bind:value={properties.dhcpRange.combobox.value}
				data={rangeOptions}
				onValueChange={handleRangeChange}
				classes="flex-1 min-w-0 max-w-[360px] space-y-1"
				placeholder="Select Range"
				triggerWidth="w-full"
				width="w-full"
			/>
		</div>

		<div class="flex min-w-0 flex-row gap-2">
			<CustomComboBox
				bind:open={properties.identifier.combobox.open}
				label="Identifier"
				bind:value={properties.identifier.combobox.value}
				data={identifierOptions}
				onValueChange={handleIdentifierChange}
				disabled={!selectedRange}
				classes="basis-0 flex-1 min-w-0 space-y-1"
				placeholder="Select Identifier"
				triggerWidth="w-full"
				width="w-full"
			/>

			<CustomComboBox
				bind:open={properties.ip.combobox.open}
				label="IP Address"
				bind:value={properties.ip.combobox.value}
				data={ipOptions}
				disabled={!properties.identifier.combobox.value}
				classes="basis-0 flex-1 min-w-0 space-y-1"
				placeholder="Select IP Address"
				triggerWidth="w-full"
				width="w-full"
			/>
		</div>

		<CustomValueInput
			label="Comments"
			bind:value={properties.comments}
			placeholder="Optional comments"
			classes="w-full min-w-0 space-y-1.5"
			type="textarea"
			textAreaClasses="min-h-18"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={save} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						{selectedLease ? 'Saving…' : 'Creating…'}
					{:else}
						{selectedLease ? 'Save' : 'Create'}
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
