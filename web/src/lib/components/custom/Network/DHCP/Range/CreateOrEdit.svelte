<script lang="ts">
	import { createDHCPRange, updateDHCPRange } from '$lib/api/network/dhcp';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { DHCPConfig, DHCPRange } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError } from '$lib/utils/http';
	import { generateSwitchOptions } from '$lib/utils/input';
	import { dnsmasqToSeconds, isValidDHCPRange, secondsToDnsmasq } from '$lib/utils/string';
	import Icon from '@iconify/svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		reload: boolean;
		networkInterfaces: Iface[];
		networkSwitches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
		selectedRange: DHCPRange | null;
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

	let currentSwId = $derived.by(() => {
		if (selectedRange) {
			console.log(selectedRange.standardSwitch, selectedRange.manualSwitch);
			if (selectedRange.standardSwitch) {
				const sw = networkSwitches.standard?.find((s) => s.id === selectedRange.standardSwitch?.id);
				if (sw) {
					return `${sw.id}-stan-${sw.name}`;
				}
			} else if (selectedRange.manualSwitch) {
				const sw = networkSwitches.manual?.find((s) => s.id === selectedRange.manualSwitch?.id);
				if (sw) {
					return `${sw.id}-man-${sw.name}`;
				}
			}
		}

		return '';
	});

	let usedSwitches = $derived.by(() => {
		if (!dhcpRanges || dhcpRanges.length === 0) {
			return [] as string[];
		}

		const used = [] as string[];
		for (const range of dhcpRanges) {
			if (range.standardSwitch) {
				const sw = networkSwitches.standard?.find((s) => s.id === range.standardSwitch?.id);
				if (sw) {
					used.push(`${sw.id}-stan-${sw.name}`);
				}
			} else if (range.manualSwitch) {
				const sw = networkSwitches.manual?.find((s) => s.id === range.manualSwitch?.id);
				if (sw) {
					used.push(`${sw.id}-man-${sw.name}`);
				}
			}
		}

		if (selectedRange) {
			// Remove the current switch from the used list to allow re-selection
			const indexStan = used.indexOf(
				`${selectedRange.standardSwitch?.id}-stan-${selectedRange.standardSwitch?.name}`
			);
			if (indexStan > -1) {
				used.splice(indexStan, 1);
			}
			const indexMan = used.indexOf(
				`${selectedRange.manualSwitch?.id}-man-${selectedRange.manualSwitch?.name}`
			);
			if (indexMan > -1) {
				used.splice(indexMan, 1);
			}
		}

		return used;
	});

	let options = $derived({
		startIp: selectedRange ? selectedRange.startIp : '',
		endIp: selectedRange ? selectedRange.endIp : '',
		switchId: {
			combobox: {
				open: false,
				value: selectedRange == null ? '' : currentSwId,
				options: generateSwitchOptions(networkSwitches).filter(
					(opt) => !usedSwitches.includes(opt.value) || opt.value === currentSwId
				)
			}
		},
		expiry: selectedRange ? secondsToDnsmasq(selectedRange.expiry, true) : '12h'
	});

	let properties = $state(options);

	$effect(() => {
		if (open && properties.expiry !== '') {
			try {
				dnsmasqToSeconds(properties.expiry);
			} catch (e) {
				properties.expiry = '12h';
				toast.error('Invalid expiry format, resetting to 12h', {
					position: 'bottom-center'
				});
			}
		}
	});

	async function create() {
		if (!isValidDHCPRange(properties.startIp, properties.endIp)) {
			toast.error('Invalid IP range', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.switchId.combobox.value) {
			toast.error('No switch selected', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.expiry || properties.expiry.trim() === '') {
			toast.error('Expiry is required', {
				position: 'bottom-center'
			});
			return;
		}

		let standardSwId = undefined as number | undefined;
		let manualSwId = undefined as number | undefined;

		if (properties.switchId.combobox.value) {
			const parts = properties.switchId.combobox.value.split('-');
			if (parts[1] === 'stan') {
				standardSwId = parseInt(parts[0]);
			} else if (parts[1] === 'man') {
				manualSwId = parseInt(parts[0]);
			}
		}

		const res = await createDHCPRange(
			properties.startIp,
			properties.endIp,
			dnsmasqToSeconds(properties.expiry),
			standardSwId,
			manualSwId
		);

		if (res.error) {
			handleAPIError(res);
			toast.error('Error creating DHCP range', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Created DHCP range', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}

	async function save() {
		if (!selectedRange) {
			toast.error('No DHCP range selected', {
				position: 'bottom-center'
			});
			return;
		}

		if (!isValidDHCPRange(properties.startIp, properties.endIp)) {
			toast.error('Invalid IP range', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.switchId.combobox.value) {
			toast.error('No switch selected', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.expiry || properties.expiry.trim() === '') {
			toast.error('Expiry is required', {
				position: 'bottom-center'
			});
			return;
		}

		let standardSwId = undefined as number | undefined;
		let manualSwId = undefined as number | undefined;

		if (properties.switchId.combobox.value) {
			const parts = properties.switchId.combobox.value.split('-');
			if (parts[1] === 'stan') {
				standardSwId = parseInt(parts[0]);
			} else if (parts[1] === 'man') {
				manualSwId = parseInt(parts[0]);
			}
		}

		const res = await updateDHCPRange(
			selectedRange!.id,
			properties.startIp,
			properties.endIp,
			dnsmasqToSeconds(properties.expiry),
			standardSwId,
			manualSwId
		);

		if (res.error) {
			handleAPIError(res);
			toast.error('Error updating DHCP range', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Updated DHCP range', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center">
						<Icon icon="memory:range" class="mr-2 h-6 w-6" />
						<span>{selectedRange ? 'Edit' : 'Create'} DHCP Range</span>
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<div class="flex items-center gap-0.5">
				<Button
					size="sm"
					variant="link"
					class="h-4"
					title={'Reset'}
					onclick={() => (properties = options)}
				>
					<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
					<span class="sr-only">{'Reset'}</span>
				</Button>
				<Button size="sm" variant="link" class="h-4" title={'Close'} onclick={() => (open = false)}>
					<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
					<span class="sr-only">{'Close'}</span>
				</Button>
			</div>
		</div>

		<div class="flex flex-row gap-2">
			<CustomValueInput
				label="Start IP"
				bind:value={properties.startIp}
				placeholder="192.168.1.50"
				classes="flex-1 space-y-1.5"
			/>

			<CustomValueInput
				label="End IP"
				bind:value={properties.endIp}
				placeholder="192.168.1.150"
				classes="flex-1 space-y-1.5"
			/>
		</div>

		<div class="flex flex-row gap-2">
			<CustomComboBox
				bind:open={properties.switchId.combobox.open}
				label="Switch"
				bind:value={properties.switchId.combobox.value}
				data={properties.switchId.combobox.options}
				classes="flex-1 space-y-1"
				placeholder="Select Switch"
				triggerWidth="w-full"
				width="w-full lg:w-[75%]"
			></CustomComboBox>

			<CustomValueInput
				label="Expiry"
				bind:value={properties.expiry}
				placeholder="Expiry"
				classes="flex-1 space-y-1.5"
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if !selectedRange}
					<Button onclick={create} type="submit" size="sm">Create</Button>
				{:else}
					<Button onclick={save} type="submit" size="sm">Save</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
