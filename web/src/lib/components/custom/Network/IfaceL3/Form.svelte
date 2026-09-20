<script lang="ts">
	import {
		deleteHostInterfaceL3,
		reapplyHostInterfaceL3,
		saveHostInterfaceL3,
		type HostInterfaceL3SavePayload
	} from '$lib/api/network/ifaceL3';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type {
		HostInterfaceL3Entry,
		HostInterfaceL3PendingEntry
	} from '$lib/types/network/ifaceL3';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { ipv4NetmaskToPrefix, parseIPv4Prefix, type IPv4Prefix } from '$lib/utils/inet';
	import { hostInterfaceL3Labels } from '$lib/utils/network/ifaceL3';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		interfaceName: string;
		currentMTU: number;
		entry: HostInterfaceL3Entry | null;
		liveIPv4?: { ip: string; netmask: string }[];
		interfaceMissing?: boolean;
		readOnlyReason?: string;
		onDone: (reloadForm?: boolean) => void | Promise<void>;
		onPending: (entry: HostInterfaceL3PendingEntry) => void;
	}

	let {
		open = $bindable(),
		interfaceName,
		currentMTU,
		entry,
		liveIPv4 = [],
		interfaceMissing = false,
		readOnlyReason = '',
		onDone,
		onPending
	}: Props = $props();

	let saving = $state(false);
	let removing = $state(false);
	let removeDialogOpen = $state(false);
	let validationError = $state('');
	let addresses = $state<string[]>([]);
	let mtu = $state('');
	let metric = $state('');
	let ipv6Mode = $state('inherit');
	let hasStaticIPv6 = $derived(addresses.some((address) => address.trim().includes(':')));
	let ipv4AddressCount = $derived(
		addresses.filter((address) => address.trim() !== '' && !address.includes(':')).length
	);
	let identityMismatch = $derived(
		entry?.conflicts.includes('host_interface_l3_identity_mismatch') ?? false
	);
	let vlanIdentityMismatch = $derived(
		entry?.conflicts.includes('host_interface_l3_vlan_identity_mismatch') ?? false
	);
	let saveBlockedByConflict = $derived(
		entry?.conflicts.some((conflict) => conflict !== 'host_interface_l3_prefix_owner_changed') ??
			false
	);
	let saveBlocked = $derived(interfaceMissing || saveBlockedByConflict);
	let reapplyBlocked = $derived(interfaceMissing || (entry?.conflicts.length ?? 0) > 0);
	let removalLeavesRuntime = $derived(interfaceMissing || identityMismatch || vlanIdentityMismatch);

	function ipv4PrefixGroup(index: number): number[] {
		const selected = parseIPv4Prefix(addresses[index] ?? '');
		if (!selected) return [];
		return addresses
			.map((address, candidateIndex) => ({ prefix: parseIPv4Prefix(address), candidateIndex }))
			.filter(({ prefix }) => prefix?.key === selected.key)
			.map(({ candidateIndex }) => candidateIndex);
	}

	function isIPv4PrefixOwner(index: number): boolean {
		const group = ipv4PrefixGroup(index);
		return group.length > 0 && group[0] === index;
	}

	function managedIPv4HostSet(): Set<string> {
		return new Set(
			(entry?.managedAddresses ?? [])
				.filter((address) => address.family === 'inet')
				.map((address) => address.address.split('/')[0])
		);
	}

	function hasForeignIPv4PrefixOwner(index: number): boolean {
		const selected = parseIPv4Prefix(addresses[index] ?? '');
		if (!selected) return false;
		const managedHosts = managedIPv4HostSet();
		return liveIPv4.some((address) => {
			const prefix = ipv4NetmaskToPrefix(address.netmask);
			if (prefix === null || managedHosts.has(address.ip)) return false;
			return parseIPv4Prefix(`${address.ip}/${prefix}`)?.key === selected.key;
		});
	}

	function makeIPv4PrefixOwner(index: number) {
		const group = ipv4PrefixGroup(index);
		if (group.length < 2 || group[0] === index) return;
		const next = [...addresses];
		const [selected] = next.splice(index, 1);
		next.splice(group[0], 0, selected);
		addresses = next;
	}

	let hasOverlappingIPv4Prefixes = $derived.by(() => {
		const prefixes = addresses
			.map(parseIPv4Prefix)
			.filter((prefix): prefix is IPv4Prefix => prefix !== null);
		const managedHosts = managedIPv4HostSet();
		const foreignPrefixes = liveIPv4
			.filter((address) => !managedHosts.has(address.ip))
			.map((address) => {
				const prefix = ipv4NetmaskToPrefix(address.netmask);
				return prefix === null ? null : parseIPv4Prefix(`${address.ip}/${prefix}`);
			})
			.filter((prefix): prefix is IPv4Prefix => prefix !== null);
		for (let left = 0; left < prefixes.length; left++) {
			for (let right = left + 1; right < prefixes.length; right++) {
				if (
					prefixes[left].key !== prefixes[right].key &&
					prefixes[left].start <= prefixes[right].end &&
					prefixes[right].start <= prefixes[left].end
				) {
					return true;
				}
			}
			for (const foreign of foreignPrefixes) {
				if (
					prefixes[left].key !== foreign.key &&
					prefixes[left].start <= foreign.end &&
					foreign.start <= prefixes[left].end
				) {
					return true;
				}
			}
		}
		return false;
	});
	let hasForeignIPv4Prefix = $derived(
		addresses.some((_address, index) => hasForeignIPv4PrefixOwner(index))
	);

	function resetForm() {
		addresses = entry
			? entry.addresses.map((address) => `${address.address}/${address.prefixLength}`)
			: [];
		mtu = entry?.mtu !== null && entry?.mtu !== undefined ? String(entry.mtu) : '';
		metric = entry?.metric !== null && entry?.metric !== undefined ? String(entry.metric) : '';
		ipv6Mode = entry?.ipv6Mode ?? 'inherit';
		validationError = '';
	}

	watch(
		() => [open, entry, interfaceName],
		() => {
			if (open) {
				resetForm();
			}
		}
	);

	const ipv6ModeOptions = [
		{ value: 'inherit', label: 'Leave as found' },
		{ value: 'enabled', label: 'Enabled' },
		{ value: 'disabled', label: 'Disabled' }
	];

	const selectClasses = {
		parent: 'min-w-0 space-y-1.5',
		label: 'flex h-7 w-full items-center text-sm',
		trigger: 'inline-flex h-9 w-full min-w-0 max-w-full items-center overflow-hidden px-3 text-left'
	};

	function parseOptionalNumber(value: string): number | null | 'invalid' {
		const trimmed = value.trim();
		if (trimmed === '') {
			return null;
		}
		const parsed = Number(trimmed);
		if (!Number.isFinite(parsed) || !Number.isInteger(parsed) || parsed < 0) {
			return 'invalid';
		}
		return parsed;
	}

	function buildPayload(): HostInterfaceL3SavePayload | null {
		const parsedMTU = parseOptionalNumber(mtu);
		const parsedMetric = parseOptionalNumber(metric);
		if (parsedMTU === 'invalid' || parsedMetric === 'invalid') {
			validationError = 'MTU and Metric must be whole numbers, or empty to restore the baseline.';
			return null;
		}
		if (parsedMTU !== null && (parsedMTU < 68 || parsedMTU > 65535)) {
			validationError = 'MTU must be between 68 and 65535.';
			return null;
		}
		if (parsedMetric !== null && parsedMetric > 255) {
			validationError = 'Metric must be between 0 and 255.';
			return null;
		}
		if ((hasStaticIPv6 || ipv6Mode === 'enabled') && parsedMTU !== null && parsedMTU < 1280) {
			validationError = 'MTU must be at least 1280 when IPv6 is enabled or configured.';
			return null;
		}
		if (hasStaticIPv6 && ipv6Mode === 'disabled') {
			validationError = 'Remove the static IPv6 addresses before disabling IPv6.';
			return null;
		}

		const normalizedAddresses = addresses
			.map((address) => address.trim())
			.filter((address) => address.length > 0)
			.map((address) => ({ address }));

		const payload: HostInterfaceL3SavePayload = {
			ipv6Mode: hasStaticIPv6 && ipv6Mode === 'inherit' ? 'enabled' : ipv6Mode,
			mtu: parsedMTU,
			metric: parsedMetric,
			addresses: normalizedAddresses,
			expectedRevision: entry?.revision ?? 0
		};
		return payload;
	}

	function isRevisionMismatch(response: { message?: string; error?: string | string[] }): boolean {
		return (
			response.message === 'host_interface_l3_revision_mismatch' ||
			response.error === 'host_interface_l3_revision_mismatch' ||
			(Array.isArray(response.error) &&
				response.error.includes('host_interface_l3_revision_mismatch'))
		);
	}

	async function save() {
		validationError = '';
		const payload = buildPayload();
		if (!payload) {
			return;
		}

		saving = true;
		try {
			const response = await saveHostInterfaceL3(interfaceName, payload);
			if (isAPIResponse(response)) {
				handleAPIError(response);
				await onDone(isRevisionMismatch(response));
				return;
			}
			open = false;
			onPending(response);
		} finally {
			saving = false;
		}
	}

	async function performRemove() {
		if (!entry) {
			return;
		}
		removing = true;
		try {
			const response = await deleteHostInterfaceL3(interfaceName, entry.revision);
			if (isAPIResponse(response)) {
				handleAPIError(response);
				await onDone(isRevisionMismatch(response));
				return;
			}
			removeDialogOpen = false;
			open = false;
			onPending(response);
		} finally {
			removing = false;
		}
	}

	async function reapply() {
		saving = true;
		try {
			const response = await reapplyHostInterfaceL3(interfaceName);
			if (isAPIResponse(response) && response.status !== 'success') {
				handleAPIError(response);
				return;
			}
			toast.success(`${interfaceName} reapplied`, { position: 'bottom-center' });
			open = false;
			await onDone();
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] overflow-hidden p-5 lg:max-w-2xl md:max-w-xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={resetForm}
		onClose={() => {
			if (!saving) open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		aria-busy={saving}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--ip]"
					size="h-5 w-5"
					gap="gap-2"
					title={`Host IP — ${interfaceName}`}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<fieldset disabled={saving || removing || saveBlocked || !!readOnlyReason} class="contents">
			<ScrollArea orientation="vertical" class="max-h-[70vh] pr-2">
				<div class="space-y-4">
					{#if readOnlyReason}
						<p
							class="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-600 dark:text-amber-400"
						>
							{readOnlyReason}
						</p>
					{/if}
					{#if interfaceMissing}
						<p
							class="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-600 dark:text-amber-400"
						>
							This interface is missing from the system. Sylve keeps the configuration so it can be
							removed; saving is disabled until the interface returns.
						</p>
					{/if}
					{#if entry && entry.conflicts.length > 0}
						<ul
							class="space-y-1 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-600 dark:text-amber-400"
						>
							{#each hostInterfaceL3Labels(entry.conflicts) as conflict (conflict)}
								<li>{conflict}</li>
							{/each}
						</ul>
					{/if}
					{#if validationError}
						<p class="text-xs text-destructive">{validationError}</p>
					{/if}
					<div class="space-y-2">
						<p class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
							Addresses
						</p>
						{#each addresses as _address, index (index)}
							<div class="flex items-end gap-2">
								<CustomValueInput
									label=""
									placeholder="10.0.0.5/24 or 2001:db8::5/64"
									bind:value={addresses[index]}
									classes="flex-1 space-y-1.5"
								/>
								{#if hasForeignIPv4PrefixOwner(index)}
									{#if isIPv4PrefixOwner(index)}
										<Button size="sm" variant="outline" disabled>Foreign prefix</Button>
									{/if}
								{:else if ipv4PrefixGroup(index).length > 1}
									<Button
										size="sm"
										variant="outline"
										onclick={() => makeIPv4PrefixOwner(index)}
										disabled={isIPv4PrefixOwner(index)}
									>
										{isIPv4PrefixOwner(index) ? 'Prefix owner' : 'Make owner'}
									</Button>
								{/if}
								<Button
									size="sm"
									variant="outline"
									onclick={() => (addresses = addresses.filter((_, i) => i !== index))}
								>
									<span class="icon-[mdi--close] h-4 w-4"></span>
								</Button>
							</div>
						{/each}
						<Button size="sm" variant="outline" onclick={() => (addresses = [...addresses, ''])}>
							<span class="icon-[mdi--plus] mr-2 h-4 w-4"></span>
							Add address
						</Button>
						{#if ipv4AddressCount > 1}
							<p class="text-xs text-amber-600 dark:text-amber-400">
								One IPv4 address in each subnet owns its connected prefix; additional addresses use
								/32 aliases. Changing the owner changes connected-route masks and is protected by
								the confirmation window.
							</p>
						{/if}
						{#if hasOverlappingIPv4Prefixes}
							<p class="text-xs text-amber-600 dark:text-amber-400">
								These IPv4 CIDRs create overlapping connected prefixes. Verify the intended routing
								before confirming the change.
							</p>
						{/if}
						{#if hasForeignIPv4Prefix}
							<p class="text-xs text-muted-foreground">
								A live foreign address owns at least one connected prefix. Sylve addresses in that
								prefix will use /32 aliases.
							</p>
						{/if}
					</div>

					<div class="grid gap-4 md:grid-cols-2">
						<CustomValueInput
							label="MTU"
							type="number"
							placeholder={String(currentMTU)}
							hint={entry
								? `Current: ${currentMTU}; empty restores ${entry.mtuBaseline ?? currentMTU}`
								: `Current: ${currentMTU}`}
							bind:value={mtu}
						/>
						<CustomValueInput
							label="Metric"
							type="number"
							placeholder="Not managed"
							hint={entry
								? `Empty restores ${entry.metricBaseline ?? 'the adoption baseline'}`
								: undefined}
							bind:value={metric}
						/>
					</div>

					<div>
						<SimpleSelect
							label="IPv6"
							options={ipv6ModeOptions}
							bind:value={ipv6Mode}
							classes={selectClasses}
							onChange={(value) => (ipv6Mode = String(value))}
						/>
					</div>
					{#if hasStaticIPv6 && ipv6Mode === 'inherit'}
						<p class="text-xs text-muted-foreground">
							Static IPv6 requires IPv6 to be enabled. Saving will track it as Enabled and restore
							the current ND6 settings when Host IP is removed.
						</p>
					{/if}

					<p class="text-xs text-muted-foreground">
						No default route is managed here; off-subnet traffic keeps using the existing default.
					</p>
					<p class="text-xs text-muted-foreground">
						Sylve changes runtime state only and does not edit rc.conf. Boot configuration applies
						first and may provide additional effective addresses or settings shown in the table.
					</p>
				</div>
			</ScrollArea>
		</fieldset>

		<Dialog.Footer>
			<div class="flex w-full items-center justify-between gap-2">
				<div>
					{#if entry}
						<div class="flex items-center gap-2">
							<Button
								size="sm"
								variant="outline"
								onclick={reapply}
								disabled={saving || reapplyBlocked || !!readOnlyReason}
							>
								Reapply
							</Button>
							<Button
								size="sm"
								variant="destructive"
								onclick={() => (removeDialogOpen = true)}
								disabled={saving || removing || !!readOnlyReason}
							>
								Remove Host IP
							</Button>
						</div>
					{/if}
				</div>
				<div class="flex items-center gap-2">
					<Button size="sm" variant="outline" onclick={() => (open = false)} disabled={saving}>
						Cancel
					</Button>
					<Button size="sm" onclick={save} disabled={saving || saveBlocked || !!readOnlyReason}>
						{#if saving}
							<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
							Saving...
						{:else}
							Save
						{/if}
					</Button>
				</div>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={removeDialogOpen}
	customTitle={removalLeavesRuntime
		? `Remove Host IP configuration from <span class="font-semibold">${interfaceName}</span>? The interface is missing or its MAC or VLAN identity changed, so only Sylve's configuration will be removed; runtime state will not be touched.`
		: `Remove Host IP configuration from <span class="font-semibold">${interfaceName}</span>? The confirmation window still applies, so you can undo within 60 seconds.`}
	confirmLabel="Remove"
	loadingLabel="Removing..."
	loading={removing}
	actions={{
		onConfirm: performRemove,
		onCancel: () => (removeDialogOpen = false)
	}}
/>
