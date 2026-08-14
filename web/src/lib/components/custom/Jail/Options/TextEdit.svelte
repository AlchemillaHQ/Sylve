<script lang="ts">
	import {
		modifyAdditionalOptions,
		modifyDevFSRules,
		modifyFstab,
		modifyMetadata,
		modifyResolvConf
	} from '$lib/api/jail/options';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	type DialogType = 'fstab' | 'resolvConf' | 'devfsRules' | 'additionalOptions' | 'metadata';

	interface Props {
		open: boolean;
		type: DialogType;
		jail: Jail;
		node: string;
		onSaved: () => void | Promise<void>;
	}

	let { open = $bindable(), type, jail, node, onSaved }: Props = $props();
	const related = {
		fstab: {
			icon: 'icon-[material-symbols--table-outline]',
			title: 'FSTab Entries',
			description: 'Manage the fstab entries for this jail'
		},
		resolvConf: {
			icon: 'icon-[mdi--dns]',
			title: '/etc/resolv.conf',
			description: 'Manage resolv.conf content for this jail'
		},
		devfsRules: {
			icon: 'icon-[material-symbols--settings-outline]',
			title: 'DevFS Ruleset',
			description: 'Manage the DevFS ruleset for this jail'
		},
		additionalOptions: {
			icon: 'icon-[material-symbols--settings-outline]',
			title: 'Additional Options',
			description: 'Manage additional jail.conf options for this jail'
		},
		metadata: {
			icon: 'icon-[material-symbols--info-outline]',
			title: 'Metadata',
			description: 'Manage jail metadata values'
		}
	} as const;

	function initialText(): string {
		switch (type) {
			case 'fstab':
				return jail.fstab || '';
			case 'resolvConf':
				return jail.resolvConf || '';
			case 'devfsRules':
				return jail.devfsRuleset || '';
			case 'additionalOptions':
				return jail.additionalOptions || '';
			case 'metadata':
				return '';
		}
	}

	function initialMetadata() {
		return { meta: jail.metadataMeta || '', env: jail.metadataEnv || '' };
	}

	let info = $derived(related[type]);
	// This dialog is remounted when its option type changes.
	let textValue = $state(initialText());
	let metadataValue = $state(initialMetadata());
	let saving = $state(false);

	function reset() {
		textValue = initialText();
		metadataValue = initialMetadata();
	}

	async function save() {
		if (saving) return;
		saving = true;
		try {
			let result: APIResponse;
			switch (type) {
				case 'fstab':
					result = await modifyFstab(jail.ctId, textValue, { hostname: node });
					break;
				case 'resolvConf':
					result = await modifyResolvConf(jail.ctId, textValue, { hostname: node });
					break;
				case 'devfsRules':
					result = await modifyDevFSRules(jail.ctId, textValue, { hostname: node });
					break;
				case 'additionalOptions':
					result = await modifyAdditionalOptions(jail.ctId, textValue, { hostname: node });
					break;
				case 'metadata':
					result = await modifyMetadata(jail.ctId, metadataValue.meta, metadataValue.env, {
						hostname: node
					});
					break;
			}

			if (result.status === 'error') {
				handleAPIError(result);
				toast.error('Failed to save changes', { position: 'bottom-center' });
				return;
			}

			await onSaved();
			toast.success('Changes saved', { position: 'bottom-center' });
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/2 overflow-hidden p-6 lg:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={reset}
		onClose={() => {
			if (saving) return;
			reset();
			open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		aria-busy={saving}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon={info.icon} size="h-5 w-5" gap="gap-2" title={info.title} />
			</Dialog.Title>
		</Dialog.Header>

		{#if type === 'metadata'}
			<div class="space-y-4">
				<CustomValueInput
					placeholder="Meta"
					bind:value={metadataValue.meta}
					type="textarea"
					classes="flex-1 space-y-1.5"
					textAreaClasses="h-32 w-full"
				/>
				<CustomValueInput
					placeholder="Environment"
					bind:value={metadataValue.env}
					type="textarea"
					classes="flex-1 space-y-1.5"
					textAreaClasses="h-32 w-full"
				/>
			</div>
		{:else}
			<CustomValueInput
				placeholder={info.description}
				bind:value={textValue}
				classes="flex-1 space-y-1.5"
				textAreaClasses="h-60 w-full"
				type="textarea"
			/>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} size="sm" disabled={saving} aria-busy={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					Saving...
				{:else}
					Save
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
