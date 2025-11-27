<script lang="ts">
	import { modifyCPU } from '$lib/api/jail/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Jail } from '$lib/types/jail/jail';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail | undefined;
		reload: boolean;
	}

	let { open = $bindable(), jail, reload = $bindable() }: Props = $props();
	let cores = $derived(jail?.cores || 1);

	async function modify() {
		let error: string = '';

		if (cores < 1) {
			error = 'CPU cores must be at least 1';
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
			toast.error(response.error, {
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
	<Dialog.Content class="w-1/4 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
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
						class="h-4 "
						onclick={() => {
							cores = jail?.cores || 1;
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
							cores = jail?.cores || 1;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput label={''} placeholder="1" bind:value={cores} classes="flex-1 space-y-1" />

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
