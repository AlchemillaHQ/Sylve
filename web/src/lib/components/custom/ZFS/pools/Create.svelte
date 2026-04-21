<script lang="ts">
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import * as Accordion from '$lib/components/ui/accordion/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { Textarea } from '$lib/components/ui/textarea';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import type { Disk, Partition } from '$lib/types/disk/disk';
	import type { Zpool, ZpoolRaidType } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { draggable, dropzone } from '$lib/utils/dnd';
	import { raidTypeArr } from '$lib/utils/zfs/pool';
	import { flip } from 'svelte/animate';
	import { slide } from 'svelte/transition';
	import { createPool } from '$lib/api/zfs/pool';
	import { isValidPoolName } from '$lib/utils/zfs';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { parsePoolActionError } from '$lib/utils/zfs/pool.svelte';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		disks: Disk[];
		pools: Zpool[];
		usable: { disks: Disk[]; partitions: Partition[] };
		reload: boolean;
	}

	interface VdevContainer {
		id: string;
		disks: Disk[];
		partitions: Partition[];
	}

	let { open = $bindable(), disks, pools, usable, reload = $bindable() }: Props = $props();

	let options = {
		name: '',
		vdev: {
			count: 1,
			containers: [] as VdevContainer[]
		},
		advanced: true,
		props: {
			comment: '',
			ashift: '12',
			autoexpand: 'off',
			autotrim: 'off',
			delegation: 'on',
			failmode: 'wait',
			spares: [] as string[],
			autoreplace: 'off'
		},
		raid: 'stripe' as ZpoolRaidType,
		mount: '',
		usable: 0,
		force: false,
		creating: false
	};

	let raidTypes = $state(raidTypeArr);
	let properties = $state(options);
	let accordionOpen = $state<string[]>([]);
	let sectionOrder = $state<string[]>(['HDD', 'SSD', 'NVMe', 'Partitions']);

	watch(
		() => open,
		() => {
			if (open) {
				const initialCounts: Record<string, number> = {
					HDD: usable.disks.filter((d) => d.type === 'HDD' && d.partitions.length === 0).length,
					SSD: usable.disks.filter((d) => d.type === 'SSD' && d.partitions.length === 0).length,
					NVMe: usable.disks.filter((d) => d.type === 'NVMe' && d.partitions.length === 0).length,
					Partitions: usable.partitions.length
				};
				sectionOrder = ['HDD', 'SSD', 'NVMe', 'Partitions'].sort(
					(a, b) => (initialCounts[b] ?? 0) - (initialCounts[a] ?? 0)
				);
				accordionOpen = diskSections.filter((s) => s.count > 0).map((s) => s.key);
			}
		}
	);

	watch(
		() => properties.vdev.count,
		() => {
			let int = parseInt(properties.vdev.count.toString(), 10);
			if (isNaN(int) || int < 1) {
				properties.vdev.count = 1;
			} else {
				properties.vdev.count = int;
			}
		}
	);

	let diskSections = $derived(
		sectionOrder.map((key) => ({
			key,
			count:
				key === 'Partitions'
					? usable.partitions.filter(
							(p) =>
								!properties.vdev.containers
									.flatMap((v) => v.partitions)
									.some((vp) => vp.name === p.name)
						).length
					: usable.disks.filter(
							(d) => d.type === key && d.partitions.length === 0 && !isDiskInVdev(d.uuid)
						).length
		}))
	);

	let spares: string[] = $derived.by(() => {
		const uD: string[] = usable.disks
			.filter((disk) => {
				return !properties.vdev.containers.some((vdev) => {
					return vdev.disks.some((d) => d.uuid === disk.uuid);
				});
			})
			.map((disk) => disk.device);

		const uP: string[] = usable.partitions
			.filter((partition) => {
				return !properties.vdev.containers.some((vdev) => {
					return vdev.partitions.some((p) => p.name === partition.name);
				});
			})
			.map((partition) => partition.name);

		return [...uD, ...uP].filter((device) => {
			return device !== 'da0' && device !== 'cd0';
		});
	});

	function setUsableSpace() {
		let totalUsable = 0;

		for (const vdev of properties.vdev.containers) {
			const sizes = [
				...(vdev.disks ?? []).map((d) => d.size),
				...(vdev.partitions ?? []).map((p) => p.size)
			].filter((size) => typeof size === 'number');

			if (sizes.length === 0) continue;

			sizes.sort((a, b) => a - b);

			const total = sizes.reduce((sum, s) => sum + s, 0);

			switch (properties.raid) {
				case 'stripe':
					totalUsable += total;
					break;
				case 'mirror':
					totalUsable += sizes[0];
					break;
				case 'raidz':
					if (sizes.length > 1) {
						totalUsable += total - sizes[sizes.length - 1];
					}
					break;
				case 'raidz2':
					if (sizes.length > 2) {
						totalUsable += total - sizes.slice(-2).reduce((a, b) => a + b, 0);
					}
					break;
				case 'raidz3':
					if (sizes.length > 3) {
						totalUsable += total - sizes.slice(-3).reduce((a, b) => a + b, 0);
					}
					break;
				default:
					console.warn(`Unknown RAID type: ${properties.raid}`);
			}
		}

		properties.usable = totalUsable;
	}

	function setRedundancyAvailability() {
		const vdevLengths = properties.vdev.containers.map(
			(vdev) => vdev.disks.length + vdev.partitions.length
		);

		raidTypes = raidTypes.map((type) => {
			switch (type.value) {
				case 'stripe':
					return { ...type, available: true };
				case 'mirror': {
					const allMirrors = vdevLengths.every((length) => length >= 2) && vdevLengths.length > 0;
					return { ...type, available: allMirrors };
				}
				case 'raidz':
					return {
						...type,
						available: vdevLengths.every((length) => length >= 3) && vdevLengths.length > 0
					};
				case 'raidz2':
					return {
						...type,
						available: vdevLengths.every((length) => length >= 4) && vdevLengths.length > 0
					};
				case 'raidz3':
					return {
						...type,
						available: vdevLengths.every((length) => length >= 5) && vdevLengths.length > 0
					};
				default:
					return type;
			}
		});

		if (!raidTypes.find((rt) => rt.value === properties.raid)?.available) {
			properties.raid = (raidTypes.find((rt) => rt.available)?.value as ZpoolRaidType) || 'stripe';
		}

		setUsableSpace();
	}

	function getVdevErrors(id: number): string {
		const vdev = properties.vdev.containers[id];
		const disks = vdev?.disks || [];
		const partitions = vdev?.partitions || [];
		const diskSizes = disks.map((disk) => disk.size);
		const partSizes = partitions.map((partition) => partition.size);
		const allSizes = [...diskSizes, ...partSizes];

		const diskTypes = disks.map((disk) => disk.type);
		for (let i = 0; i < diskTypes.length - 1; i++) {
			if (diskTypes[i] !== diskTypes[i + 1]) {
				return 'Disks within a VDEV should ideally be the same type';
			}
		}

		const partitionTypes = partitions.map((partition) => {
			const disk = usable.disks.find((d) => d.partitions.some((p) => p.name === partition.name));
			return disk ? disk.type : null;
		});

		for (let i = 0; i < partitionTypes.length - 1; i++) {
			if (partitionTypes[i] !== partitionTypes[i + 1]) {
				return 'Disks within a VDEV should ideally be the same drive type';
			}
		}

		for (let i = 0; i < allSizes.length - 1; i++) {
			if (allSizes[i] !== allSizes[i + 1]) {
				if (partSizes.length === 0) {
					return 'Disks within a VDEV should ideally be the same size';
				} else if (diskSizes.length === 0) {
					return 'Partitions within a VDEV should ideally be the same size';
				} else {
					return 'Disks/Partitions within a VDEV should ideally be the same drive type';
				}
			}
		}

		return '';
	}

	function handleDropToVdev(containerId: number, event: DragEvent) {
		properties.props.spares = [];

		const diskId = event.dataTransfer?.getData('application/disk');

		if (!properties.vdev.containers[containerId]) {
			properties.vdev.containers[containerId] = {
				id: `vdev-${containerId}`,
				disks: [],
				partitions: []
			};
		}

		const disk = disks.find((d) => d.uuid === diskId);

		if (disk) {
			const existingDisk = properties.vdev.containers[containerId].disks.find(
				(d) => d.uuid === disk.uuid
			);
			if (!existingDisk) {
				properties.vdev.containers[containerId].disks.push(disk);
				usable.disks = usable.disks.filter((ud) => ud.uuid !== disk.uuid);
			}
		}

		if (!disk) {
			const diskContainingPartition = disks.find((d) =>
				d.partitions.some((p) => p.name === diskId)
			);

			if (diskContainingPartition) {
				const partition = diskContainingPartition.partitions.find((p) => p.name === diskId);
				if (partition) {
					const existingPartition = properties.vdev.containers[containerId].partitions.find(
						(p) => p.name === partition.name
					);

					if (!existingPartition) {
						properties.vdev.containers[containerId].partitions.push(partition);
						usable.disks = usable.disks.filter(
							(ud) => !ud.partitions.some((p) => p.name === partition.name)
						);
					}
				}
			}
		}

		setRedundancyAvailability();
		setUsableSpace();
	}

	function isDiskInVdev(diskId: string | undefined | string[]): boolean {
		if (!diskId) return false;

		if (typeof diskId === 'string') {
			return properties.vdev.containers.some((vdev) => {
				return vdev.disks.some((disk) => disk.uuid === diskId);
			});
		}

		if (Array.isArray(diskId)) {
			return properties.vdev.containers.some((vdev) => {
				return vdev.partitions.some((partition) => diskId.includes(partition.name));
			});
		}

		return false;
	}

	function vdevContains(id: number): boolean {
		const vdev = properties.vdev.containers[id];
		if (!vdev) return false;

		return vdev.disks.length > 0 || vdev.partitions.length > 0;
	}

	function addToLastVdev(itemId: string) {
		const lastIdx = properties.vdev.count - 1;

		if (!properties.vdev.containers[lastIdx]) {
			properties.vdev.containers[lastIdx] = {
				id: `vdev-${lastIdx}`,
				disks: [],
				partitions: []
			};
		}

		const disk = disks.find((d) => d.uuid === itemId);
		if (disk) {
			const exists = properties.vdev.containers[lastIdx].disks.find((d) => d.uuid === disk.uuid);
			if (!exists) {
				properties.vdev.containers[lastIdx].disks.push(disk);
				usable.disks = usable.disks.filter((ud) => ud.uuid !== disk.uuid);
			}
		} else {
			const parentDisk = disks.find((d) => d.partitions.some((p) => p.name === itemId));
			if (parentDisk) {
				const partition = parentDisk.partitions.find((p) => p.name === itemId);
				if (partition) {
					const exists = properties.vdev.containers[lastIdx].partitions.find(
						(p) => p.name === partition.name
					);
					if (!exists) {
						properties.vdev.containers[lastIdx].partitions.push(partition);
						usable.disks = usable.disks.filter(
							(ud) => !ud.partitions.some((p) => p.name === partition.name)
						);
					}
				}
			}
		}

		properties.props.spares = [];
		setRedundancyAvailability();
		setUsableSpace();
	}

	function removeFromVdev(id: number, diskId: string) {
		properties.props.spares = [];

		const vdev = properties.vdev.containers[id];
		if (!vdev) return;

		const diskIndex = vdev.disks.findIndex((d) => d.uuid === diskId);
		if (diskIndex !== -1) {
			const removedDisk = vdev.disks.splice(diskIndex, 1)[0];
			if (!usable.disks.some((ud) => ud.uuid === removedDisk.uuid)) {
				usable.disks = [...usable.disks, removedDisk];
			}
		}

		const partitionIndex = vdev.partitions.findIndex((p) => p.name === diskId);
		if (partitionIndex !== -1) {
			const removedPartition = vdev.partitions.splice(partitionIndex, 1)[0];
			const parentDisk = disks.find((d) =>
				d.partitions.some((p) => p.name === removedPartition.name)
			);
			if (
				parentDisk &&
				!usable.disks.some((ud) => ud.partitions.some((p) => p.name === removedPartition.name))
			) {
				usable.disks = [...usable.disks, { ...parentDisk }];
			}
		}

		setRedundancyAvailability();
	}

	async function makePool() {
		if (properties.creating) return;

		properties.creating = true;

		if (usable.disks.length === 0 && usable.partitions.length === 0) {
			toast.error('No available disks or partitions', {
				position: 'bottom-center'
			});

			properties.creating = false;
			return;
		}

		if (!isValidPoolName(properties.name)) {
			toast.error('Invalid pool name', {
				position: 'bottom-center'
			});

			properties.creating = false;
			return;
		}

		if (
			properties.vdev.containers.some((vdev) => {
				return vdev.disks.length === 0 && vdev.partitions.length === 0;
			})
		) {
			properties.vdev.containers = properties.vdev.containers.filter((vdev) => {
				return vdev.disks.length > 0 || vdev.partitions.length > 0;
			});
			return;
		}

		if (properties.vdev.containers.length === 0) {
			toast.error('At least one VDEV containing disks is required', {
				position: 'bottom-center'
			});
			properties.creating = false;
			return;
		}

		const raid: ZpoolRaidType = properties.raid;

		properties.creating = true;
		let biggestSize = 0;

		for (const vdev of properties.vdev.containers) {
			const sizes = [
				...(vdev.disks ?? []).map((d) => d.size),
				...(vdev.partitions ?? []).map((p) => p.size)
			].filter((size) => typeof size === 'number');

			if (sizes.length === 0) continue;
			sizes.sort((a, b) => a - b);
			biggestSize = Math.max(biggestSize, ...sizes);
		}

		if (properties.props.spares.length !== 0) {
			const spareSizes = properties.props.spares.map((spare) => {
				const disk = usable.disks.find((d) => d.device === spare);
				if (disk) {
					return disk.size;
				}
				const partition = usable.partitions.find((p) => p.name === spare);
				if (partition) {
					return partition.size;
				}
				return 0;
			});

			const minSpareSize = Math.min(...spareSizes);
			if (minSpareSize < biggestSize) {
				toast.error('Spares must be larger than the largest disk in the pool', {
					position: 'bottom-center'
				});
				properties.creating = false;
				return;
			}
		}

		const existingPools = pools.filter((pool) => pool.name === properties.name);
		if (existingPools.length > 0) {
			toast.error('A pool with this name already exists', {
				position: 'bottom-center'
			});
			properties.creating = false;
			return;
		}

		const response = await createPool({
			name: properties.name,
			raidType: raid,
			vdevs: properties.vdev.containers.map((vdev) => ({
				name: vdev.id,
				devices: [
					...vdev.disks.map((disk) => disk.device),
					...vdev.partitions.map((partition) => partition.name)
				]
			})),
			properties: {
				comment: properties.props.comment,
				ashift: properties.props.ashift,
				autoexpand: properties.props.autoexpand,
				autotrim: properties.props.autotrim,
				delegation: properties.props.delegation,
				failmode: properties.props.failmode
			},
			spares: properties.props.spares.map((spare) => spare),
			createForce: properties.force
		});

		reload = true;
		properties.creating = false;

		if (response.status === 'error') {
			toast.error(parsePoolActionError(response), {
				position: 'bottom-center'
			});
		} else {
			toast.success('Pool Created', {
				position: 'bottom-center'
			});

			properties = options;
			open = false;
		}
	}

	onMount(() => {
		properties.raid = 'stripe';
		raidTypes = raidTypeArr;
	});
