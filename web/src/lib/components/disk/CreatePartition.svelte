<script lang="ts">
	import { createPartitions } from '$lib/api/disk/disk';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Slider } from '$lib/components/ui/slider/index.js';
	import * as Table from '$lib/components/ui/table';
	import type { Disk } from '$lib/types/disk/disk';
	import humanFormat from 'human-format';
	import { watch } from 'runed';
	import { tick } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { slide } from 'svelte/transition';

	interface Data {
		open: boolean;
		disk: Disk | null;
		onCancel: () => void;
		reload?: boolean;
	}

	let { open, disk, onCancel, reload = $bindable() }: Data = $props();

	let newPartitions: { name: string; size: number }[] = $state([]);
	let currentPartitionInput = $state('0 B');
	let currentTextPartition = $derived.by(() => {
		try {
			const parsed = humanFormat.parse.raw(currentPartitionInput);
			return parsed.factor * parsed.value;
		} catch (e) {
			return 0;
		}
	});

	let currentPartitionSlider = $state(0);

	function removePartition(index: number) {
		const removedPartition = newPartitions.splice(index, 1)[0];
		remainingSpace += removedPartition.size;
	}

	async function savePartitions() {
		if (disk) {
			const sizes = newPartitions.map((partition) => Math.floor(partition.size));
			const result = await createPartitions(`/dev/${disk.device}`, sizes);
			let message = '';

			if (result.status === 'success') {
				message = `Partition${sizes.length > 1 ? 's' : ''} created`;
			} else {
				message = `Error creating ${sizes.length > 1 ? 'partitions' : 'partition'}`;
			}

			if (reload !== undefined) {
				reload = true;
			}

			toast.success(message, {
				position: 'bottom-center'
			});

			newPartitions = [];
		}
		onCancel();
	}

	async function addPartition() {
		if (currentTextPartition > 0) {
			if (remainingSpace - currentTextPartition < 0) {
				currentTextPartition = remainingSpace;
			}

			newPartitions.push({
				name: `New Partition ${newPartitions.length + 1}`,
				size: currentTextPartition
			});
			remainingSpace -= currentTextPartition;

			currentTextPartition = 0;
			currentPartitionInput = '0B';

			await tick();

			const table = document.getElementById('table-body');
			if (table) {
				table.scroll({
					top: table.scrollHeight,
					behavior: 'smooth'
				});
			}
		}
	}

	function close() {
		newPartitions = [];
		remainingSpace = 0;
		currentTextPartition = 0;
		onCancel();
	}

	function calculateRemainingSpace(disk: Disk) {
		if (!disk) return 0;
		const usedSpace =
			disk.partitions && disk.partitions.length > 0
				? disk.partitions.reduce((total, partition) => total + partition.size, 0)
				: 0;

		let actual = disk.size - usedSpace;

		if (actual > 128 * 1024 * 1024) {
			actual = actual - 128 * 1024 * 1024;
		}

		return actual;
	}

	let remainingSpace = $derived.by(() => (disk ? calculateRemainingSpace(disk) : 0));
	let remainingSpacePercentage = $derived.by(() => {
		if (!disk) return 0;
		return (remainingSpace / disk.size) * 100;
	});

	watch(
		() => currentTextPartition,
		(value) => {
			try {
				const percentage = (value / remainingSpace) * 100;
				currentPartitionSlider = isNaN(percentage) ? 0 : percentage;
			} catch (e) {
				currentPartitionSlider = 0;
			}
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed top-1/2 left-1/2 w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-4 overflow-hidden p-5 lg:max-w-3xl"
	>
		<div class="flex items-center justify-between">
			<Dialog.Header class="p-0">
				<Dialog.Title>
					<span class="flex items-center gap-2">
						<span class="icon icon-[ant-design--partition-outlined] h-6 w-6"></span>
						<span>Create Partitions</span>
					</span>
				</Dialog.Title>
				<Dialog.Description></Dialog.Description>
			</Dialog.Header>

			<div class="flex items-center gap-0.5">
				<Button
					size="sm"
					variant="link"
					class="h-4 cursor-pointer"
					title={'Reset'}
					onclick={() => {
						newPartitions = [];
						remainingSpace = disk ? calculateRemainingSpace(disk) : 0;
						currentTextPartition = 0;
						currentPartitionInput = '';
					}}
				>
					<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Reset</span>
				</Button>
				<Button
					size="sm"
					variant="link"
					class="h-4 cursor-pointer"
					title={'Close'}
					onclick={() => close()}
				>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</div>
		</div>

		<div class="max-h-75 overflow-y-auto" id="table-body">
			<Table.Root>
				<Table.Header class="bg-background sticky top-0 z-10">
					<Table.Row>
						<Table.Head class="w-50">Name</Table.Head>
						<Table.Head class="w-37.5 text-right">Size</Table.Head>
						<Table.Head class="w-37.5 text-right">Usage</Table.Head>
						<Table.Head class="w-25 text-right">Actions</Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#if disk && disk.partitions && disk.partitions.length > 0}
						{#each disk.partitions as partition}
							<Table.Row>
								<Table.Cell>{partition.name}</Table.Cell>
								<Table.Cell class="text-right">{humanFormat(partition.size)}</Table.Cell>
								<Table.Cell class="text-right">{partition.usage}</Table.Cell>
								<Table.Cell class="text-right">
									<span class="text-muted-foreground text-xs italic">Existing</span>
								</Table.Cell>
							</Table.Row>
						{/each}
					{/if}

					{#if newPartitions.length > 0}
						{#each newPartitions as partition, index}
							<Table.Row>
								<Table.Cell>{partition.name}</Table.Cell>
								<Table.Cell class="text-right">{humanFormat(partition.size)}</Table.Cell>
								<Table.Cell class="text-right">-</Table.Cell>
								<Table.Cell class="text-right">
									<Button variant="ghost" class="h-8" onclick={() => removePartition(index)}>
										<span class="icon-[gg--trash] h-4 w-4"></span>
									</Button>
								</Table.Cell>
							</Table.Row>
						{/each}
					{/if}

					{#if (!disk || !disk.partitions || disk.partitions.length === 0) && newPartitions.length === 0}
						<Table.Row>
							<Table.Cell colspan={4} class="text-muted-foreground h-20 text-center">
								No partitions created yet
							</Table.Cell>
						</Table.Row>
					{/if}
				</Table.Body>
			</Table.Root>
		</div>

		<div class="space-y-2 border-t pt-4">
			<div class="flex items-center gap-4">
				<div class="flex-1">
					{#if remainingSpace > 0}
						<Slider
							type="single"
							bind:value={currentPartitionSlider}
							max={100}
							step={0.01}
							onValueCommit={(value: number) => {
								currentTextPartition = (remainingSpace * value) / 100;
								currentPartitionInput = humanFormat(currentTextPartition);
							}}
						></Slider>
					{/if}
				</div>

				<Input
					type="text"
					class="h-8 w-24 text-right"
					min="0"
					max={remainingSpace}
					bind:value={currentPartitionInput}
				/>

				<div class={remainingSpace > 0 ? '' : 'cursor-not-allowed'}>
					<Button
						class="h-8 whitespace-nowrap"
						onclick={addPartition}
						disabled={currentTextPartition <= 0}
					>
						{#if remainingSpace > 0}
							Add Partition
						{:else}
							No space left
						{/if}
					</Button>
				</div>
			</div>

			<div class="flex justify-end">
				<div class="flex flex-nowrap items-center gap-1 whitespace-nowrap">
					<p class="text-muted-foreground text-sm">
						Remaining space: <span class="font-semibold">{humanFormat(remainingSpace)}</span>
					</p>
				</div>
			</div>
		</div>
		{#if newPartitions.length > 0}
			<div in:slide={{ duration: 200 }} out:slide={{ duration: 200 }}>
				<Dialog.Footer class="flex justify-between gap-2 border-t py-4">
					<div class="flex gap-2">
						<Button size="sm" class="h-8" onclick={savePartitions}>Save Partitions</Button>
					</div>
				</Dialog.Footer>
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
