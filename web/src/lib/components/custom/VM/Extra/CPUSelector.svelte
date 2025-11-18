<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { CPUPin, VM } from '$lib/types/vm/vm';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		cpuInfo: CPUInfo;
		vms: VM[];
		pinnedCPUs: CPUPin[];
		coreSelectionLimit?: number;
	}

	interface Core {
		id: string;
		number: number;
		frequency?: string;
		status: 'available' | 'busy';
	}

	interface SocketData {
		id: string | number;
		name: string;
		model: string | number;
		cores: Core[];
	}

	let {
		open = $bindable(),
		cpuInfo = $bindable(),
		vms,
		pinnedCPUs = $bindable(),
		coreSelectionLimit
	}: Props = $props();

	function onConfirm(newPinnedCPUs: CPUPin[]) {
		pinnedCPUs = newPinnedCPUs;
	}

	$inspect(pinnedCPUs, 'pinnedCPUs');

	let selectedSocket = $state<string | null>(null);
	let selectedCores = $state<Set<string>>(new Set());
	let step = $state<'socket' | 'cores'>('socket');
	let allSelections = $state<Map<number, number[]>>(new Map());

	$effect(() => {
		if (open && pinnedCPUs.length > 0) {
			const newSelections = new Map<number, number[]>();
			pinnedCPUs.forEach((pin) => {
				newSelections.set(pin.socket, pin.cores);
			});
			allSelections = newSelections;
		} else if (!open) {
			allSelections = new Map();
			selectedSocket = null;
			selectedCores = new Set();
			step = 'socket';
		}
	});

	let usedPins = $derived.by(() => {
		const pins = [] as { vmId: number; hostSocket: number; hostCpu: number[] }[];
		for (const vm of vms) {
			if (vm.cpuPinning) {
				for (const pin of vm.cpuPinning) {
					pins.push({
						vmId: vm.id,
						hostSocket: pin.hostSocket,
						hostCpu: pin.hostCpu
					});
				}
			}
		}

		return pins;
	});

	const handleSocketSelect = (socketId: string) => {
		selectedSocket = socketId;
		step = 'cores';

		if (cpuInfo) {
			const socketIndex = parseInt(socketId);
			const coresPerSocket = cpuInfo.logicalCores / cpuInfo.sockets;
			const cores = Array.from({ length: coresPerSocket }, (_, coreIndex) => {
				const isPinned = usedPins.some(
					(pin) => pin.hostSocket === socketIndex && pin.hostCpu.includes(coreIndex)
				);

				return {
					id: `${socketIndex}-core-${coreIndex}`,
					number: coreIndex,
					frequency: '3.0 GHz',
					status: isPinned ? 'busy' : 'available'
				} as Core;
			});

			selectedSocketData = {
				id: socketId,
				name: `Socket ${socketIndex}`,
				model: cpuInfo.model.toString(),
				cores: cores
			};

			const existingSelection = allSelections.get(socketIndex);
			if (existingSelection) {
				selectedCores = new Set(
					existingSelection.map((coreIndex) => `${socketIndex}-core-${coreIndex}`)
				);
			} else {
				selectedCores = new Set();
			}
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

	const handleBack = () => {
		if (selectedSocket && selectedCores.size > 0) {
			const socketId = parseInt(selectedSocket);
			const coreIds = Array.from(selectedCores).map((coreId) => {
				return parseInt(coreId.split('-core-')[1]);
			});
			const newSelections = new Map(allSelections);
			newSelections.set(socketId, coreIds);
			allSelections = newSelections;
		} else if (selectedSocket) {
			const socketId = parseInt(selectedSocket);
			const newSelections = new Map(allSelections);
			newSelections.delete(socketId);
			allSelections = newSelections;
		}

		step = 'socket';
		selectedSocket = null;
		selectedCores = new Set();
	};

	const handleConfirm = () => {
		if (selectedSocket && selectedCores.size > 0) {
			const socketId = parseInt(selectedSocket);
			const coreIds = Array.from(selectedCores).map((coreId) => {
				return parseInt(coreId.split('-core-')[1]);
			});

			const newSelections = new Map(allSelections);
			if (coreIds.length > 0) {
				newSelections.set(socketId, coreIds);
			} else {
				newSelections.delete(socketId);
			}
			allSelections = newSelections;
		}

		// Always call onConfirm, even with empty selections
		const pinnedCPUs: CPUPin[] = Array.from(allSelections.entries()).map(
			([hostSocket, hostCpu]) => ({
				socket: hostSocket,
				cores: hostCpu
			})
		);

		onConfirm?.(pinnedCPUs);
		open = false;
	};

	const handleClose = () => {
		open = false;
	};

	let selectedSocketData = $state<SocketData | undefined>(undefined);
	let availableCores = $derived(
		selectedSocketData?.cores.filter((core) => core.status === 'available') || []
	);

	const sockets = $derived.by(() => {
		return Array.from({ length: cpuInfo.sockets }, (__, socketIndex) => {
			const coresPerSocket = cpuInfo.logicalCores / cpuInfo.sockets;
			const cores = Array.from({ length: coresPerSocket }, (_, coreIndex) => {
				const isPinned = usedPins.some(
					(pin) => pin.hostSocket === socketIndex && pin.hostCpu.includes(coreIndex)
				);

				return {
					id: `${socketIndex}-core-${coreIndex}`,
					number: coreIndex,
					frequency: '3.0 GHz',
					status: isPinned ? 'busy' : 'available'
				} as Core;
			});

			return {
				id: socketIndex,
				name: 'Socket ' + socketIndex,
				model: cpuInfo.name,
				cores: cores
			};
		});
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[iconoir--cpu] h-5 w-5"></span>

					CPU Pinning
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" onclick={handleClose} title={'Close'}>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if step === 'socket'}
			<div class="mt-3 space-y-3">
				<div
					class="flex max-h-96 w-full flex-wrap items-center justify-center gap-4 overflow-auto p-1"
				>
					{#each sockets as socket (socket.id)}
						{@const availableCount = socket.cores.filter((c) => c.status === 'available').length}
						{@const busyCount = socket.cores.filter((c) => c.status === 'busy').length}
						{@const hasSelection = allSelections.has(socket.id)}
						{@const selectedCount = hasSelection ? allSelections.get(socket.id)?.length || 0 : 0}

						<Card.Root
							class="hover:bg-accent/50 w-[300px] cursor-pointer transition-colors {hasSelection
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
											<p class="text-muted-foreground text-sm">
												{socket.model}
											</p>
										</div>
									</div>
								</div>

								<div class="mt-4 flex gap-4">
									<div class="flex items-center gap-1">
										<div class="h-2 w-2 rounded-full bg-green-500"></div>
										<span class="text-sm">
											{availableCount} available
										</span>
									</div>
									<div class="flex items-center gap-1">
										<div class="h-2 w-2 rounded-full bg-red-500"></div>
										<span class="text-sm">
											{busyCount} busy
										</span>
									</div>
									{#if hasSelection}
										<div class="flex items-center gap-1">
											<div class="h-2 w-2 rounded-full bg-yellow-500"></div>
											<span class="text-sm">
												{selectedCount} selected
											</span>
										</div>
									{/if}
								</div>

								<div class="mt-3">
									<div class="text-muted-foreground mb-1 text-xs">Core utilization</div>
									<div class="bg-muted h-2 w-full rounded-full">
										<div
											class="h-2 rounded-full bg-green-500"
											style="width: {(availableCount / socket.cores.length) * 100}%"
										></div>
									</div>
								</div>
							</Card.Content>
						</Card.Root>
					{/each}
				</div>
			</div>
		{/if}

		{#if step === 'cores' && selectedSocketData}
			<div class="space-y-4">
				<div class="flex items-center gap-2">
					<Button variant="outline" size="sm" class="p-0.5" onclick={handleBack}>
						<span class="icon-[material-symbols--arrow-back-ios-new-rounded] h-4 w-4"></span>

						Back to Sockets
					</Button>
				</div>

				<div class="space-y-2">
					<p class="text-muted-foreground">
						Select cores from {selectedSocketData.name} ({availableCores.length} available):
					</p>
					<p class="text-muted-foreground text-sm">
						Selected: {selectedCores.size} core{selectedCores.size !== 1 ? 's' : ''}
					</p>
					<p class="text-muted-foreground text-sm">
						Maximum selectable cores : {coreSelectionLimit}
					</p>
				</div>
				<div class="grid max-h-64 grid-cols-6 gap-2 overflow-auto sm:grid-cols-8 md:grid-cols-10">
					{#each selectedSocketData?.cores as core (core.id)}
						{@const isSelected = selectedCores.has(core.id)}
						{@const isAvailable = core.status === 'available'}
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

							{#if !isAvailable}
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
					<Button variant="outline" size="sm" onclick={() => (selectedCores = new Set())}>
						Clear Selection
					</Button>
				</div>
			</div>
		{/if}
		<Dialog.Footer>
			<Button variant="outline" onclick={handleClose}>Cancel</Button>
			{#if step === 'cores'}
				<Button variant="outline" onclick={handleBack}>Save & Back to Sockets</Button>
			{/if}
			{#if step === 'socket'}
				{#if allSelections.size > 0}
					{@const totalCores = Array.from(allSelections.values()).reduce(
						(sum, cores) => sum + cores.length,
						0
					)}
					<Button onclick={handleConfirm}>
						Confirm {totalCores} Core{totalCores !== 1 ? 's' : ''} from {allSelections.size} Socket{allSelections.size !==
						1
							? 's'
							: ''}
					</Button>
				{:else if pinnedCPUs.length > 0}
					<Button onclick={handleConfirm} variant="destructive">Clear All Pinning</Button>
				{/if}
			{/if}
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
