<script lang="ts">
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { CPUPin, SimpleVm } from '$lib/types/vm/vm';
	import {
		formatBytesBinary,
		normalizeSizeInputExact,
		parseSizeInputToBytes
	} from '$lib/utils/bytes';
	import CPUSelector from '../Extra/CPUSelector.svelte';

	interface Props {
		node: string;
		sockets: number;
		cores: number;
		threads: number;
		memory: number;
		devices: PCIDevice[];
		pptDevices: PPTDevice[];
		passthroughIds: number[];
		vms: SimpleVm[];
		pinnedCPUs: CPUPin[];
		isPinningOpen: boolean;
	}

	let {
		node,
		sockets = $bindable(),
		cores = $bindable(),
		threads = $bindable(),
		memory = $bindable(),
		devices = $bindable(),
		pptDevices = $bindable(),
		passthroughIds = $bindable(),
		pinnedCPUs = $bindable(),
		vms,
		isPinningOpen = $bindable()
	}: Props = $props();

	let humanSize = $state(formatBytesBinary(memory || 1024 * 1024 * 1024));
	let coreSelectionLimit = $derived.by(() => sockets * cores * threads);

	function updateMemory(value: string | number) {
		const bytes = parseSizeInputToBytes(String(value));
		memory = bytes ?? 1024 * 1024 * 1024;
	}

	let checkboxItems = $derived.by(() =>
		pptDevices
			.filter((mapping) => mapping.domain === 0)
			.flatMap((mapping) => {
				const [bus, deviceID, deviceFunction] = mapping.deviceID.split('/').map(Number);
				const device = devices.find(
					(candidate) =>
						candidate.domain === mapping.domain &&
						candidate.bus === bus &&
						candidate.device === deviceID &&
						candidate.function === deviceFunction
				);
				return device ? [{ device, pptId: mapping.id.toString() }] : [];
			})
	);

	let selectedPptIds = $state<string[]>([]);

	function toggle(id: string, on: boolean) {
		selectedPptIds = on ? [...selectedPptIds, id] : selectedPptIds.filter((x) => x !== id);
		passthroughIds = selectedPptIds.map(Number).filter((mappingID) => mappingID > 0);
	}
</script>

<div class="flex flex-col gap-4 p-4">
	<div class="grid grid-cols-1 gap-4 lg:grid-cols-1">
		<div class="grid grid-cols-3 gap-4">
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
		</div>

		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2 lg:items-end">
			<div>
				<CPUSelector bind:open={isPinningOpen} bind:pinnedCPUs {node} {vms} {coreSelectionLimit} />
			</div>

			<CustomValueInput
				label="Memory Size"
				placeholder="10G"
				bind:value={humanSize}
				classes="flex-1 space-y-1.5"
				onChange={updateMemory}
				onBlur={() => {
					const normalized = normalizeSizeInputExact(humanSize);
					if (normalized !== null) {
						humanSize = normalized;
					}
				}}
			/>
		</div>
	</div>

	{#if checkboxItems.length > 0}
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
