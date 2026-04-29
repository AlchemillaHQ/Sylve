<script lang="ts">
	import type { Jail } from '$lib/types/jail/jail';
	import type { APIResponse } from '$lib/types/common';

	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	import {
		modifyAdditionalOptions,
		modifyDevFSRules,
		modifyFstab,
		modifyMetadata,
		modifyResolvConf
	} from '$lib/api/jail/jail';

	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	type DialogType = 'fstab' | 'resolvConf' | 'devfsRules' | 'additionalOptions' | 'metadata';

	type MetadataValue = {
		meta: string;
		env: string;
	};

	interface Props {
		open: boolean;
		type: DialogType;
		jail: Jail;
		reload: boolean;
	}

	let { open = $bindable(), type, jail, reload = $bindable() }: Props = $props();

	const related = {
		fstab: {
			icon: 'icon-[material-symbols--table-outline]',
			title: 'FSTab Entries',
			description: 'Manage the fstab entries for this jail',
			initial: () => jail.fstab || '',
			saveFn: modifyFstab
		},
		resolvConf: {
			icon: 'icon-[mdi--dns]',
			title: '/etc/resolv.conf',
			description: 'Manage resolv.conf content for this jail',
			initial: () => jail.resolvConf || '',
			saveFn: modifyResolvConf
		},
		devfsRules: {
			icon: 'icon-[material-symbols--settings-outline]',
			title: 'DevFS Ruleset',
			description: 'Manage the devfs ruleset for this jail',
			initial: () => jail.devfsRuleset || '',
			saveFn: modifyDevFSRules
		},
		additionalOptions: {
			icon: 'icon-[material-symbols--settings-outline]',
			title: 'Additional Options',
			description: 'Manage additional options for this jail',
			initial: () => jail.additionalOptions || '',
			saveFn: modifyAdditionalOptions
		},
		metadata: {
			icon: 'icon-[material-symbols--info-outline]',
			title: 'Metadata',
			description: 'Meta and Env key-value pairs'
		}
	} as const;

	let info = $derived(related[type]);
	let textValue = $state('');
	let metadataValue = $state<MetadataValue>({ meta: '', env: '' });

	$effect(() => {
		if (type === 'metadata') {
			metadataValue = {
				meta: jail.metadataMeta || '',
				env: jail.metadataEnv || ''
			};
		} else {
			textValue = related[type].initial();
		}
	});

	async function save() {
		let result: APIResponse;

		if (type === 'metadata') {
			result = await modifyMetadata(jail.ctId, metadataValue.meta, metadataValue.env);
		} else {
			result = await related[type].saveFn(jail.ctId, textValue);
		}

		if (result.status === 'error') {
			handleAPIError(result);
			toast.error('Failed to save changes', { position: 'bottom-center' });
			return;
		}

		toast.success('Changes saved', { position: 'bottom-center' });
		reload = !reload;
		open = false;
	}

	function reset() {
		if (type === 'metadata') {
			metadataValue = {
				meta: jail.metadataMeta || '',
				env: jail.metadataEnv || ''
			};
		} else {
			textValue = related[type].initial();
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/2 overflow-hidden p-6 lg:max-w-2xl"
		showResetButton={true}
		onReset={reset}
		onClose={() => {
			reset();
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon={info.icon} size="h-5 w-5" gap="gap-2" title={info.title} />
			</Dialog.Title>
		</Dialog.Header>

		{#if type === 'metadata'}
			<div class="space-y-4">
				<CustomValueInput
					placeholder="Meta (Key=Value per line)"
					bind:value={metadataValue.meta}
					type="textarea"
					classes="flex-1 space-y-1.5"
					textAreaClasses="w-full h-32"
				/>

				<CustomValueInput
					placeholder="ENV (Key=Value per line)"
					bind:value={metadataValue.env}
					type="textarea"
					classes="flex-1 space-y-1.5"
					textAreaClasses="w-full h-32"
				/>
			</div>
		{:else}
			<CustomValueInput
				placeholder={info.description}
				bind:value={textValue}
				classes="flex-1 space-y-1.5"
				textAreaClasses="w-full h-60"
				type="textarea"
			/>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} size="sm">Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
