<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import Icon from '@iconify/svelte';

	interface CoreSelectorProps {
		open: boolean;
		onConfirm?: (selection: { socketId: string; coreIds: string[] }) => void;
	}

	const MOCK_SOCKETS = [
		{
			id: 'socket-0',
			name: 'Socket 0',
			model: 'Intel Xeon Gold 6248R',
			cores: Array.from({ length: 24 }, (_, i) => ({
				id: `socket-0-core-${i}`,
				number: i,
				frequency: '3.0 GHz',
				status: i % 8 === 7 ? 'busy' : 'available' // Some cores busy for realism
			}))
		},
		{
			id: 'socket-1',
			name: 'Socket 1',
			model: 'Intel Xeon Gold 6248R',
			cores: Array.from({ length: 24 }, (_, i) => ({
				id: `socket-1-core-${i}`,
				number: i,
				frequency: '3.0 GHz',
				status: i % 12 === 11 ? 'busy' : 'available'
			}))
		},
		{
			id: 'socket-2',
			name: 'Socket 2',
			model: 'Intel Xeon Gold 6248R',
			cores: Array.from({ length: 24 }, (_, i) => ({
				id: `socket-2-core-${i}`,
				number: i,
				frequency: '3.0 GHz',
				status: 'available'
			}))
		},
		{
			id: 'socket-3',
			name: 'Socket 3',
			model: 'Intel Xeon Gold 6248R',
			cores: Array.from({ length: 24 }, (_, i) => ({
				id: `socket-3-core-${i}`,
				number: i,
				frequency: '3.0 GHz',
				status: i % 6 === 5 ? 'busy' : 'available'
			}))
		}
	];

	let { open = $bindable(), onConfirm }: CoreSelectorProps = $props();

	let selectedSocket = $state<string | null>(null);
	let selectedCores = $state<Set<string>>(new Set());
	let step = $state<'socket' | 'cores'>('socket');

	const handleSocketSelect = (socketId: string) => {
		selectedSocket = socketId;
		selectedCores = new Set();
		step = 'cores';
	};

	const handleCoreToggle = (coreId: string) => {
		const newSelection = new Set(selectedCores);
		if (newSelection.has(coreId)) {
			newSelection.delete(coreId);
		} else {
			newSelection.add(coreId);
		}
		selectedCores = newSelection;
	};

	const handleBack = () => {
		step = 'socket';
		selectedSocket = null;
		selectedCores = new Set();
	};

	const handleConfirm = () => {
		if (selectedSocket && selectedCores.size > 0) {
			onConfirm?.({
				socketId: selectedSocket,
				coreIds: Array.from(selectedCores)
			});
			open = false;
			setTimeout(() => {
				step = 'socket';
				selectedSocket = null;
				selectedCores = new Set();
			}, 200);
		}
	};

	const handleClose = () => {
		open = false;
		setTimeout(() => {
			step = 'socket';
			selectedSocket = null;
			selectedCores = new Set();
		}, 200);
	};

	let selectedSocketData = $state<(typeof MOCK_SOCKETS)[0] | undefined>(undefined);

	$effect(() => {
		selectedSocketData = MOCK_SOCKETS.find((s) => s.id === selectedSocket);
	});

	let availableCores = $derived(
		selectedSocketData?.cores.filter((core) => core.status === 'available') || []
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<Icon icon="iconoir:cpu" class="h-5 w-5 " />
					CPU Core Selector
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" onclick={handleClose} title={'Close'}>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if step === 'socket'}
			<div class="space-y-4">
				<p class="text-muted-foreground">Select a CPU socket to allocate cores from:</p>

				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					{#each MOCK_SOCKETS as socket (socket.id)}
						{@const availableCount = socket.cores.filter((c) => c.status === 'available').length}
						{@const busyCount = socket.cores.filter((c) => c.status === 'busy').length}

						<Card.Root
							class="hover:bg-accent/50 cursor-pointer transition-colors"
							onclick={() => handleSocketSelect(socket.id)}
						>
							<Card.Content class="p-6">
								<div class="flex items-start justify-between">
									<div class="flex items-center gap-3">
										<div class="bg-primary/10 rounded-lg p-2">
											<Icon icon="iconoir:cpu" class="text-primary h-6 w-6" />
										</div>
										<div>
											<h3 class="font-medium">
												{socket.name}
											</h3>
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
					<Button variant="ghost" size="sm" onclick={handleBack}>
						<Icon icon="material-symbols:arrow-back-ios-new-rounded" class="h-4 w-4" />
						Back to Sockets
					</Button>
				</div>

				<div class="space-y-2">
					<p class="text-muted-foreground">
						Select cores from {selectedSocketData.name} (
						{availableCores.length} available):
					</p>
					<p class="text-muted-foreground text-sm">
						Selected: {selectedCores.size} cores
					</p>
				</div>

				<div class="grid grid-cols-6 gap-2 sm:grid-cols-8 md:grid-cols-12">
					{#each selectedSocketData.cores as core (core.id)}
						{@const isSelected = selectedCores.has(core.id)}
						{@const isAvailable = core.status === 'available'}

						<button
							disabled={!isAvailable}
							onclick={() => isAvailable && handleCoreToggle(core.id)}
							class="
        relative flex flex-col items-center gap-1 rounded-lg border-2 p-3 transition-all duration-200
        {isAvailable
								? isSelected
									? 'border-primary bg-primary/10 text-primary'
									: 'border-border hover:border-primary/50 hover:bg-accent'
								: 'border-muted bg-muted/30 text-muted-foreground cursor-not-allowed'}
      "
						>
							<Icon icon="mynaui:zap" class="h-4 w-4 {!isAvailable ? 'opacity-50' : ''}" />
							<span class="text-xs">
								{core.number}
							</span>

							{#if isSelected}
								<div
									class="bg-primary text-primary-foreground absolute -right-1 -top-1 flex h-4 w-4 items-center justify-center rounded-full"
								>
									<Icon icon="material-symbols:check" class="h-2.5 w-2.5" />
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
						onclick={() => (selectedCores = new Set(availableCores.map((core) => core.id)))}
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
				<Button onclick={handleConfirm} disabled={selectedCores.size === 0}>
					Allocate {selectedCores.size} Core
					{selectedCores.size !== 1 ? 's' : ''}
				</Button>
			{/if}
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
