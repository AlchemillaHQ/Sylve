<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
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
		meta: template?.meta || ''
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
			meta: properties.meta
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
	<Dialog.Content class="flex max-h-[90vh] flex-col p-5 overflow-hidden">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--cloud-upload-outline] h-5 w-5"></span>
					{#if isEdit}
						<span>Edit Template - {template?.name}</span>
					{:else}
						<span>Create Template</span>
					{/if}
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
		<Dialog.Content class="overflow-hidden p-5 max-w-[320px]!">
			<Dialog.Header>
				<div class="flex items-center justify-between">
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--cloud-upload-outline] h-5 w-5"></span>
						<span>Select a Template</span>
					</div>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							templateSelector.open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Header>

			<SimpleSelect
				options={[
					{ label: 'Simple', value: 'simple' },
					{ label: 'Docker', value: 'docker' }
				]}
				placeholder="Select a Template"
				bind:value={templateSelector.current}
				onChange={(e: string) => {
					const template = generateTemplate(e);
					properties = {
						name: e,
						user: template.user,
						meta: template.meta
					};

					templateSelector.open = false;
				}}
			/>
		</Dialog.Content>
	</Dialog.Root>
{/if}
