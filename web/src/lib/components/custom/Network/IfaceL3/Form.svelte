<script lang="ts">
	import {
		deleteHostInterfaceL3,
		getHostInterfaceL3,
		getHostInterfaceL3Pending,
		reapplyHostInterfaceL3,
		saveHostInterfaceL3,
		type HostInterfaceL3SavePayload
	} from '$lib/api/network/ifaceL3';
	import { getInterfaces } from '$lib/api/network/iface';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import { showErrorToast } from '$lib/stores/error-details.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Iface } from '$lib/types/network/iface';
	import type {
		HostInterfaceL3Entry,
		HostInterfaceL3PendingEntry,
		HostInterfaceL3Target
	} from '$lib/types/network/ifaceL3';
	import { isHostInterfaceL3List } from '$lib/types/network/ifaceL3';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { ipv4NetmaskToPrefix, parseIPv4Prefix, type IPv4Prefix } from '$lib/utils/inet';
	import {
		hostInterfaceL3Message,
		hostInterfaceL3Preflight,
		normalizeHostAddress,
		type HostInterfaceL3PreflightMode
	} from '$lib/utils/network/ifaceL3';
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
		onReviewPending?: () => void;
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
		onPending,
		onReviewPending
	}: Props = $props();

	const preflightToastID = 'host-interface-l3-preflight';
	const requestToastID = 'host-interface-l3-request';

	let saving = $state(false);
	let removing = $state(false);
	let removeDialogOpen = $state(false);
	let addresses = $state<string[]>([]);
	let mtu = $state('');
	let metric = $state('');
	let ipv6Mode = $state('inherit');
	let hasStaticIPv6 = $derived(addresses.some((address) => address.trim().includes(':')));
	let identityMismatch = $derived(
		entry?.conflicts.includes('host_interface_l3_identity_mismatch') ?? false
	);
	let vlanIdentityMismatch = $derived(
		entry?.conflicts.includes('host_interface_l3_vlan_identity_mismatch') ?? false
	);
	let removalLeavesRuntime = $derived(interfaceMissing || identityMismatch || vlanIdentityMismatch);
	let editable = $derived(!readOnlyReason && !interfaceMissing);

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
	let hasSharedIPv4Prefix = $derived(
		addresses.some((_address, index) => ipv4PrefixGroup(index).length > 1)
	);

	function resetForm() {
		const restored = entry
			? entry.addresses.map((address) => `${address.address}/${address.prefixLength}`)
			: [];
		addresses = restored.length > 0 ? restored : [''];
		mtu = entry?.mtu !== null && entry?.mtu !== undefined ? String(entry.mtu) : '';
		metric = entry?.metric !== null && entry?.metric !== undefined ? String(entry.metric) : '';
		ipv6Mode = entry?.ipv6Mode ?? 'inherit';
	}

	function removeAddress(index: number) {
		if (addresses.length === 1) {
			addresses = [''];
			return;
		}
		addresses = addresses.filter((_, candidateIndex) => candidateIndex !== index);
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

	function buildPayload(): { payload: HostInterfaceL3SavePayload } | { error: string } {
		const parsedMTU = parseOptionalNumber(mtu);
		const parsedMetric = parseOptionalNumber(metric);
		if (parsedMTU === 'invalid' || parsedMetric === 'invalid') {
			return { error: 'MTU and Metric must be whole numbers, or empty to restore the baseline.' };
		}
		if (parsedMTU !== null && (parsedMTU < 68 || parsedMTU > 65535)) {
			return { error: 'MTU must be between 68 and 65535.' };
		}
		if (parsedMetric !== null && parsedMetric > 255) {
			return { error: 'Metric must be between 0 and 255.' };
		}
		if ((hasStaticIPv6 || ipv6Mode === 'enabled') && parsedMTU !== null && parsedMTU < 1280) {
			return { error: 'MTU must be at least 1280 when IPv6 is enabled or configured.' };
		}
		if (hasStaticIPv6 && ipv6Mode === 'disabled') {
			return { error: 'Remove the static IPv6 addresses before disabling IPv6.' };
		}

		const normalizedAddresses = addresses
			.map((address) => address.trim())
			.filter((address) => address.length > 0)
			.map((address) => ({ address }));

		return {
			payload: {
				ipv6Mode: hasStaticIPv6 && ipv6Mode === 'inherit' ? 'enabled' : ipv6Mode,
				mtu: parsedMTU,
				metric: parsedMetric,
				addresses: normalizedAddresses,
				expectedRevision: entry?.revision ?? 0
			}
		};
	}

	type PreflightSnapshot = {
		row: HostInterfaceL3Entry | null;
		target: HostInterfaceL3Target | null;
		pendingCount: number;
		addressOwners: Record<string, string>;
	};

	function buildAddressOwners(
		rows: HostInterfaceL3Entry[],
		interfaces: Iface[]
	): Record<string, string> {
		const owners: Record<string, string> = {};
		const record = (value: string, owner: string) => {
			const normalized = normalizeHostAddress(value);
			if (normalized) {
				owners[`${normalized.family}|${normalized.host}`] = owner;
			}
		};

		for (const row of rows) {
			if (row.interface === interfaceName) {
				continue;
			}
			for (const address of row.addresses) {
				record(address.address, row.interface);
			}
		}
		for (const live of interfaces) {
			if (live.name === interfaceName) {
				continue;
			}
			for (const address of live.ipv4 ?? []) {
				record(address.ip, live.name);
			}
			for (const address of live.ipv6 ?? []) {
				record(address.ip, live.name);
			}
		}

		return owners;
	}

	async function fetchPreflightSnapshot(): Promise<PreflightSnapshot | null> {
		const [listResponse, pendingResponse, interfacesResponse] = await Promise.all([
			getHostInterfaceL3(),
			getHostInterfaceL3Pending(),
			getInterfaces()
		]);
		if (
			isAPIResponse(listResponse) ||
			isAPIResponse(pendingResponse) ||
			isAPIResponse(interfacesResponse) ||
			!isHostInterfaceL3List(listResponse)
		) {
			return null;
		}

		return {
			row: listResponse.rows.find((row) => row.interface === interfaceName) ?? null,
			target: listResponse.targets.find((target) => target.interface === interfaceName) ?? null,
			pendingCount: pendingResponse.filter((pending) => pending.interface === interfaceName).length,
			addressOwners: buildAddressOwners(listResponse.rows, interfacesResponse)
		};
	}

	function notifyBlocked(title: string, description?: string) {
		toast.error(title, {
			id: preflightToastID,
			description,
			duration: 3000,
			position: 'bottom-center'
		});
	}

	function notifyPending(count: number) {
		toast.info(
			count === 1
				? 'A change is awaiting confirmation'
				: `${count} changes are awaiting confirmation`,
			{
				id: preflightToastID,
				duration: 8000,
				position: 'bottom-center',
				action: onReviewPending
					? { label: 'Review', onClick: () => onReviewPending?.() }
					: undefined
			}
		);
	}

	function notifyRequestError(response: APIResponse) {
		const code = Array.isArray(response.error) ? response.error[0] : response.error;
		const message = hostInterfaceL3Message(String(code ?? '').trim());
		if (message) {
			showErrorToast(message, response, { id: requestToastID, position: 'bottom-center' });
			return;
		}
		handleAPIError(response);
	}

	async function preflightAllows(
		mode: HostInterfaceL3PreflightMode,
		requestedAddresses: string[]
	): Promise<boolean> {
		const snapshot = await fetchPreflightSnapshot();
		if (!snapshot) {
			return true;
		}

		const result = hostInterfaceL3Preflight({ mode, ...snapshot, requestedAddresses });
		if (result.status === 'pending') {
			notifyPending(result.count);
			await onDone(false);
			return false;
		}
		if (result.status === 'blocked') {
			notifyBlocked(result.title, result.description);
			await onDone(false);
			return false;
		}
		return true;
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
		const built = buildPayload();
		if ('error' in built) {
			notifyBlocked('Cannot save', built.error);
			return;
		}

		saving = true;
		try {
			const requestedAddresses = built.payload.addresses.map((address) => address.address);
			if (!(await preflightAllows('save', requestedAddresses))) {
				return;
			}

			const response = await saveHostInterfaceL3(interfaceName, built.payload);
			if (isAPIResponse(response)) {
				notifyRequestError(response);
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
			const snapshot = await fetchPreflightSnapshot();
			if (snapshot && snapshot.pendingCount > 0) {
				removeDialogOpen = false;
				notifyPending(snapshot.pendingCount);
				await onDone(false);
				return;
			}

			const response = await deleteHostInterfaceL3(interfaceName, entry.revision);
			if (isAPIResponse(response)) {
				notifyRequestError(response);
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
			if (!(await preflightAllows('reapply', []))) {
				return;
			}

			const response = await reapplyHostInterfaceL3(interfaceName);
			if (isAPIResponse(response) && response.status !== 'success') {
				notifyRequestError(response);
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
		showResetButton={!saving && editable}
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
					title={`Host IP - ${interfaceName}`}
				/>
			</Dialog.Title>
			<Dialog.Description class="text-xs">
				Manage static addresses, MTU and metric at runtime; rc.conf and the default route are left
				untouched.
			</Dialog.Description>
		</Dialog.Header>

		<fieldset disabled={saving || removing || !editable} class="contents">
			<ScrollArea orientation="vertical" class="max-h-[70vh] pr-2">
				<div class="space-y-4">
					{#if readOnlyReason}
						<p class="text-xs text-muted-foreground">View only. {readOnlyReason}</p>
					{:else if interfaceMissing}
						<p class="text-xs text-muted-foreground">
							Interface missing. The configuration is kept so it can still be removed.
						</p>
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
										<Button
											size="sm"
											class="h-9"
											variant="outline"
											disabled
											title="A live address outside Sylve owns this prefix, so Sylve addresses here use /32 aliases."
										>
											Foreign prefix
										</Button>
									{/if}
								{:else if ipv4PrefixGroup(index).length > 1}
									<Button
										size="sm"
										class="h-9"
										variant="outline"
										onclick={() => makeIPv4PrefixOwner(index)}
										disabled={isIPv4PrefixOwner(index)}
									>
										{isIPv4PrefixOwner(index) ? 'Prefix owner' : 'Make owner'}
									</Button>
								{/if}
								<Button
									size="icon"
									variant="outline"
									title="Remove address"
									onclick={() => removeAddress(index)}
								>
									<span class="icon-[mdi--close] h-4 w-4"></span>
								</Button>
							</div>
						{/each}
						<Button size="sm" variant="outline" onclick={() => (addresses = [...addresses, ''])}>
							<span class="icon-[mdi--plus] mr-2 h-4 w-4"></span>
							Add address
						</Button>
						{#if hasSharedIPv4Prefix}
							<p class="text-xs text-muted-foreground">
								One address per subnet owns the connected route; the rest become /32 aliases.
							</p>
						{/if}
						{#if hasOverlappingIPv4Prefixes}
							<p class="text-xs text-amber-600 dark:text-amber-400">
								Overlapping IPv4 subnets; verify the intended routing before saving.
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
							Saving enables IPv6 and restores the previous settings if Host IP is removed.
						</p>
					{/if}
				</div>
			</ScrollArea>
		</fieldset>

		<Dialog.Footer>
			<div class="flex w-full items-center justify-between gap-2">
				<div>
					{#if entry && !readOnlyReason}
						<div class="flex items-center gap-2">
							{#if !interfaceMissing}
								<Button size="sm" variant="outline" onclick={reapply} disabled={saving || removing}>
									Reapply
								</Button>
							{/if}
							<Button
								size="sm"
								variant="destructive"
								onclick={() => (removeDialogOpen = true)}
								disabled={saving || removing}
							>
								Remove Host IP
							</Button>
						</div>
					{/if}
				</div>
				{#if editable}
					<Button size="sm" onclick={save} disabled={saving || removing}>
						{#if saving}
							<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
							Saving...
						{:else}
							Save
						{/if}
					</Button>
				{/if}
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
