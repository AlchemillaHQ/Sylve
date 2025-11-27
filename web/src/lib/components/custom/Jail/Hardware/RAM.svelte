<script lang="ts">
	import { modifyRAM } from '$lib/api/jail/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';

	import humanFormat from 'human-format';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		ram: RAMInfo;
		jail: Jail | undefined;
		reload: boolean;
	}

	let { open = $bindable(), ram, jail, reload = $bindable() }: Props = $props();
	let options = {
		ram: humanFormat(jail?.memory || 1)
	};

	let properties = $state(options);

	async function modify() {
		let bytes: number = 0;
		let error: string = '';

		try {
			bytes = humanFormat.parse(properties.ram);
			bytes = parseInt(bytes.toString(), 10);
		} catch (e) {
			error = 'Invalid RAM value';
		}

		if (bytes <= 0) {
			error = 'RAM value must be greater than 0';
		}

		if (bytes > ram.total - 1024 * 1024 * 1024 || bytes > ram.total) {
			if (bytes > ram.total) {
				error = 'RAM value exceeds available memory';
			} else if (bytes > ram.total - 1024 * 1024 * 1024) {
				error = 'RAM value is too high, at least 1 GiB must be reserved for the host';
			}
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		if (jail) {
			const response = await modifyRAM(jail.ctId, bytes);
			reload = true;
			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to modify RAM', {
					position: 'bottom-center'
				});
			} else {
				toast.success('RAM modified', {
					position: 'bottom-center'
				});
				open = false;
			}
		} else {
			toast.error('Jail not found', {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-1/4 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[ri--ram-fill] h-5 w-5"></span>
					<span>RAM</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
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

		<CustomValueInput
			label={''}
			placeholder="1.0 GiB"
			bind:value={properties.ram}
			classes="flex-1 space-y-1"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
