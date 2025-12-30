<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { CPUPin, VM } from '$lib/types/vm/vm';
	import { toast } from 'svelte-sonner';
	import Label from '$lib/components/ui/label/label.svelte';
	import Progress from '$lib/components/ui/progress/progress.svelte';
	import { resource, watch, watchOnce } from 'runed';
	import { plural } from '$lib/utils';
	import { getCPUInfo } from '$lib/api/info/cpu';

	interface Props {
		open: boolean;
		vm?: VM | null;
		vms: VM[];
		pinnedCPUs: CPUPin[];
		coreSelectionLimit?: number;
	}

	interface Core {
		id: string;
		number: number;
		status: 'available' | 'busy' | 'inUse';
	}

	interface SocketData {
		id: string | number;
		name: string;
		model: string | number;
		cores: Core[];
	}

	let {
		open = $bindable(),
		vm,
		vms,
		pinnedCPUs = $bindable(),
		coreSelectionLimit
	}: Props = $props();

	let selectedSocket = $state<string | null>(null);
	let selectedCores = $state<Set<string>>(new Set());
	let step = $state<'socket' | 'cores'>('socket');
	let allSelections = $state<Map<number, number[]>>(new Map());
	let selectedSocketData = $state<SocketData | undefined>(undefined);
	let currentVmId = $state<number | null>(vm?.id ?? null);
	let initialPinnedCount = $derived.by(() => {
		if (pinnedCPUs && pinnedCPUs.length > 0) {
			return pinnedCPUs.reduce((total, pin) => total + pin.cores.length, 0);
		}

		if (vm?.cpuPinning && vm.cpuPinning.length > 0) {
			return vm.cpuPinning.reduce((total, pin) => total + pin.hostCpu.length, 0);
		}
		return 0;
	});

	let lastConfirmedPinnedCount = $state<number>(initialPinnedCount);
	let hasPinnedOverride = $state<boolean>(false);

	let cpuInfo = resource(
		() => 'cpu-info',
		async () => {
			const result = await getCPUInfo('current');
			return result;
		}
	);

	watch(
		() => hasPinnedOverride,
		(newHasPinnedOverride) => {
			if (!newHasPinnedOverride) {
				lastConfirmedPinnedCount = initialPinnedCount;
			}
		}
	);

	watch(
		() => vm?.id ?? null,
		(newVmId) => {
			if (newVmId !== currentVmId) {
				currentVmId = newVmId;
				hasPinnedOverride = false;
			}
		}
	);

	let displayPinnedCount = $derived.by(() =>
		hasPinnedOverride ? lastConfirmedPinnedCount : initialPinnedCount
	);

	let usedPins = $derived.by(() => {
		const pins: { vmId: number; hostSocket: number; hostCpu: number[] }[] = [];
		for (const vmItem of vms) {
			if (vmItem.cpuPinning) {
				for (const pin of vmItem.cpuPinning) {
					pins.push({
						vmId: vmItem.id,
						hostSocket: pin.hostSocket,
						hostCpu: pin.hostCpu
					});
				}
			}
		}
		return pins;
	});

	watch([() => allSelections.size, () => pinnedCPUs.length], ([selectedSize], [pinnedSize]) => {
		if (selectedSize === 0) return;
		if (pinnedSize && pinnedSize > 0) {
			for (const pin of pinnedCPUs) {
				allSelections.set(pin.socket, pin.cores);
			}
		}
	});

	watchOnce(
		() => vm?.cpuPinning,
		(cpuPinning) => {
			if (cpuPinning) {
				for (const pin of cpuPinning) {
					allSelections.set(pin.hostSocket, pin.hostCpu);
				}
			}
		}
	);

	let sockets = $derived.by<SocketData[]>(() => {
		if (!cpuInfo || !cpuInfo.current) return [];
		const currentVmId = vm ? vm.id : null;

		return Array.from({ length: cpuInfo.current.sockets }, (_, socketIndex) => {
			const coresPerSocket =
				cpuInfo.current?.logicalCores && cpuInfo.current.sockets
					? cpuInfo.current?.logicalCores / cpuInfo.current.sockets
					: 0;

			const otherPins = currentVmId ? usedPins.filter((pin) => pin.vmId !== currentVmId) : usedPins;
			const currentPins = currentVmId ? usedPins.filter((pin) => pin.vmId === currentVmId) : [];
			const cores: Core[] = Array.from({ length: coresPerSocket }, (_, coreIndex) => {
				const isCurrentPin = currentPins.some(
					(pin) => pin.hostSocket === socketIndex && pin.hostCpu.includes(coreIndex)
				);

				const isBusyByOther = otherPins.some(
					(pin) => pin.hostSocket === socketIndex && pin.hostCpu.includes(coreIndex)
				);

				let status: 'available' | 'busy' | 'inUse' = 'available';
				if (isBusyByOther) status = 'busy';
				else if (isCurrentPin && currentVmId) status = 'inUse';

				return {
					id: `${socketIndex}-core-${coreIndex}`,
					number: coreIndex,
					status
				};
			});

			return {
				id: socketIndex,
				name: cpuInfo.current?.name || `Socket ${socketIndex}`,
				model: `Socket ${socketIndex}`,
				cores
			};
		});
	});

	let availableCores = $derived(
		selectedSocketData?.cores.filter(
			(core) => core.status === 'available' || core.status === 'inUse'
		) || []
	);

	const handleSocketSelect = (socketId: string) => {
		selectedSocket = socketId;
		step = 'cores';

		const socketIndex = parseInt(socketId);
		const socketData = sockets.find((s) => Number(s.id) === socketIndex);
		if (!socketData) return;

		selectedSocketData = socketData;

		const currentVmId = vm ? vm.id : null;

		let currentCoreIds: string[] = [];
		if (currentVmId && vm?.cpuPinning) {
			const pinForSocket = vm.cpuPinning.find((p) => p.hostSocket === socketIndex);
			if (pinForSocket) {
				currentCoreIds = pinForSocket.hostCpu.map((c) => `${socketIndex}-core-${c}`);
			}
		}

		const savedSelection =
			allSelections.get(socketIndex)?.map((c) => `${socketIndex}-core-${c}`) || [];

		if (currentVmId && savedSelection.length === 0) {
			selectedCores = new Set(currentCoreIds);
		} else {
			selectedCores = new Set(savedSelection);
		}
	};

	const handleCoreToggle = (coreId: string) => {
		if (
			coreSelectionLimit !== undefined &&
			selectedCores.size >= coreSelectionLimit &&
			!selectedCores.has(coreId)
		) {
			toast.warning(`You can only select up to ${coreSelectionLimit} cores.`);
			return;
		}

		const newSelection = new Set(selectedCores);
		if (newSelection.has(coreId)) {
			newSelection.delete(coreId);
		} else {
			newSelection.add(coreId);
		}
		selectedCores = newSelection;
	};

	const persistSocketSelection = () => {
		if (!selectedSocket) return;

		const socketId = parseInt(selectedSocket);
		const coreIds = Array.from(selectedCores).map((coreId) => parseInt(coreId.split('-core-')[1]));
		const newSelections = new Map(allSelections);

		if (coreIds.length > 0) {
			newSelections.set(socketId, coreIds);
		} else {
			newSelections.delete(socketId);
		}

		allSelections = newSelections;
	};

	const handleBack = () => {
		if (selectedSocket) {
			persistSocketSelection();
		}

		step = 'socket';
		selectedSocket = null;
		selectedCores = new Set();
		selectedSocketData = undefined;
	};

	const handleCancel = () => {
		open = false;
		setTimeout(() => {
			step = 'socket';
			selectedSocket = null;
			selectedSocketData = undefined;
			selectedCores = new Set();
			allSelections = new Map();
		}, 200);
	};

	const handleClose = () => {
		open = false;
	};

	const handleConfirm = () => {
		if (step === 'cores' && selectedSocket) {
			persistSocketSelection();
		}

		pinnedCPUs = Array.from(allSelections.entries()).map(([socket, cores]) => ({
			socket,
			cores
		}));

		lastConfirmedPinnedCount = pinnedCPUs.reduce((sum, pin) => sum + pin.cores.length, 0);
		hasPinnedOverride = true;

		handleClose();
	};
