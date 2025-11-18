<script lang="ts">
	import { modifyCPU } from '$lib/api/vm/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { VM } from '$lib/types/vm/vm';
	import { getCache, handleAPIError } from '$lib/utils/http';

	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		vm: VM | null;
		vms: VM[];
	}

	let cpuInfo: CPUInfo | null = $state(getCache('cpuInfo') || null);
	let { open = $bindable(), vm, vms }: Props = $props();
	let options = {
		cpu: {
			sockets: vm?.cpuSockets || 1,
			cores: vm?.cpuCores || 1,
			threads: vm?.cpuThreads || 1,
			pinning: vm?.cpuPinning || []
		}
	};

	let properties = $state(options);
	let otherVmPinnedIndices = $derived.by(() => {
		return vms.filter((v) => v.id !== vm?.id).flatMap((v) => v.cpuPinning || []);
	});

	let allPinnedIndices = $derived.by(() => {
		return [...otherVmPinnedIndices, ...properties.cpu.pinning];
	});

	let vCPUs = $derived(properties.cpu.sockets * properties.cpu.cores * properties.cpu.threads);

	function pinCPU(index: number) {
		if (properties.cpu.pinning.includes(index)) {
			properties.cpu.pinning = properties.cpu.pinning.filter((cpu) => cpu !== index);
		} else {
			if (properties.cpu.pinning.length >= vCPUs) {
				toast.info(`You can only pin up to ${vCPUs} vCPU${vCPUs > 1 ? 's' : ''}`, {
					position: 'bottom-center'
				});
				return;
			}
			properties.cpu.pinning = [...properties.cpu.pinning, index];
		}
	}

	function unpinCPU(index: number) {
		if (properties.cpu.pinning.includes(index)) {
			properties.cpu.pinning = properties.cpu.pinning.filter((cpu) => cpu !== index);
		} else {
			toast.info(`CPU ${index} is not pinned by this VM`, {
				position: 'bottom-center'
			});
		}
	}

	$effect(() => {
		if (properties.cpu.pinning.length > vCPUs) {
			properties.cpu.pinning = properties.cpu.pinning.slice(0, vCPUs);
		}

		let totalPinned = allPinnedIndices.length;

		if (totalPinned === cpuInfo?.logicalCores) {
			properties.cpu.pinning = properties.cpu.pinning.slice(0, -1);
			toast.info('At least one CPU must be left unpinned', {
				position: 'bottom-center'
			});
		}
	});

	async function modify() {
		if (vm) {
			const response = await modifyCPU(
				vm.vmId,
				parseInt(properties.cpu.sockets.toString(), 10),
				parseInt(properties.cpu.cores.toString(), 10),
				parseInt(properties.cpu.threads.toString(), 10),
				properties.cpu.pinning.map((x) => parseInt(x.toString(), 10))
			);

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to modify CPU', {
					position: 'bottom-center'
				});
			} else {
				toast.success('vCPUs modified', {
					position: 'bottom-center'
				});
				open = false;
			}
		} else {
			toast.error('VM not found', {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-1/2 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[solar--cpu-bold] h-5 w-5"></span>

					<span>CPU</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4"
						onclick={() => {
							properties = options;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-3 gap-4">
			<CustomValueInput
				label="Sockets"
				bind:value={properties.cpu.sockets}
				type="number"
				classes="space-y-1"
				placeholder="1"
			/>

			<CustomValueInput
				label="Cores"
				bind:value={properties.cpu.cores}
				type="number"
				classes="space-y-1"
				placeholder="1"
			/>

			<CustomValueInput
				label="Threads"
				bind:value={properties.cpu.threads}
				type="number"
				classes="space-y-1"
				placeholder="1"
			/>
		</div>

		<div>
			{#if cpuInfo}
				<Label class="mb-4 flex justify-center">CPU Pinning</Label>
				<ScrollArea orientation="vertical" class="h-full w-full max-w-full">
					<div
						class="grid grid-cols-6 justify-items-center gap-1 text-xs sm:grid-cols-8 md:grid-cols-10"
					>
						{#each Array(cpuInfo.logicalCores).fill(0) as _, index (index)}
							{#if otherVmPinnedIndices.includes(index)}
								<span
									class="icon-[iconoir--cpu] h-5 w-5 cursor-pointer text-red-600"
									onclick={() => unpinCPU(index)}
								></span>
							{:else}
								<span
									class={`icon-[iconoir--cpu] h-5 w-5 cursor-pointer ${properties.cpu.pinning.includes(index) ? 'text-yellow-600' : 'text-green-400'}`}
									onclick={() => pinCPU(index)}
								></span>
							{/if}
						{/each}
					</div>
				</ScrollArea>
			{/if}
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
