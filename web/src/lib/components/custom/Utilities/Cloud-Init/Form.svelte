<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { CloudInitTemplate } from '$lib/types/utilities/cloud-init';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { cloudInitPlaceholders, generateTemplate } from '$lib/utils/utilities/cloud-init';
	import SimpleSelect from '../../SimpleSelect.svelte';
	import { toast } from 'svelte-sonner';
	import { createTemplate, updateTemplate } from '$lib/api/utilities/cloud-init';

	interface Props {
		open: boolean;
		reload: boolean;
		template: CloudInitTemplate | null;
	}

	let { open = $bindable(), reload = $bindable(), template }: Props = $props();
	let isEdit = $derived(!!template);

	// svelte-ignore state_referenced_locally
	let options = {
		name: template?.name || '',
		user: template?.user || '',
		meta: template?.meta || '',
		networkConfig: template?.networkConfig || ''
	};

	let properties = $state(options);

	let templateSelector = $state({
		open: false,
		current: ''
	});

	async function save() {
		if (properties.name.trim() === '') {
			toast.error('Name is required', {
				position: 'bottom-center'
			});
			return;
		}

		if (properties.user.trim() === '') {
			toast.error('User Data is required', {
				position: 'bottom-center'
			});
			return;
		}

		if (properties.meta.trim() === '') {
			toast.error('Meta Data is required', {
				position: 'bottom-center'
			});
			return;
		}

		const payload: Partial<CloudInitTemplate> = {
			id: template?.id || undefined,
			name: properties.name,
			user: properties.user,
			meta: properties.meta,
			networkConfig: properties.networkConfig
		};

		let response = null;

		if (isEdit) {
			response = await updateTemplate(payload);
		} else {
			response = await createTemplate(payload);
		}

		reload = true;

		if (response.status === 'success') {
			toast.success(`Template ${properties.name} ${isEdit ? 'updated' : 'created'}`, {
				position: 'bottom-center'
			});
			open = false;
		} else {
			toast.error(`Failed to ${isEdit ? 'update' : 'create'} template ${properties.name}`, {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[90vh] flex-col p-5 overflow-hidden"
		showCloseButton={true}
		showResetButton={true}
		onClose={() => {
			properties = options;
			open = false;
		}}
		onReset={() => {
			properties = options;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon="icon-[mdi--cloud-upload-outline]" size="h-5 w-5" gap="gap-2" title={isEdit ? `Edit Template - ${template?.name}` : 'Create Template'} />
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex-1 overflow-y-auto space-y-4 pr-2">
			<CustomValueInput bind:value={properties.name} placeholder="Name" classes="space-y-1" />
			<CustomValueInput
				bind:value={properties.user}
				placeholder={cloudInitPlaceholders.data}
				classes="space-y-1"
				label="User Data"
				type="textarea"
				textAreaClasses="min-h-32 max-h-64"
				topRightButton={{
					icon: 'icon-[mingcute--ai-line]',
					tooltip: 'Insert a pre-made template',
					function: async () => {
						templateSelector.open = true;
						return '';
					}
				}}
			/>
		</div>

		<CustomValueInput
			bind:value={properties.meta}
			placeholder={cloudInitPlaceholders.metadata}
			classes="space-y-1"
			label="Meta Data"
			type="textarea"
			textAreaClasses="min-h-32 max-h-64"
		/>

		<CustomValueInput
			bind:value={properties.networkConfig}
			placeholder={cloudInitPlaceholders.networkConfig}
			classes="space-y-1"
			label="Network Config"
			type="textarea"
			textAreaClasses="min-h-32 max-h-64"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={save} type="submit" size="sm">
					{#if isEdit}
						Save Changes
					{:else}
						Create Template
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

{#if templateSelector.open}
	<Dialog.Root bind:open={templateSelector.open}>
		<Dialog.Content
			class="overflow-hidden p-5 max-w-[320px]!"
			showCloseButton={true}
			onClose={() => {
				templateSelector.open = false;
			}}
		>
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon icon="icon-[mdi--cloud-upload-outline]" size="h-5 w-5" gap="gap-2" title="Select a Template" />
				</Dialog.Title>
			</Dialog.Header>

			<SimpleSelect
				options={[
					{ label: 'Simple', value: 'Simple' },
					{ label: 'FreeBSD with Static IP', value: 'FreeBSD Network Config' },
					{ label: 'Debian with Static IP', value: 'Debian Network Config' },
					{ label: 'Docker', value: 'Docker' }
				]}
				placeholder="Select a Template"
				bind:value={templateSelector.current}
				onChange={(e: string) => {
					const template = generateTemplate(e);
					properties = {
						name: e,
						user: template.user,
						meta: template.meta,
						networkConfig: template.networkConfig
					};

					templateSelector.open = false;
				}}
			/>
		</Dialog.Content>
	</Dialog.Root>
{/if}