</script>

{#snippet vdevErrors(id: number)}
	{#if getVdevErrors(id) !== ''}
		<div class="absolute top-1 right-1 z-50 cursor-pointer text-yellow-700 hover:text-yellow-600">
			<Tooltip.Provider>
				<Tooltip.Root>
					<Tooltip.Trigger class="cursor-pointer">
						<span class="icon-[carbon--warning-filled] pointer-events-none h-5 w-5 cursor-pointer"
						></span>
					</Tooltip.Trigger>
					<Tooltip.Content>
						<p>
							<!-- eslint-disable-next-line svelte/no-at-html-tags -->
							{@html getVdevErrors(id)}
						</p>
					</Tooltip.Content>
				</Tooltip.Root>
			</Tooltip.Provider>
		</div>
	{/if}
{/snippet}

{#snippet vdevContainer(id: number)}
	{#each properties.vdev.containers[id]?.disks || [] as disk (disk.uuid)}
		<div animate:flip={{ duration: 300 }} class="relative">
			{#if disk.type === 'HDD'}
				<span class="icon-[mdi--harddisk] h-11 w-11 text-green-500"></span>
			{:else if disk.type === 'SSD'}
				<span class="icon-[icon-park-outline--ssd] h-11 w-11 text-blue-500"></span>
			{:else if disk.type === 'NVMe'}
				<span class="icon-[bi--nvme] h-11 w-11 rotate-90 text-blue-500"></span>
			{/if}

			<div class="max-w-12 truncate text-center text-xs">
				{disk.device.split('/').pop()}
			</div>

			<button
				aria-label="Remove {disk.device.split('/').pop()} from VDEV {id + 1}"
				class="absolute -top-1 -right-1 h-5 w-5 flex items-center justify-center
	       rounded-full bg-red-500 text-white hover:bg-red-600"
				onclick={() => removeFromVdev(id, disk.uuid as string)}
			>
				<span class="icon-[mdi--close] h-3 w-3 block"></span>
			</button>
		</div>
	{/each}

	{#each properties.vdev.containers[id]?.partitions || [] as partition (partition.name)}
		<div animate:flip={{ duration: 300 }} class="relative">
			<span class="icon-[ant-design--partition-outlined] h-11 w-11 rotate-90 text-blue-500"></span>

			<div class="max-w-12 truncate text-center text-xs">
				{partition.name.split('/').pop()}
			</div>

			<button
				aria-label="Remove {partition.name.split('/').pop()} from VDEV {id + 1}"
				class="absolute -top-1 -right-1 h-5 w-5 rounded-full bg-red-500 text-white hover:bg-red-600
	       flex items-center justify-center"
				onclick={() => removeFromVdev(id, partition.name)}
			>
				<span class="icon-[mdi--close] h-3 w-3 block"></span>
			</button>
		</div>
	{/each}
{/snippet}

{#snippet diskContainer(type: string)}
	<ScrollArea class="w-full rounded-md whitespace-nowrap" orientation="horizontal">
		<div class="flex min-h-16 items-center justify-start gap-4 px-1 py-2">
			{#each usable.disks.filter((disk) => disk.type === type && disk.partitions.length === 0 && !isDiskInVdev(disk.uuid)) as disk (disk.uuid)}
				<div class="relative text-center" animate:flip={{ duration: 300 }}>
					<div class="cursor-move" use:draggable={disk.uuid ?? ''}>
						{#if type === 'HDD'}
							<span class="icon-[mdi--harddisk] h-11 w-11 text-green-500"></span>
						{:else if type === 'SSD'}
							<span class="icon-[icon-park-outline--ssd] h-11 w-11 text-blue-500"></span>
						{:else if type === 'NVMe'}
							<span class="icon-[bi--nvme] h-11 w-11 rotate-90 text-blue-500"></span>
						{/if}
					</div>
					<div class="max-w-16 truncate text-xs">
						{disk.device.replaceAll('/dev/', '')}
					</div>
					<div class="text-muted-foreground text-xs">
						{formatBytesBinary(disk.size)}
					</div>
					<button
						aria-label="Add {disk.device.split('/').pop()} to last VDEV"
						class="absolute -top-1 -right-1 flex h-5 w-5 items-center justify-center rounded-full bg-green-500 text-white hover:bg-green-600"
						onclick={() => addToLastVdev(disk.uuid ?? '')}
					>
						<span class="icon-[clarity--arrow-line] block h-3 w-3"></span>
					</button>
				</div>
			{/each}

			{#if usable.disks.filter((disk) => disk.type === type).length === 0 || usable.disks.filter((disk) => disk.type === type && disk.partitions.length === 0 && !isDiskInVdev(disk.uuid)).length === 0}
				<div class="text-muted-foreground/80 flex h-16 w-full items-center justify-center">
					No available {type} disks
				</div>
			{/if}
		</div>
	</ScrollArea>
{/snippet}

{#snippet partitionsContainer()}
	<ScrollArea class="w-full rounded-md whitespace-nowrap" orientation="horizontal">
		<div class="flex min-h-16 items-center justify-start gap-4 px-1 py-2">
			{#each usable.partitions.filter((partition) => !properties.vdev.containers
						.flatMap((vdev) => vdev.partitions)
						.some((p) => p.name === partition.name)) as partition (partition.name)}
				<div class="relative text-center" animate:flip={{ duration: 100 }}>
					<div class="cursor-move" use:draggable={partition.name}>
						<span class="icon-[ant-design--partition-outlined] h-11 w-11 rotate-90 text-blue-500"
						></span>
					</div>
					<div class="max-w-16 truncate text-xs">
						{partition.name}
					</div>
					<div class="text-muted-foreground text-xs">
						{formatBytesBinary(partition.size)}
					</div>
					<button
						aria-label="Add {partition.name} to last VDEV"
						class="absolute -top-1 -right-1 flex h-5 w-5 items-center justify-center rounded-full bg-green-500 text-white hover:bg-green-600"
						onclick={() => addToLastVdev(partition.name)}
					>
						<span class="icon-[clarity--arrow-line] block h-3 w-3"></span>
					</button>
				</div>
			{/each}

			{#if usable.partitions.length === 0 || usable.partitions.filter((partition) => !properties.vdev.containers
							.flatMap((vdev) => vdev.partitions)
							.some((p) => p.name === partition.name)).length === 0}
				<div class="text-muted-foreground/80 flex h-16 w-full items-center justify-center">
					No available partitions
				</div>
			{/if}
		</div>
	</ScrollArea>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={() => {
			properties = options;
			open = false;
		}}
		class="fixed top-1/2 left-1/2 flex h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform flex-col gap-4 overflow-auto p-5 transition-all duration-300 ease-in-out lg:max-w-[70%]"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between  text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[bi--hdd-stack-fill] h-5 w-5"></span>

					Create ZFS Pool
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title="Reset"
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
						title="Close"
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>

						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<Tabs.Root value="tab-devices" class="flex h-full flex-col overflow-y-auto ">
			<Tabs.List class="grid w-full grid-cols-2 p-0 ">
				<Tabs.Trigger value="tab-devices" class="border-b">Devices</Tabs.Trigger>
				<Tabs.Trigger value="tab-options" class="border-b">Options</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content class="mt-4" value="tab-devices">
				<Card.Root class="border-none ">
					<Card.Content class="grid grid-cols-1 gap-4 lg:grid-cols-3 lg:items-end">
						<CustomValueInput
							label="Name"
							placeholder="tank"
							bind:value={properties.name}
							classes="flex-1 space-y-1"
						/>

						<CustomValueInput
							label="Virtual Devices"
							placeholder="1"
							bind:value={properties.vdev.count}
							classes="flex-1 space-y-1"
							type="number"
						></CustomValueInput>

						<div class="flex-1 space-y-1">
							<Label class="w-24 text-sm whitespace-nowrap" for="raid"
								>Redundancy
								<span class="font-semibold text-green-500 {properties.usable ? '' : 'hidden'}"
									>{`(${formatBytesBinary(properties.usable)})`}</span
								></Label
							>

							<Select.Root
								type="single"
								bind:value={properties.raid}
								onValueChange={() => {
									setRedundancyAvailability();
								}}
							>
								<Select.Trigger class="w-full">
									{properties.raid
										? raidTypes.find((rt) => rt.value === properties.raid)?.label
										: 'Select Redundancy'}
								</Select.Trigger>
								<Select.Content>
									{#each raidTypes as raidType (raidType.value)}
										{#if raidType.available}
											<Select.Item value={raidType.value} label={raidType.label}>
												{raidType.label}
											</Select.Item>
										{/if}
									{/each}
								</Select.Content>
							</Select.Root>
						</div>
					</Card.Content>

					<Card.Content class="flex flex-col gap-4 ">
						<div id="vdev-containers">
							<Label>VDEVs</Label>
							<ScrollArea class="w-full rounded-md whitespace-nowrap" orientation="horizontal">
								<div
									class="bg-muted mt-1 flex w-full items-center justify-center gap-7 overflow-hidden rounded-lg border-y border-none p-4 pr-4"
								>
									{#each Array(properties.vdev.count) as _, i (i)}
										<div class="relative flex flex-col">
											{@render vdevErrors(i)}
											<div
												class={`bg-primary/10 dark:bg-background relative h-28 w-48 shrink-0 overflow-auto rounded-lg p-2 ${getVdevErrors(i) ? 'border border-yellow-700 ' : ''}`}
												use:dropzone={{
													on_dropzone: (_: unknown, event: DragEvent) => handleDropToVdev(i, event),
													dragover_class: 'droppable'
												}}
											>
												{#if !vdevContains(i)}
													<div
														class="text-muted-foreground/60 flex h-full flex-col items-center justify-center gap-1"
													>
														<span class="text-muted-foreground/60">{i + 1}</span>
														<span>Drop disks here</span>
													</div>
												{:else}
													<div class="flex h-full flex-wrap items-center justify-center gap-2">
														{@render vdevContainer(i)}
													</div>
												{/if}
											</div>
										</div>
									{/each}
								</div></ScrollArea
							>
						</div>
					</Card.Content>

					<Card.Content class="flex flex-col gap-4">
						<div id="disk-containers">
							<Label>Disks &amp; Partitions</Label>
							<Accordion.Root
								type="multiple"
								bind:value={accordionOpen}
								class="mt-1 overflow-hidden rounded-lg border"
							>
								{#each diskSections as section, i (section.key)}
									<Accordion.Item
										value={section.key}
										class={i < diskSections.length - 1 ? 'border-b' : 'border-b-0'}
									>
										<Accordion.Trigger class="px-4 hover:no-underline">
											<div class="flex items-center gap-2 text-sm font-medium">
												{#if section.key === 'HDD'}
													<span class="icon-[mdi--harddisk] h-4 w-4 text-green-500"></span>
												{:else if section.key === 'SSD'}
													<span class="icon-[icon-park-outline--ssd] h-4 w-4 text-blue-500"></span>
												{:else if section.key === 'NVMe'}
													<span class="icon-[bi--nvme] h-4 w-4 text-blue-500"></span>
												{:else if section.key === 'Partitions'}
													<span
														class="icon-[ant-design--partition-outlined] h-4 w-4 rotate-90 text-blue-500"
													></span>
												{/if}
												{section.key}
												<span class="text-muted-foreground text-xs font-normal"
													>({section.count} available)</span
												>
											</div>
										</Accordion.Trigger>
										<Accordion.Content class="px-4 pb-3">
											{#if section.key === 'Partitions'}
												{@render partitionsContainer()}
											{:else}
												{@render diskContainer(section.key)}
											{/if}
										</Accordion.Content>
									</Accordion.Item>
								{/each}
							</Accordion.Root>
						</div>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content class="mt-3" value="tab-options">
				<Card.Root class="min-h-[20vh] border-none pb-6">
					<Card.Content class="flex flex-col gap-4 p-4 pb-0!">
						<div transition:slide class="grid grid-cols-1 gap-4">
							<div class="flex-1 space-y-1.5">
								<Label for="comment">Comment</Label>
								<Textarea
									id="comment"
									placeholder="Comments about the pool"
									bind:value={properties.props.comment}
								/>
							</div>

							<div transition:slide class="grid grid-cols-1 items-center gap-4 md:grid-cols-3">
								<CustomValueInput
									type="text"
									label="Mount Point"
									placeholder="/tank"
									bind:value={properties.mount}
									classes="flex-1 space-y-1"
								></CustomValueInput>

								<div class="col-span-2 flex items-center gap-6 md:mt-4">
									<CustomCheckbox
										label="Force Create"
										bind:checked={properties.force}
										classes="flex items-center gap-2"
									></CustomCheckbox>

									<CustomCheckbox
										label="Advanced"
										bind:checked={properties.advanced}
										classes="flex items-center gap-2"
									></CustomCheckbox>
								</div>
							</div>
						</div>

						{#if properties.advanced}
							<div transition:slide class="grid grid-cols-1 gap-4 md:grid-cols-3">
								<SimpleSelect
									label="AShift"
									placeholder="Select ASHIFT"
									options={[
										{ value: '9', label: '9' },
										{ value: '10', label: '10' },
										{ value: '11', label: '11' },
										{ value: '12', label: '12' },
										{ value: '13', label: '13' },
										{ value: '14', label: '14' },
										{ value: '15', label: '15' },
										{ value: '16', label: '16' }
									]}
									bind:value={properties.props.ashift}
									onChange={(value) => (properties.props.ashift = value)}
								/>

								<SimpleSelect
									label="Auto Expand"
									placeholder="Select Auto Expand"
									options={[
										{ value: 'on', label: 'Yes' },
										{ value: 'off', label: 'No' }
									]}
									bind:value={properties.props.autoexpand}
									onChange={(value) => (properties.props.autoexpand = value)}
								/>

								<SimpleSelect
									label="Auto Trim"
									placeholder="Select Auto Trim"
									options={[
										{ value: 'on', label: 'Yes' },
										{ value: 'off', label: 'No' }
									]}
									bind:value={properties.props.autotrim}
									onChange={(value) => (properties.props.autotrim = value)}
								/>

								<SimpleSelect
									label="Delegation"
									placeholder="Select Delegation"
									options={[
										{ value: 'on', label: 'Yes' },
										{ value: 'off', label: 'No' }
									]}
									bind:value={properties.props.delegation}
									onChange={(value) => (properties.props.delegation = value)}
								/>

								<SimpleSelect
									label="Fail Mode"
									placeholder="Select Fail Mode"
									options={[
										{ value: 'continue', label: 'Continue' },
										{ value: 'wait', label: 'Wait' },
										{ value: 'panic', label: 'Panic' }
									]}
									bind:value={properties.props.failmode}
									onChange={(value) => (properties.props.failmode = value)}
								/>

								{#if spares && spares.length > 0 && properties.raid !== 'stripe'}
									<div class="h-full space-y-1">
										<Label class="w-24 text-sm whitespace-nowrap">Spares</Label>
										<Select.Root
											type="multiple"
											bind:value={properties.props.spares}
											onValueChange={(value) => {
												properties.props.spares = value as string[];
											}}
										>
											<Select.Trigger class="w-full">
												{#if properties.props.spares.length > 0}
													<span>
														{properties.props.spares.join(', ')}
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

									{#if properties.props.spares.length > 0}
										<SimpleSelect
											label="Auto Replace"
											placeholder="Select Auto Replace"
											options={[
												{ value: 'on', label: 'Yes' },
												{ value: 'off', label: 'No' }
											]}
											bind:value={properties.props.autoreplace}
											onChange={(value) => (properties.props.autoreplace = value)}
										/>
									{/if}
								{/if}
							</div>
						{/if}
					</Card.Content>
				</Card.Root>
			</Tabs.Content>
		</Tabs.Root>

		<Dialog.Footer class="flex justify-between gap-2">
			<div class="flex gap-2">
				<Button
					size="sm"
					class="h-8 w-full lg:w-28"
					onclick={() => {
						makePool();
					}}
				>
					{#if properties.creating}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					{:else}
						Create
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
