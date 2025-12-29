<script lang="ts">
	import { replaceDevice } from '$lib/api/zfs/pool';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import type { Disk, Partition } from '$lib/types/disk/disk';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { getDiskSize } from '$lib/utils/disk';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		replacing: boolean;
		pool: Zpool;
		old: string;
		latest: string;
		usable: { disks: Disk[]; partitions: Partition[] };
	}

	let { open = $bindable(), replacing = $bindable(), pool, old, latest, usable }: Props = $props();

	async function handleReplace() {
		if (!latest) {
			toast.error('Replacement device not selected', { position: 'bottom-center' });
			return;
		}

		const vdev = pool?.vdevs
			? Object.values(pool.vdevs).find(
					(v) => v.vdevs && Object.values(v.vdevs).some((d) => d.name === old)
				)
			: null;

		const disks = {
			old: vdev?.vdevs ? Object.values(vdev.vdevs).find((d) => d.name === old) : null,
			latest: usable.disks.find((d) => d.device === latest)
		};

		if (disks.old && disks.latest) {
			if (parseInt(getDiskSize(disks.latest)) < disks.old.size) {
				toast.error('New disk is smaller than old disk', {
					position: 'bottom-center'
				});
				return;
			}
		} else if (!disks.latest && disks.old) {
			const partition = usable.partitions.find((p) => p.name === latest);
			if (partition) {
				if (partition.size < disks.old.size) {
					toast.error('New partition is smaller than old device', {
						position: 'bottom-center'
					});
					return;
				}
			}
		}

		try {
			let snapshot = {
				name: pool.name,
				guid: pool.guid,
				old: $state.snapshot(old),
				latest: $state.snapshot(latest)
			};

			toast.promise(
				replaceDevice({
					guid: pool.guid,
					old: old,
					new: latest
				}),
				{
					loading: `Replacing ${snapshot.old} with ${snapshot.latest}`,
					success: (response) => {
						replacing = true;
						return `Device replacement started for ${snapshot.old} in ${snapshot.name}`;
					},
					error: (error) => {
						replacing = false;
						return `Error replacing device`;
					},
					position: 'bottom-center'
				}
			);
		} finally {
			open = false;
			replacing = true;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
	>
		<div class="flex items-center justify-between pb-3">
			<Dialog.Header>
				<Dialog.Title>{`Replace ${old} in ${pool.name}`}</Dialog.Title>
			</Dialog.Header>

			<Dialog.Close
				class="flex h-5 w-5 items-center justify-center rounded-sm opacity-70 transition-opacity hover:opacity-100"
				onclick={() => {
					open = false;
				}}
			>
				<span class="icon-[material-symbols--close-rounded] h-5 w-5"></span>
			</Dialog.Close>
		</div>

		<div class="space-y-1 py-1">
			<Select.Root type="single" bind:value={latest}>
				<Select.Trigger class="w-full">
					{latest ? latest : 'Select Replacement Device'}
				</Select.Trigger>
				<Select.Content class="max-h-36 overflow-y-auto">
					<Select.Group>
						{#each usable.disks as disk}
							<Select.Item value={disk.device} label={disk.device}>
								{disk.device}
							</Select.Item>
						{/each}

						{#each usable.partitions as partition}
							<Select.Item value={partition.name} label={partition.name}>
								{partition.name}
							</Select.Item>
						{/each}
					</Select.Group>
				</Select.Content>
			</Select.Root>
		</div>

		<Dialog.Footer class="flex justify-between gap-2">
			<div class="flex gap-2">
				<Button size="sm" class="h-8" onclick={() => handleReplace()}>Replace</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
