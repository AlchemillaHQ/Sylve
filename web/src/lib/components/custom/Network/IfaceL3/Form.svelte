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
	import { hostInterfaceL3Labels } from '$lib/utils/network/ifaceL3';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		interfaceName: string;
		currentMTU: number;
		entry: HostInterfaceL3Entry | null;
		interfaceMissing?: boolean;
		onDone: () => void | Promise<void>;
		onPending: (entry: HostInterfaceL3PendingEntry) => void;
	}

	let {
		open = $bindable(),
		interfaceName,
		currentMTU,
		entry,
		interfaceMissing = false,
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

	function resetForm() {
		addresses = entry
			? entry.addresses
					.slice()
					.sort((a, b) => a.ordering - b.ordering)
					.map((address) => `${address.address}/${address.prefixLength}`)
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

		const normalizedAddresses = addresses
			.map((address) => address.trim())
			.filter((address) => address.length > 0)
			.map((address) => ({ address }));

		const payload: HostInterfaceL3SavePayload = {
			ipv6Mode,
			mtu: parsedMTU,
			metric: parsedMetric,
			addresses: normalizedAddresses,
			expectedRevision: entry?.revision ?? 0
		};
		return payload;
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

		<fieldset disabled={saving || removing || interfaceMissing} class="contents">
			<ScrollArea orientation="vertical" class="max-h-[70vh] pr-2">
				<div class="space-y-4">
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
					</div>

					<div class="grid gap-4 md:grid-cols-2">
						<CustomValueInput
							label="MTU"
							type="number"
							placeholder={String(currentMTU)}
							hint={`Current: ${currentMTU}`}
							bind:value={mtu}
						/>
						<CustomValueInput
							label="Metric"
							type="number"
							placeholder="Not managed"
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

					<p class="text-xs text-muted-foreground">
						No default route is managed here; off-subnet traffic keeps using the existing default.
					</p>
				</div>
			</ScrollArea>
		</fieldset>

		<Dialog.Footer>
			<div class="flex w-full items-center justify-between gap-2">
				<div>
					{#if entry}
						<div class="flex items-center gap-2">
							<Button size="sm" variant="outline" onclick={reapply} disabled={saving}>
								Reapply
							</Button>
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
				<div class="flex items-center gap-2">
					<Button size="sm" variant="outline" onclick={() => (open = false)} disabled={saving}>
						Cancel
					</Button>
					<Button size="sm" onclick={save} disabled={saving || interfaceMissing}>
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
	customTitle={`Remove Host IP configuration from <span class="font-semibold">${interfaceName}</span>? The confirmation window still applies, so you can undo within 60 seconds.`}
	confirmLabel="Remove"
	loadingLabel="Removing..."
	loading={removing}
	actions={{
		onConfirm: performRemove,
		onCancel: () => (removeDialogOpen = false)
	}}
/>
