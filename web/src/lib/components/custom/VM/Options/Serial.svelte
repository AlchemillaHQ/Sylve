<script lang="ts">
	import { modifySerialConsole } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
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

	async function setSerial(enable: boolean) {
		const response = await modifySerialConsole(vm.vmId, enable);
		reload = true;
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify serial console', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified serial console', {
			position: 'bottom-center'
		});

		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[mdi--console] mr-2 h-5 w-5"></span>

					<span>Serial Console</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if vm?.serial}
			<span class="text-sm">
				This VM currently has serial console access enabled. You can disable it using the button
				below.
			</span>
		{:else}
			<span class="text-sm">
				This VM currently has serial console access disabled. You can enable it using the button
				below.
			</span>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if !vm?.serial}
					<Button onclick={() => setSerial(true)} type="submit" size="sm">Enable</Button>
				{:else}
					<Button onclick={() => setSerial(false)} type="submit" size="sm">Disable</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
