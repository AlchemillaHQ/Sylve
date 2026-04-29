<script lang="ts">
	import { modifyCPU } from '$lib/api/jail/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { Jail } from '$lib/types/jail/jail';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail | undefined;
		reload: boolean;
		cpu: CPUInfo;
	}

	let { open = $bindable(), jail, reload = $bindable(), cpu }: Props = $props();
	let cores = $derived(jail?.cores || 1);

	async function modify() {
		let error: string = '';

		if (cores < 1) {
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

		const response = await modifyCPU(jail?.ctId || 0, cores);
		reload = true;
		if (response.error) {
			toast.error('CPU cores update failed', {
				position: 'bottom-center'
			});
		} else {
			toast.success('CPU cores updated', {
				position: 'bottom-center'
			});

			open = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/4 overflow-hidden p-6 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			cores = jail?.cores || 1;
		}}
		onClose={() => {
			cores = jail?.cores || 1;
			open = false;
		}}
	>
		<Dialog.Header class="">
			<Dialog.Title>
				<SpanWithIcon icon="icon-[solar--cpu-bold]" size="h-5 w-5" gap="gap-2" title="CPU" />
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput placeholder="1" bind:value={cores} classes="flex-1 space-y-1" />

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">Save</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
