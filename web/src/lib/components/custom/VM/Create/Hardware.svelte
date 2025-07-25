<script lang="ts">
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { VM } from '$lib/types/vm/vm';
	import { getCache } from '$lib/utils/http';
	import { getPCIDeviceId } from '$lib/utils/system/pci';
	import Icon from '@iconify/svelte';
	import humanFormat from 'human-format';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		sockets: number;
		cores: number;
		threads: number;
		memory: number;
		devices: PCIDevice[];
		pptDevices: PPTDevice[];
		passthroughIds: number[];
		vms: VM[];
		pinnedCPUs: number[];
	}

	let {
		sockets = $bindable(),
		cores = $bindable(),
		threads = $bindable(),
		memory = $bindable(),
		devices = $bindable(),
		pptDevices = $bindable(),
		passthroughIds = $bindable(),
		pinnedCPUs = $bindable(),
		vms
	}: Props = $props();

	let humanSize = $state('1024 M');
	$effect(() => {
		try {
			const p = humanFormat.parse.raw(humanSize);
			memory = p.factor * p.value;
		} catch {
			memory = 1024;
		}
	});

	let checkboxItems = $derived.by(() =>
		devices.map((device) => {
			const raw = getPCIDeviceId(device)
				.replace(/pci\d+:/, '')
				.replace(/:/g, '/');
			const existing = pptDevices.find((p) => p.deviceID === raw);
			return { device, pptId: existing?.id.toString() ?? raw, deviceId: raw };
		})
	);

	let selectedPptIds = $state<string[]>([]);
	let cpuInfo: CPUInfo | null = $state(getCache('cpuInfo') || null);

	function toggle(id: string, on: boolean) {
		selectedPptIds = on ? [...selectedPptIds, id] : selectedPptIds.filter((x) => x !== id);
		passthroughIds = selectedPptIds.map((x) => parseInt(x));
	}

	let pinnedIndices = $derived.by(() => {
		return vms.flatMap((vm, index) => (vm.cpuPinning ? vm.cpuPinning.map((id) => id) : []));
	});

	let vCPUs = $derived(sockets * cores * threads);

	function pinCPU(index: number) {
		if (pinnedCPUs.includes(index)) {
			pinnedCPUs = pinnedCPUs.filter((cpu) => cpu !== index);
		} else {
			if (pinnedCPUs.length >= vCPUs) {
				toast.info(`You can only pin up to ${vCPUs} vCPU${vCPUs > 1 ? 's' : ''}`, {
					position: 'bottom-center'
				});
				return;
			}
			pinnedCPUs = [...pinnedCPUs, index];
		}
	}

	$effect(() => {
		if (pinnedCPUs.length > vCPUs) {
			pinnedCPUs = pinnedCPUs.slice(0, vCPUs);
		}

		let totalPinned = pinnedIndices.length + pinnedCPUs.length;

		if (totalPinned === cpuInfo?.logicalCores) {
			pinnedCPUs = pinnedCPUs.slice(0, -1);
			toast.info('At least one CPU must be left unpinned', {
				position: 'bottom-center'
			});
		}
	});
</script>

<div class="flex flex-col gap-4 p-4">
	<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
		<CustomValueInput
			label="CPU Sockets"
			placeholder="1"
			type="number"
			bind:value={sockets}
			classes="flex-1 space-y-1.5"
		/>
		<CustomValueInput
			label="CPU Cores"
			placeholder="1"
			type="number"
			bind:value={cores}
			classes="flex-1 space-y-1.5"
		/>
		<CustomValueInput
			label="CPU Threads"
			placeholder="1"
			type="number"
			bind:value={threads}
			classes="flex-1 space-y-1.5"
		/>
		<CustomValueInput
			label="Memory Size"
			placeholder="10G"
			bind:value={humanSize}
			classes="flex-1 space-y-1.5"
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
						{#if pinnedIndices.includes(index)}
							<Icon icon="iconoir:cpu" class="h-5 w-5 cursor-pointer text-red-600" />
						{:else}
							<Icon
								icon="iconoir:cpu"
								class={`h-5 w-5 cursor-pointer
                                ${pinnedCPUs.includes(index) ? 'text-yellow-600' : 'text-green-400'}`}
								onclick={() => pinCPU(index)}
							/>
						{/if}
					{/each}
				</div>
			</ScrollArea>
		{/if}
	</div>

	{#if pptDevices && pptDevices.length > 0}
		<p class="font-medium">PCI Passthrough</p>
		<div class="border p-4">
			<ScrollArea orientation="vertical" class="h-full w-full">
				{#each checkboxItems as item (item.pptId)}
					<div class="mb-3 border p-4">
						<div class="flex items-start space-x-3">
							<Checkbox
								id={item.pptId}
								data-cbid={item.pptId}
								checked={selectedPptIds.includes(item.pptId)}
								onCheckedChange={(v: boolean | 'indeterminate') => {
									if (typeof v === 'boolean') toggle(item.pptId, v);
								}}
							/>
							<div class="grid gap-1.5 leading-none">
								<Label for={item.pptId} class="text-sm font-medium">
									<!-- {item.device.names.device} — {item.device.names.vendor} -->
									{`${item.device.names.device} — ${item.device.names.vendor}`}
								</Label>
								<p class="text-muted-foreground text-sm">
									<!-- pci{item.device.domain}:{item.device.bus}:{item.device.device}:{item.device
										.function} -->
									{`pci${item.device.domain}:${item.device.bus}:${item.device.device}:${item.device.function}`}
								</p>
							</div>
						</div>
					</div>
				{/each}
			</ScrollArea>
		</div>
	{/if}
</div>