</script>

<div>
	<Label class="mb-1.5 flex items-center justify-between">
		<span class="text-sm font-medium">CPU Pinning</span>
	</Label>
	<Button
		size="sm"
		variant="outline"
		class="flex h-9 w-full justify-start"
		onclick={() => (open = true)}
		disabled={cpuInfo.loading || !cpuInfo.current}
	>
		{#if cpuInfo.loading}
			<div class="w-full flex justify-center">
				<span class="icon icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
			</div>
		{:else if !cpuInfo.current}
			CPU Info Unavailable
		{:else}
			<div class="w-full flex gap-2 items-center">
				<span class="icon-[mdi--cpu-64-bit] h-4 w-4"></span>
				<span>Manage ({displayPinnedCount} pinned)</span>
			</div>
		{/if}
	</Button>
</div>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[iconoir--cpu] h-5 w-5"></span>
					CPU Pinning
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" onclick={handleClose} title="Close">
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if step === 'socket' && sockets.length > 0}
			<div class="mt-3 space-y-3">
				<div
					class="flex max-h-96 w-full flex-wrap items-center justify-center gap-4 overflow-auto p-1"
				>
					{#each sockets as socket (socket.id)}
						{@const availableCount = socket.cores.filter((c) => c.status === 'available').length}
						{@const busyCount = socket.cores.filter((c) => c.status === 'busy').length}
						{@const inUseCount = socket.cores.filter((c) => c.status === 'inUse').length}
						{@const hasSelection = allSelections.has(socket.id as number)}
						{@const selectedCount = hasSelection
							? allSelections.get(socket.id as number)?.length || 0
							: 0}
						{@const totalAvailable = availableCount + inUseCount}
						{@const actualAvailable = Math.max(0, totalAvailable - selectedCount)}
						{@const totalCores = socket.cores.length}
						{@const usedPinsCount = busyCount + selectedCount}
						{@const progressColor =
							usedPinsCount / totalCores > 0.75
								? 'bg-red-500'
								: usedPinsCount / totalCores > 0.5
									? 'bg-yellow-500'
									: 'bg-green-500'}

						<Card.Root
							class="hover:bg-accent/50 w-75 cursor-pointer transition-colors {hasSelection ||
							inUseCount > 0
								? 'ring-2 ring-yellow-500'
								: ''}"
							onclick={() => handleSocketSelect(socket.id.toString())}
						>
							<Card.Content class="px-6">
								<div class="flex items-start justify-between">
									<div class="flex items-center gap-3">
										<div class="bg-primary/10 rounded-lg p-2">
											<span class="icon-[iconoir--cpu] text-primary h-6 w-6"></span>
										</div>
										<div>
											<h3 class="font-medium text-sm">{socket.name}</h3>
											<p class="text-muted-foreground text-xs">
												{socket.model}
											</p>
										</div>
									</div>
								</div>

								<div class="mt-4 flex flex-wrap gap-2 text-xs">
									<div class="flex items-center gap-1">
										<div class="h-2 w-2 rounded-full bg-green-500"></div>
										<span>{actualAvailable} available</span>
									</div>
									<div class="flex items-center gap-1">
										<div class="h-2 w-2 rounded-full bg-red-500"></div>
										<span>{busyCount} busy</span>
									</div>
									{#if hasSelection || inUseCount > 0}
										<div class="flex items-center gap-1">
											<div class="h-2 w-2 rounded-full bg-yellow-500"></div>
											<span>
												{(selectedCount || 0) + (hasSelection ? 0 : inUseCount)} selected
											</span>
										</div>
									{/if}
								</div>

								<Progress
									value={(usedPinsCount / totalCores) * 100}
									max={100}
									class="mt-2"
									progressClass={progressColor}
								/>
							</Card.Content>
						</Card.Root>
					{/each}
				</div>
			</div>
		{/if}

		{#if step === 'cores' && selectedSocketData}
			<div class="space-y-4">
				<div class="flex items-center gap-2">
					<Button variant="outline" size="sm" class="px-3.5 py-2" onclick={handleBack}>
						<span class="icon-[material-symbols--arrow-back-ios-new-rounded] h-4 w-4"></span>
						Back to Sockets
					</Button>
				</div>

				<div class="space-y-3">
					<div class="flex flex-row gap-1 text-sm">
						<p>Selected: {selectedCores.size},</p>
						<p>Limit: {coreSelectionLimit}</p>
					</div>
				</div>

				<div class="grid max-h-64 grid-cols-6 gap-2 overflow-auto sm:grid-cols-8 md:grid-cols-10">
					{#each selectedSocketData.cores as core (core.id)}
						{@const isSelected = selectedCores.has(core.id)}
						{@const isAvailable = core.status === 'available' || core.status === 'inUse'}
						{@const isBusy = core.status === 'busy'}

						{@const disableSelect =
							!isAvailable ||
							(coreSelectionLimit !== undefined &&
								selectedCores.size >= coreSelectionLimit &&
								!isSelected)}

						<button
							onclick={() => isAvailable && handleCoreToggle(core.id)}
							class="
								relative flex flex-col items-center gap-1 rounded-lg border-2 p-3 transition-all duration-200
								{isAvailable
								? isSelected
									? 'border-yellow-600 bg-yellow-500/10 text-yellow-500'
									: 'border-border hover:border-primary/50 hover:bg-accent'
								: 'border-muted bg-muted/30 text-muted-foreground cursor-not-allowed'}
								{disableSelect && !isSelected ? 'cursor-not-allowed opacity-50' : ''}
							"
						>
							<span class="icon-[mynaui--zap] h-4 w-4 {!isAvailable ? 'opacity-50' : ''}"></span>
							<span class="text-xs">
								{core.number}
							</span>

							{#if isSelected}
								<div
									class="text-primary-foreground absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full bg-yellow-600"
								>
									<span class="icon-[material-symbols--check] h-2.5 w-2.5"></span>
								</div>
							{/if}

							{#if isBusy}
								<div
									class="absolute inset-0 flex items-center justify-center rounded-lg bg-red-500/20"
								>
									<div class="h-1.5 w-1.5 rounded-full bg-red-500"></div>
								</div>
							{/if}
						</button>
					{/each}
				</div>

				<div class="flex flex-wrap gap-2">
					<Button
						variant="outline"
						size="sm"
						onclick={() => {
							const max =
								coreSelectionLimit !== undefined ? coreSelectionLimit : availableCores.length;
							if (coreSelectionLimit !== undefined && availableCores.length > coreSelectionLimit) {
								toast.warning(`You can only select up to ${coreSelectionLimit} cores.`);
							}
							selectedCores = new Set(availableCores.map((core) => core.id).slice(0, max));
						}}
					>
						Select All Available
					</Button>
					<Button
						variant="outline"
						size="sm"
						onclick={() => {
							selectedCores = new Set();
						}}
					>
						Clear Selection
					</Button>
				</div>
			</div>
		{/if}

		<Dialog.Footer>
			<Button variant="outline" onclick={handleCancel}>Cancel</Button>
			{#if step === 'cores'}
				<Button variant="outline" onclick={handleBack}>Save &amp; Back to Sockets</Button>
			{/if}
			{#if step === 'socket'}
				{#if allSelections.size > 0}
					{@const totalCores = Array.from(allSelections.values()).reduce(
						(sum, cores) => sum + cores.length,
						0
					)}
					<Button onclick={handleConfirm}>
						{vm ? 'Update ' : 'Apply '}
						{totalCores}{' '}
						{plural(totalCores, ['Core', 'Core #'])}
						{' from '}
						{allSelections.size}{' '}
						{plural(allSelections.size, ['Socket', 'Socket #'])}
					</Button>
				{:else if pinnedCPUs.length > 0 || (vm?.cpuPinning && vm.cpuPinning.length > 0)}
					<Button onclick={handleConfirm} variant="destructive">Clear All Pinning</Button>
				{:else}
					<Button onclick={handleConfirm}>Apply Changes</Button>
				{/if}
			{/if}
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
