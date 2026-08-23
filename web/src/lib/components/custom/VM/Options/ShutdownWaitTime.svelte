<script lang="ts">
	import { modifyShutdownWaitTime } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { untrack } from 'svelte';

	interface Props {
		open: boolean;
		node: string;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), node, vm, reload = $bindable(false) }: Props = $props();

	let shutdownWaitTime = $state(untrack(() => vm.shutdownWaitTime));
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		const normalizedWaitTime = Number(shutdownWaitTime);
		if (
			!Number.isSafeInteger(normalizedWaitTime) ||
			normalizedWaitTime < 1 ||
			normalizedWaitTime > 3600
		) {
			toast.error('Shutdown wait time must be a whole number between 1 and 3600 seconds', {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyShutdownWaitTime(vm.rid, normalizedWaitTime, {
				hostname: node
			});
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify shutdown wait time', { position: 'bottom-center' });
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No shutdown wait-time changes needed'
					: 'Modified shutdown wait time',
				{ position: 'bottom-center' }
			);

			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/3 overflow-hidden p-5 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			shutdownWaitTime = vm.shutdownWaitTime;
		}}
		onClose={() => {
			shutdownWaitTime = vm.shutdownWaitTime;
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[basil--power-button-solid]"
					size="h-5 w-5"
					gap="gap-2"
					title="Shutdown Wait Time"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label="Shutdown Wait Time"
			placeholder="10"
			bind:value={shutdownWaitTime}
			classes="flex-1 space-y-1.5"
			type="number"
			disabled={saving}
		/>

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
