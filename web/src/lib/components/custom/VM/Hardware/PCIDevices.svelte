<script lang="ts">
	import { modifyPPT } from '$lib/api/vm/hardware';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { getPCIDeviceId } from '$lib/utils/system/pci';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { DomainState } from '$lib/types/vm/vm';
	import { SvelteMap } from 'svelte/reactivity';

	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		node: string;
		vm: VM | null;
		pciDevices: PCIDevice[];
		pptDevices: PPTDevice[];
		vms: VM[];
		reload: boolean;
	}

	let {
		open = $bindable(),
		node,
		vm,
		pciDevices,
		pptDevices,
		vms = [],
		reload = $bindable(false)
	}: Props = $props();

	let selectedPptIds = $state<string[]>(initialSelected());
	let searchQuery = $state('');
	let saving = $state(false);

	type DeviceEntry = { device: PCIDevice | null; pptId: number };

	let passableDevices = $derived.by(() => {
		const passable: DeviceEntry[] = [];

		for (const mapping of pptDevices) {
			if (mapping.domain !== 0) continue;

			const parts = mapping.deviceID.split('/');
			const bus = Number(parts[0]);
			const device = Number(parts[1]);
			const fn = Number(parts[2]);
			if (!Number.isFinite(bus) || !Number.isFinite(device) || !Number.isFinite(fn)) continue;

			const match = pciDevices.find(
				(candidate) =>
					candidate.name.startsWith('ppt') &&
					candidate.domain === mapping.domain &&
					candidate.bus === bus &&
					candidate.device === device &&
					candidate.function === fn
			);

			if (match) {
				passable.push({ device: match, pptId: mapping.id });
			}
		}

		return passable;
	});

	// Devices assigned to this VM that are no longer present on the node don't
	// appear in `passableDevices`. Include them anyway (with `device: null`) so
	// they can still be deselected and removed instead of being silently stuck.
	let deviceEntries = $derived.by(() => {
		const entries = [...passableDevices];
		const presentIds = new Set(entries.map((entry) => String(entry.pptId)));

		for (const id of selectedPptIds) {
			if (!presentIds.has(id)) {
				entries.push({ device: null, pptId: Number(id) });
			}
		}

		return entries;
	});

	let pptIdToVMs = $derived.by(() => {
		const byPptId = new SvelteMap<number, VM[]>();

		for (const candidate of vms) {
			if (!Array.isArray(candidate.pciDevices)) continue;
			for (const pptId of candidate.pciDevices) {
				const list = byPptId.get(pptId);
				if (list) {
					list.push(candidate);
				} else {
					byPptId.set(pptId, [candidate]);
				}
			}
		}

		return byPptId;
	});

	let filteredDevices = $derived.by(() => {
		const q = searchQuery.trim().toLowerCase();
		if (!q) return deviceEntries;

		return deviceEntries.filter(({ device, pptId }) => {
			if (!device) {
				return String(pptId).includes(q) || 'no longer present'.includes(q);
			}

			const haystack =
				`${device.names.vendor} ${device.names.device} ${device.names.class} ${getPCIDeviceId(device)}`.toLowerCase();
			return haystack.includes(q);
		});
	});

	let vmPptIds = $derived.by(() => new Set((vm?.pciDevices ?? []).map(String)));

	function initialSelected(): string[] {
		return vm?.pciDevices?.map((device) => device.toString()) || [];
	}

	function reset() {
		selectedPptIds = initialSelected();
		searchQuery = '';
	}

	function toggle(id: string, on: boolean) {
		selectedPptIds = on ? [...selectedPptIds, id] : selectedPptIds.filter((x) => x !== id);
	}

	function vmStateLabel(state: number): string {
		return state === DomainState.DomainRunning ? 'Running' : 'Stopped';
	}

	async function modify() {
		if (vm && !saving) {
			saving = true;
			try {
				const response = await modifyPPT(
					vm.rid,
					selectedPptIds.map((id) => Number(id)),
					{ hostname: node }
				);

				if (response.status !== 'success') {
					handleAPIError(response);
					toast.error('Failed to modify PCI devices', {
						position: 'bottom-center'
					});
					return;
				}

				reload = true;
				toast.success('PCI devices modified', {
					position: 'bottom-center'
				});
				open = false;
			} finally {
				saving = false;
			}
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-full min-w-0 p-5"
		showResetButton={true}
		onReset={() => {
			reset();
		}}
		onClose={() => {
			reset();
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--video-input-hdmi]"
					size="h-5 w-5"
					gap="gap-2"
					title="PCI Devices"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="relative pb-0">
			<input
				type="text"
				placeholder="Search devices…"
				bind:value={searchQuery}
				class="bg-muted/50 placeholder:text-muted-foreground h-8 w-full min-w-0 rounded-md border py-0 pr-8 pl-2 text-sm outline-none"
				disabled={saving}
			/>
			{#if searchQuery}
				<button
					type="button"
					aria-label="Clear search"
					onclick={() => (searchQuery = '')}
					class="text-muted-foreground hover:bg-muted absolute top-1/2 right-1 flex size-5 -translate-y-1/2 items-center justify-center rounded"
				>
					<span class="icon-[mdi--close] h-3.5 w-3.5"></span>
				</button>
			{/if}
		</div>

		<div class="border">
			<ScrollArea orientation="vertical" class="h-64 w-full">
				<div class="space-y-3 p-3">
					{#if filteredDevices.length === 0}
						<p class="text-muted-foreground py-6 text-center text-sm">
							{deviceEntries.length === 0
								? 'No PCI passthrough devices available on this node.'
								: 'No devices match your search.'}
						</p>
					{:else}
						{#each filteredDevices as { device, pptId } (pptId)}
							{@const pptIdStr = String(pptId)}
							{@const usedBy = pptIdToVMs.get(pptId) ?? []}
							{@const usedByRunning = usedBy.filter(
								(candidate) =>
									candidate.rid !== vm?.rid && candidate.state === DomainState.DomainRunning
							)}
							{@const usedByStopped = usedBy.filter(
								(candidate) =>
									candidate.rid !== vm?.rid && candidate.state !== DomainState.DomainRunning
							)}
							<div
								class="border-muted-foreground/20 flex items-start gap-3 rounded-md border p-3 {vmPptIds.has(
									pptIdStr
								)
									? 'border-primary/50 bg-primary/5'
									: ''}"
							>
								<Checkbox
									id={`ppt-${pptId}`}
									checked={selectedPptIds.includes(pptIdStr)}
									onCheckedChange={(checked: boolean | 'indeterminate') => {
										if (typeof checked === 'boolean') toggle(pptIdStr, checked);
									}}
								/>
								<div class="grid min-w-0 flex-1 gap-1 leading-none">
									{#if device}
										<Label
											for={`ppt-${pptId}`}
											class="cursor-pointer text-sm leading-4 font-medium"
										>
											{device.names.device || 'Unknown Device'}
										</Label>
										<p class="text-muted-foreground font-mono text-xs">
											{getPCIDeviceId(device)}
										</p>
										<p class="text-muted-foreground truncate text-xs">
											{device.names.vendor || 'Unknown Vendor'}
											{#if device.names.class}
												· {device.names.class}
											{/if}
										</p>
									{:else}
										<Label
											for={`ppt-${pptId}`}
											class="cursor-pointer text-sm leading-4 font-medium"
										>
											Unavailable PCI device
										</Label>
										<p class="text-muted-foreground font-mono text-xs">ppt {pptId}</p>
										<p class="mt-1 flex items-center gap-1 text-xs font-medium text-amber-500">
											<span class="icon-[mdi--alert-circle-outline] h-3.5 w-3.5 shrink-0"></span>
											<span class="truncate">
												No longer present on this node. Uncheck to remove it.
											</span>
										</p>
									{/if}
									{#if usedByRunning.length > 0}
										<p class="mt-1 flex items-center gap-1 text-xs font-medium text-red-500">
											<span class="icon-[mdi--alert-circle] h-3.5 w-3.5 shrink-0"></span>
											<span class="truncate">
												In use by {usedByRunning.map((candidate) => candidate.name).join(', ')}
											</span>
										</p>
									{:else if usedByStopped.length > 0}
										<p class="text-muted-foreground mt-1 flex items-center gap-1 text-xs">
											<span class="icon-[mdi--information-outline] h-3.5 w-3.5 shrink-0"></span>
											<span class="truncate">
												Assigned to {usedByStopped.map((candidate) => candidate.name).join(', ')}
												({vmStateLabel(usedByStopped[0].state)})
											</span>
										</p>
									{/if}
								</div>
							</div>
						{/each}
					{/if}
				</div>
			</ScrollArea>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-between gap-2">
				<span class="text-muted-foreground text-xs">
					{selectedPptIds.length} selected
				</span>
				<Button onclick={modify} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
