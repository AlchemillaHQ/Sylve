<script lang="ts">
	import type { Jail } from '$lib/types/jail/jail';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { modifyAdditionalOptions, modifyDevFSRules, modifyFstab } from '$lib/api/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		type: 'fstab' | 'devfsRules' | 'additionalOptions';
		jail: Jail;
		reload: boolean;
	}

	let { open = $bindable(), type, jail, reload = $bindable() }: Props = $props();
	let related = {
		fstab: {
			icon: 'icon-[material-symbols--table-outline]',
			title: 'FSTab Entries',
			description: 'Manage the fstab entries for this jail',
			initial: jail.fstab || '',
			saveFn: modifyFstab
		},
		devfsRules: {
			icon: 'icon-[material-symbols--settings-outline]',
			title: 'DevFS Ruleset',
			description: 'Manage the devfs ruleset for this jail',
			initial: jail.devfsRuleset || '',
			saveFn: modifyDevFSRules
		},
		additionalOptions: {
			icon: 'icon-[material-symbols--settings-outline]',
			title: 'Additional Options',
			description: 'Manage additional options for this jail',
			initial: jail.additionalOptions || '',
			saveFn: modifyAdditionalOptions
		}
	};

	let info = $derived(related[type]);
	let value = $state(info.initial);

	async function save() {
		const result = await info.saveFn(jail.ctId, value);
		if (result.status === 'error') {
			handleAPIError(result);
			toast.error('Failed to save changes', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Changes saved', {
			position: 'bottom-center'
		});

		reload = !reload;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-1/2 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon {info.icon} h-5 w-5"></span>
					<span>{info.title}</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							value = info.initial;
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
							value = info.initial;
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
			placeholder={info.description}
			bind:value
			classes="flex-1 space-y-1.5"
			textAreaClasses="w-full h-60"
			type="textarea"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={save} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
