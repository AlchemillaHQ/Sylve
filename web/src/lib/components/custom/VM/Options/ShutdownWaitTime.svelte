<script lang="ts">
	import { modifyShutdownWaitTime } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), vm, reload = $bindable(false) }: Props = $props();

	let shutdownWaitTime = $state(vm.shutdownWaitTime);

	async function modify() {
		if (!vm) return;

		if (isNaN(Number(shutdownWaitTime)) || Number(shutdownWaitTime) < 0) {
			toast.error('Shutdown Wait Time must be a non-negative number', {
				position: 'bottom-center'
			});
			return;
		}

		const response = await modifyShutdownWaitTime(vm.rid, Number(shutdownWaitTime));
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify shutdown wait time', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified shutdown wait time', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
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
			label={'Start Order'}
			placeholder="1"
			bind:value={shutdownWaitTime}
			classes="flex-1 space-y-1.5"
			type="number"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
