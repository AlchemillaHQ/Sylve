<script lang="ts">
	import { editPool } from '$lib/api/zfs/pool';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { Textarea } from '$lib/components/ui/textarea';
	import type { APIResponse } from '$lib/types/common';
	import type { Disk, Partition } from '$lib/types/disk/disk';

	import type { Zpool } from '$lib/types/zfs/pool';
	import { deepSearchKey } from '$lib/utils/arr';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		pool: Zpool;
		usable: { disks: Disk[]; partitions: Partition[] };
		parsePoolActionError: (response: APIResponse) => string;
	}

	let { open = $bindable(), pool, usable, parsePoolActionError }: Props = $props();
	let spares: string[] = $derived.by(() => {
		const uD: string[] = usable.disks.map((disk) => disk.device);
		const uP: string[] = usable.partitions.map((partition) => `/dev/${partition.name}`);
		const all: string[] = [...uD, ...uP].filter((device) => {
			return device !== 'da0' && device !== 'cd0';
		});

		return all;
	});

	let isRaid: boolean = $derived.by(() => {
		const names = deepSearchKey(pool, 'name');
		return names.some((name) => name.startsWith('raidz') || name.startsWith('mirror'));
	});

	let options = {
		autoexpand: pool.properties.find((prop) => prop.property === 'autoexpand')?.value || 'off',
		autotrim: pool.properties.find((prop) => prop.property === 'autotrim')?.value || 'off',
		delegation: pool.properties.find((prop) => prop.property === 'delegation')?.value || 'off',
		comment: pool.properties.find((prop) => prop.property === 'comment')?.value || '',
		failmode: pool.properties.find((prop) => prop.property === 'failmode')?.value || 'wait',
		spares: pool.spares.map((spare) => spare.name) || ([] as string[]),
		autoreplace: pool.properties.find((prop) => prop.property === 'autoreplace')?.value || 'off',
		editing: false
	};

	let properties = $state(options);

	async function edit() {
		if (properties.editing) return;

		properties.editing = true;

		const response = await editPool(
			pool.name,
			{
				comment: properties.comment,
				autoexpand: properties.autoexpand,
				autotrim: properties.autotrim,
				delegation: properties.delegation,
				failmode: properties.failmode
			},
			properties.spares
		);

		if (response.error) {
			toast.error(parsePoolActionError(response), {
				position: 'bottom-center'
			});

			properties.editing = false;
			return;
		}

		properties.editing = false;
		open = false;
		toast.success('Pool edited', {
			position: 'bottom-center'
		});
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed top-1/2 left-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-2xl"
		onInteractOutside={() => {
			properties = options;
			open = false;
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between gap-2 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--database-edit] h-5 w-5"></span>

					<span>Edit ZFS Pool - {pool.name}</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => {
							properties = options;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
							properties = options;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 grid grid-cols-1 gap-4">
			<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
				<SimpleSelect
					label="Auto Expand"
					placeholder="Select Auto Expand"
					options={[
						{ value: 'on', label: 'Yes' },
						{ value: 'off', label: 'No' }
					]}
					bind:value={properties.autoexpand}
					onChange={(value) => (properties.autoexpand = value)}
				/>

				<SimpleSelect
					label="Auto Trim"
					placeholder="Select Auto Trim"
					options={[
						{ value: 'on', label: 'Yes' },
						{ value: 'off', label: 'No' }
					]}
					bind:value={properties.autotrim}
					onChange={(value) => (properties.autotrim = value)}
				/>

				<SimpleSelect
					label="Delegation"
					placeholder="Select Delegation"
					options={[
						{ value: 'on', label: 'Yes' },
						{ value: 'off', label: 'No' }
					]}
					bind:value={properties.delegation}
					onChange={(value) => (properties.delegation = value)}
				/>

				<SimpleSelect
					label="Fail Mode"
					placeholder="Select Fail Mode"
					options={[
						{ value: 'continue', label: 'Continue' },
						{ value: 'wait', label: 'Wait' },
						{ value: 'panic', label: 'Panic' }
					]}
					bind:value={properties.failmode}
					onChange={(value) => (properties.failmode = value)}
				/>

				{#if properties.spares && isRaid}
					<div class="h-full space-y-1">
						<Label class="w-24 text-sm whitespace-nowrap">Spares</Label>
						<Select.Root
							type="multiple"
							bind:value={properties.spares}
							onValueChange={(value) => {
								properties.spares = value as string[];
							}}
						>
							<Select.Trigger class="w-full">
								{#if properties.spares.length > 0}
									<span>
										{properties.spares.join(', ')}
									</span>
								{:else}
									<span>Select spares</span>
								{/if}
							</Select.Trigger>
							<Select.Content>
								<Select.Group>
									{#each spares as spare (spare)}
										<Select.Item value={spare} label={spare}>
											{spare}
										</Select.Item>
									{/each}
								</Select.Group>
							</Select.Content>
						</Select.Root>
					</div>

					{#if properties.spares.length > 0}
						<SimpleSelect
							label="Auto Replace"
							placeholder="Select Auto Replace"
							options={[
								{ value: 'on', label: 'Yes' },
								{ value: 'off', label: 'No' }
							]}
							bind:value={properties.autoreplace}
							onChange={(value) => (properties.autoreplace = value)}
						/>
					{/if}
				{/if}
			</div>
		</div>

		<div class="mt-4 flex-1 space-y-1">
			<Label for="comment">Comment</Label>
			<Textarea
				id="comment"
				placeholder="Comments about the pool"
				bind:value={properties.comment}
			/>
		</div>

		<Dialog.Footer class="mt-4 flex justify-between gap-2">
			<div class="flex gap-2">
				<Button
					size="sm"
					class="h-8 w-full lg:w-28"
					onclick={() => {
						edit();
					}}
				>
					{#if properties.editing}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					{:else}
						Edit
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
