<script lang="ts">
	import { createDHCPRange, updateDHCPRange } from '$lib/api/network/dhcp';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { DHCPConfig, DHCPRange } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidIPv4Range, isValidIPv6Range } from '$lib/utils/inet';
	import { generateSwitchOptions } from '$lib/utils/input';
	import { dnsmasqToSeconds, secondsToDnsmasq } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';

	type IPType = 'ipv4' | 'ipv6';
	type SwitchKind = 'stan' | 'man';

	interface Props {
		open: boolean;
		reload: boolean;
		networkInterfaces: Iface[];
		networkSwitches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
		selectedRange: DHCPRange | null;
	}

	interface SwitchSelection {
		id: number;
		kind: SwitchKind;
	}

	interface RangeSubmission {
		type: IPType;
		startIp: string;
		endIp: string;
		expiry: number;
		raOnly: boolean;
		slaac: boolean;
		standardSwitchId?: number;
		manualSwitchId?: number;
	}

	let {
		open = $bindable(),
		reload = $bindable(),
		networkInterfaces,
		networkSwitches,
		dhcpRanges,
		dhcpConfig,
		selectedRange = null as DHCPRange | null
	}: Props = $props();

	let saving = $state(false);

	function switchOptionValue(id: number, kind: SwitchKind, name: string): string {
		return `${id}-${kind}-${name}`;
	}

	function optionForRange(range: DHCPRange | null): { label: string; value: string } | null {
		if (range?.standardSwitch) {
			const current =
				networkSwitches.standard.find((sw) => sw.id === range.standardSwitch?.id) ??
				range.standardSwitch;
			return {
				label: current.name,
				value: switchOptionValue(current.id, 'stan', current.name)
			};
		}
		if (range?.manualSwitch) {
			const current =
				networkSwitches.manual.find((sw) => sw.id === range.manualSwitch?.id) ?? range.manualSwitch;
			return {
				label: current.name,
				value: switchOptionValue(current.id, 'man', current.name)
			};
		}
		return null;
	}

	function rangeSwitchKey(range: DHCPRange): string | null {
		if (range.standardSwitchId ?? range.standardSwitch?.id) {
			return `stan:${range.standardSwitchId ?? range.standardSwitch?.id}`;
		}
		if (range.manualSwitchId ?? range.manualSwitch?.id) {
			return `man:${range.manualSwitchId ?? range.manualSwitch?.id}`;
		}
		return null;
	}

	function parseSwitchSelection(value: string): SwitchSelection | null {
		const match = /^(\d+)-(stan|man)-/.exec(value);
		if (!match) return null;

		const id = Number(match[1]);
		if (!Number.isSafeInteger(id) || id <= 0) return null;
		return { id, kind: match[2] as SwitchKind };
	}

	function createProperties() {
		return {
			ipType: {
				combobox: {
					value: (selectedRange?.type ?? 'ipv4') as IPType,
					options: [
						{ label: 'IPv4', value: 'ipv4' },
						{ label: 'IPv6', value: 'ipv6' }
					],
					open: false
				}
			},
			startIp: selectedRange?.startIp ?? '',
			endIp: selectedRange?.endIp ?? '',
			switchId: {
				combobox: {
					open: false,
					value: optionForRange(selectedRange)?.value ?? ''
				}
			},
			expiry: selectedRange ? secondsToDnsmasq(selectedRange.expiry, true) : '12h',
			raOnly: selectedRange?.raOnly ?? false,
			slaac: selectedRange?.slaac ?? false
		};
	}

	let properties = $state(createProperties());
	let currentSwitchOption = $derived(optionForRange(selectedRange));
	let configuredSwitchKeys = $derived.by(() => {
		const configured: string[] = [];
		for (const sw of dhcpConfig.standardSwitches) configured.push(`stan:${sw.id}`);
		for (const sw of dhcpConfig.manualSwitches) configured.push(`man:${sw.id}`);
		return configured;
	});
	let usedSwitchKeys = $derived.by(() => {
		const used: string[] = [];
		for (const range of dhcpRanges) {
			if (selectedRange && range.id === selectedRange.id) continue;
			const switchKey = rangeSwitchKey(range);
			if (switchKey) used.push(`${switchKey}|${range.type}`);
		}
		return used;
	});
	let switchOptions = $derived.by(() => {
		const options = generateSwitchOptions(networkSwitches);
		if (
			currentSwitchOption &&
			!options.some((option) => option.value === currentSwitchOption?.value)
		) {
			options.unshift(currentSwitchOption);
		}

		return options.filter((option) => {
			const selection = parseSwitchSelection(option.value);
			if (!selection) return false;

			const switchKey = `${selection.kind}:${selection.id}`;
			const isCurrent = option.value === currentSwitchOption?.value;
			if (!configuredSwitchKeys.includes(switchKey) && !isCurrent) return false;
			return !usedSwitchKeys.includes(`${switchKey}|${properties.ipType.combobox.value}`);
		});
	});

	function handleIPTypeChange(value: string | string[]) {
		if (value === 'ipv4') {
			properties.raOnly = false;
			properties.slaac = false;
		}
	}

	function selectedSwitchInterface(selection: SwitchSelection): Iface | null {
		let interfaceName = '';
		let displayName = '';
		if (selection.kind === 'stan') {
			const sw =
				networkSwitches.standard.find((candidate) => candidate.id === selection.id) ??
				dhcpConfig.standardSwitches.find((candidate) => candidate.id === selection.id) ??
				(selectedRange && selectedRange.standardSwitchId === selection.id
					? selectedRange.standardSwitch
					: null);
			if (!sw) return null;
			interfaceName = sw.bridgeName;
			displayName = sw.name;
		} else {
			const sw =
				networkSwitches.manual.find((candidate) => candidate.id === selection.id) ??
				dhcpConfig.manualSwitches.find((candidate) => candidate.id === selection.id) ??
				(selectedRange && selectedRange.manualSwitchId === selection.id
					? selectedRange.manualSwitch
					: null);
			if (!sw) return null;
			interfaceName = sw.bridge;
			displayName = sw.name;
		}

		return (
			networkInterfaces.find(
				(iface) =>
					iface.name === interfaceName ||
					iface.description === interfaceName ||
					iface.description === displayName
			) ?? null
		);
	}

	function validateForm(): RangeSubmission | null {
		const type = properties.ipType.combobox.value;
		if (type !== 'ipv4' && type !== 'ipv6') {
			toast.error('IP Type is required', { position: 'bottom-center' });
			return null;
		}

		const selection = parseSwitchSelection(properties.switchId.combobox.value);
		if (!selection) {
			toast.error('No switch selected', { position: 'bottom-center' });
			return null;
		}
		const iface = selectedSwitchInterface(selection);
		if (!iface) {
			toast.error('Failed to find interface for selected switch', {
				position: 'bottom-center'
			});
			return null;
		}

		const startIp = properties.startIp.trim();
		const endIp = properties.endIp.trim();
		if (type === 'ipv4') {
			const addresses = iface.ipv4 ?? [];
			if (addresses.length === 0) {
				toast.error('Selected interface has no IPv4 address', { position: 'bottom-center' });
				return null;
			}
			const inSubnet = addresses.some((address) =>
				isValidIPv4Range(startIp, endIp, address.ip, address.netmask)
			);
			if (!inSubnet) {
				toast.error('IP range is invalid or is not in the switch subnet', {
					position: 'bottom-center'
				});
				return null;
			}
		} else {
			if ((startIp === '') !== (endIp === '')) {
				toast.error('Both IPv6 range addresses must be set, or both left empty', {
					position: 'bottom-center'
				});
				return null;
			}

			const usable = (iface.ipv6 ?? []).filter(
				(address) => !address.ip.toLowerCase().startsWith('fe80')
			);
			if (usable.length === 0) {
				toast.error('Selected interface has no usable IPv6 address', {
					position: 'bottom-center'
				});
				return null;
			}
			if (
				startIp !== '' &&
				!usable.some((address) =>
					isValidIPv6Range(startIp, endIp, address.ip, address.prefixLength)
				)
			) {
				toast.error('IP range is invalid or is not in the switch subnet', {
					position: 'bottom-center'
				});
				return null;
			}
		}

		if (!properties.expiry.trim()) {
			toast.error('Expiry is required', { position: 'bottom-center' });
			return null;
		}
		let expiry: number;
		try {
			expiry = dnsmasqToSeconds(properties.expiry);
		} catch {
			toast.error('Invalid expiry format', { position: 'bottom-center' });
			return null;
		}
		if (!Number.isSafeInteger(expiry) || expiry < 0 || expiry > 0xffffffff) {
			toast.error('Expiry is outside the supported range', { position: 'bottom-center' });
			return null;
		}

		return {
			type,
			startIp,
			endIp,
			expiry,
			raOnly: type === 'ipv6' && properties.raOnly,
			slaac: type === 'ipv6' && properties.slaac,
			standardSwitchId: selection.kind === 'stan' ? selection.id : undefined,
			manualSwitchId: selection.kind === 'man' ? selection.id : undefined
		};
	}

	async function submit() {
		if (saving) return;
		const submission = validateForm();
		if (!submission) return;

		saving = true;
		try {
			const result = selectedRange
				? await updateDHCPRange(
						selectedRange.type,
						selectedRange.id,
						submission.startIp,
						submission.endIp,
						submission.expiry,
						submission.raOnly,
						submission.slaac,
						submission.standardSwitchId,
						submission.manualSwitchId
					)
				: await createDHCPRange(
						submission.type,
						submission.startIp,
						submission.endIp,
						submission.expiry,
						submission.raOnly,
						submission.slaac,
						submission.standardSwitchId,
						submission.manualSwitchId
					);

			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error(selectedRange ? 'Error updating DHCP range' : 'Error creating DHCP range', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success(selectedRange ? 'Updated DHCP range' : 'Created DHCP range', {
				position: 'bottom-center'
			});
			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}

	function resetForm() {
		if (!saving) properties = createProperties();
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={resetForm}
		onClose={() => {
			if (!saving) open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[memory--range]"
					size="h-6 w-6"
					gap="gap-2"
					title={selectedRange ? 'Edit DHCP Range' : 'Create DHCP Range'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		{#if !selectedRange}
			<CustomComboBox
				bind:open={properties.ipType.combobox.open}
				label="IP Type"
				bind:value={properties.ipType.combobox.value}
				data={properties.ipType.combobox.options}
				onValueChange={handleIPTypeChange}
				disabled={saving}
				classes="flex-1 space-y-1"
				placeholder="Select IP Type"
				triggerWidth="w-full"
				width="w-full"
			/>
		{/if}

		<div class="flex flex-row gap-2">
			<CustomValueInput
				label="Start IP"
				bind:value={properties.startIp}
				placeholder={properties.ipType.combobox.value === 'ipv4'
					? '192.168.1.50'
					: 'fd00:cafe:babe::50'}
				disabled={saving}
				classes="flex-1 space-y-1.5"
			/>

			<CustomValueInput
				label="End IP"
				bind:value={properties.endIp}
				placeholder={properties.ipType.combobox.value === 'ipv4'
					? '192.168.1.150'
					: 'fd00:cafe:babe::150'}
				disabled={saving}
				classes="flex-1 space-y-1.5"
			/>
		</div>

		<div class="flex flex-row items-end gap-2">
			<CustomComboBox
				bind:open={properties.switchId.combobox.open}
				label="Switch"
				bind:value={properties.switchId.combobox.value}
				data={switchOptions}
				disabled={saving}
				classes="flex-1 space-y-1"
				placeholder="Select Switch"
				triggerWidth="w-full"
				width="w-full lg:w-[75%]"
			/>

			<CustomValueInput
				label="Expiry"
				bind:value={properties.expiry}
				placeholder="Expiry"
				disabled={saving}
				classes="flex-1 space-y-1.5"
			/>
		</div>

		{#if properties.ipType.combobox.value === 'ipv6'}
			<div class="mt-2 flex flex-row gap-2">
				<CustomCheckbox
					label="RA Only"
					bind:checked={properties.raOnly}
					disabled={saving}
					classes="flex items-center gap-2"
				/>

				<CustomCheckbox
					label="SLAAC"
					bind:checked={properties.slaac}
					disabled={saving}
					classes="flex items-center gap-2"
				/>
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={submit} type="button" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						{selectedRange ? 'Saving...' : 'Creating...'}
					{:else}
						{selectedRange ? 'Save' : 'Create'}
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
