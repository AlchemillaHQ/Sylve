<script lang="ts">
	import { modifyCPU } from '$lib/api/jail/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		cpu: CPUInfo;
		node: string;
		onSaved: () => void | Promise<void>;
	}

	let { open = $bindable(), jail, cpu, node, onSaved }: Props = $props();
	let cores = $derived(jail.cores || 1);
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		let error: string = '';

		if (!Number.isInteger(cores)) {
			error = 'CPU cores must be a whole number';
		} else if (cores < 1) {
			error = 'CPU cores must be at least 1';
		} else if (cores > cpu.logicalCores) {
			error = `CPU cores larger than logical cores (${cpu.logicalCores})`;
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyCPU(jail.ctId, cores, { hostname: node });
			if (response.error) {
				handleAPIError(response);
				toast.error('CPU cores update failed', {
					position: 'bottom-center'
				});
				return;
			}

			await onSaved();
			toast.success('CPU cores updated', {
				position: 'bottom-center'
			});
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/4 overflow-hidden p-6 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			if (!saving) cores = jail.cores || 1;
		}}
		onClose={() => {
			if (saving) return;
			cores = jail.cores || 1;
			open = false;
		}}
	>
		<Dialog.Header class="">
			<Dialog.Title>
				<SpanWithIcon icon="icon-[solar--cpu-bold]" size="h-5 w-5" gap="gap-2" title="CPU" />
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput type="number" placeholder="1" bind:value={cores} classes="flex-1 space-y-1" />

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm" disabled={saving} aria-busy={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
