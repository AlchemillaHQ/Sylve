<script lang="ts">
	import { modifyCPU } from '$lib/api/vm/hardware';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { CPUPin, VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import CPUSelector from '../Extra/CPUSelector.svelte';

	interface Props {
		open: boolean;
		node: string;
		vm: VM | null;
		vms: VM[];
		pinnedCPUs: CPUPin[];
		reload: boolean;
	}

	let {
		open = $bindable(),
		node,
		vm,
		vms,
		pinnedCPUs = $bindable(),
		reload = $bindable(false)
	}: Props = $props();

	function initialProperties() {
		return {
			cpu: {
				sockets: vm?.cpuSockets ?? 1,
				cores: vm?.cpuCores ?? 1,
				threads: vm?.cpuThreads ?? 1
			}
		};
	}

	function initialPinnedCPUs(): CPUPin[] {
		return (
			vm?.cpuPinning?.map((pin) => ({
				socket: pin.hostSocket,
				cores: [...pin.hostCpu]
			})) ?? []
		);
	}

	let properties = $state(initialProperties());
	let isPinningOpen = $state(false);
	let saving = $state(false);
	let coreSelectionLimit = $derived.by(
		() =>
			Number(properties.cpu.sockets) * Number(properties.cpu.cores) * Number(properties.cpu.threads)
	);

	function reset() {
		properties = initialProperties();
		pinnedCPUs = initialPinnedCPUs();
	}

	async function modify() {
		if (!vm || saving) {
			if (!vm) toast.error('VM not found', { position: 'bottom-center' });
			return;
		}

		const sockets = Number(properties.cpu.sockets);
		const cores = Number(properties.cpu.cores);
		const threads = Number(properties.cpu.threads);
		if (
			!Number.isSafeInteger(sockets) ||
			!Number.isSafeInteger(cores) ||
			!Number.isSafeInteger(threads) ||
			sockets <= 0 ||
			cores <= 0 ||
			threads <= 0
		) {
			toast.error('Sockets, cores, and threads must be positive whole numbers', {
				position: 'bottom-center'
			});
			return;
		}

		const vcpuCount = sockets * cores * threads;
		if (!Number.isSafeInteger(vcpuCount)) {
			toast.error('CPU topology is too large', { position: 'bottom-center' });
			return;
		}
		const pinnedCount = pinnedCPUs.reduce((total, pin) => total + pin.cores.length, 0);
		if (pinnedCount > vcpuCount) {
			toast.error(`CPU pinning cannot exceed ${vcpuCount} vCPUs`, {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyCPU(vm.rid, sockets, cores, threads, pinnedCPUs, {
				hostname: node
			});
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify CPU', { position: 'bottom-center' });
				return;
			}

			reload = true;
			toast.success(
				response.message === 'no_changes_detected' ? 'No CPU changes needed' : 'vCPUs modified',
				{
					position: 'bottom-center'
				}
			);
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/2 overflow-hidden p-5 lg:max-w-2xl"
		showResetButton={true}
		onReset={reset}
		onClose={() => {
			reset();
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon="icon-[solar--cpu-bold]" size="h-5 w-5" gap="gap-2" title="CPU" />
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-3 gap-4">
			<CustomValueInput
				label="Sockets"
				bind:value={properties.cpu.sockets}
				type="number"
				classes="space-y-1"
				placeholder="1"
				disabled={saving}
			/>

			<CustomValueInput
				label="Cores"
				bind:value={properties.cpu.cores}
				type="number"
				classes="space-y-1"
				placeholder="1"
				disabled={saving}
			/>

			<CustomValueInput
				label="Threads"
				bind:value={properties.cpu.threads}
				type="number"
				classes="space-y-1"
				placeholder="1"
				disabled={saving}
			/>
		</div>

		<div class="grid grid-cols-1 md:grid-cols-2">
			<div>
				<CPUSelector
					bind:open={isPinningOpen}
					bind:pinnedCPUs
					{node}
					{vm}
					{vms}
					{coreSelectionLimit}
				/>
			</div>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
