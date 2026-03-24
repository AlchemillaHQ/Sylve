<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { convertJailToTemplate, deleteJailTemplate, jailAction } from '$lib/api/jail/jail';
	import CreateJailFromTemplate from '$lib/components/custom/Jail/Template/Create.svelte';
	import ViewJailTemplate from '$lib/components/custom/Jail/Template/View.svelte';
	import CreateVMFromTemplate from '$lib/components/custom/VM/Template/Create.svelte';
	import ViewVMTemplate from '$lib/components/custom/VM/Template/View.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { actionVm, convertVMToTemplate, deleteVMTemplate } from '$lib/api/vm/vm';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { slide } from 'svelte/transition';
	import { toast } from 'svelte-sonner';
	import SidebarElement from './TreeViewCluster.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { handleAPIError } from '$lib/utils/http';

	interface SidebarProps {
		id: string;
		label: string;
		icon: string;
		href?: string;
		state?: 'active' | 'inactive';
		resourceId?: number;
		resourceType?: 'vm' | 'jail' | 'jail-template' | 'vm-template';
		nodeHostname?: string;
		nextGuestId?: number;
		children?: SidebarProps[];
	}

	interface Props {
		item: SidebarProps;
		openIds: Set<string>;
		onToggleId: (id: string) => void;
		nextGuestId?: number;
	}

	let { item, openIds, onToggleId, nextGuestId = 100 }: Props = $props();
	let isOpen = $derived(openIds.has(item.id));

	const handleLabelClick = (e: MouseEvent) => {
		e.preventDefault();
		if (item.href) {
			goto(item.href, { replaceState: false, noScroll: false });
		}
	};

	const handleIconClick = (e: MouseEvent) => {
		e.preventDefault();
		e.stopPropagation();
		if (item.children && item.children.length > 0) {
			onToggleId(item.id);
		}
	};

	const sidebarActive = 'rounded-md bg-muted dark:bg-muted font-inter font-medium';

	function isItemActive(menuItem: SidebarProps, currentUrl: string): boolean {
		if (menuItem.href && currentUrl.startsWith(menuItem.href)) {
			return true;
		}
		if (menuItem.children) {
			return menuItem.children.some((child) => isItemActive(child, currentUrl));
		}
		return false;
	}

	let activeUrl = $derived(page.url.pathname);
	let isActive = $derived(isItemActive(item, activeUrl));
	let hasContextMenu = $derived(
		item.resourceType === 'vm' ||
			item.resourceType === 'jail' ||
			item.resourceType === 'jail-template' ||
			item.resourceType === 'vm-template'
	);
	let lastActiveUrl = $derived.by(() => {
		const segments = activeUrl.split('/');
		return segments[segments.length - 1];
	});
	let createFromTemplateOpen = $state(false);
	let viewTemplateOpen = $state(false);
	let deleteTemplateOpen = $state(false);
	let deleteTemplateLoading = $state(false);
	let convertTemplateOpen = $state(false);
	let convertTemplateLoading = $state(false);
	let convertTemplateName = $state('');

	function baseGuestName(label: string): string {
		return label.replace(/\s*\((?:CT|VM)?\s*\d+\)\s*$/i, '').trim();
	}

	const openConvertTemplateDialog = () => {
		const baseName = baseGuestName(item.label) || 'template';
		convertTemplateName = `${baseName} Template`;
		convertTemplateOpen = true;
	};

	const handleActionClick = async (action: 'start' | 'reboot' | 'shutdown' | 'stop') => {
		if (item.resourceId === undefined || item.resourceType === undefined) {
			return;
		}

		if (item.resourceType === 'jail') {
			if (action !== 'start' && action !== 'stop') {
				return;
			}
			await jailAction(item.resourceId, action, item.nodeHostname);
		} else {
			await actionVm(item.resourceId, action, item.nodeHostname);
		}

		reload.leftPanel = true;

		console.log(`[cluster-tree] ${action} ${item.resourceType}`, {
			id: item.resourceId,
			hostname: item.nodeHostname
		});
	};

	const handleConvertToTemplate = async () => {
		if (!item.resourceId) return;
		const name = convertTemplateName.trim();
		if (!name) {
			toast.error('Template name is required', { position: 'bottom-center' });
			return;
		}

		convertTemplateLoading = true;
		try {
			const result =
				item.resourceType === 'vm'
					? await convertVMToTemplate(item.resourceId, { name }, item.nodeHostname)
					: await convertJailToTemplate(item.resourceId, { name }, item.nodeHostname);
			if (result.error) {
				handleAPIError(result);
				if (!Array.isArray(result.error)) {
					const err = (result.error || '').toLowerCase();
					if (err.includes('template_name_already_in_use')) {
						toast.error('Template name already in use', { position: 'bottom-center' });
						return;
					}

					if (err.includes('template_name_required')) {
						toast.error('Template name is required', { position: 'bottom-center' });
						return;
					}

					if (err.includes('vm_must_be_shut_off')) {
						toast.error('VM must be shut off to convert to template', {
							position: 'bottom-center'
						});
						return;
					}

					toast.error('Failed to convert to template', { position: 'bottom-center' });
					return;
				}
			}

			convertTemplateOpen = false;
			reload.leftPanel = true;
			toast.success('Template conversion queued', { position: 'bottom-center' });
		} finally {
			convertTemplateLoading = false;
		}
	};

	const handleDeleteTemplate = async () => {
		if (!item.resourceId) return;
		deleteTemplateLoading = true;
		try {
			const result =
				item.resourceType === 'vm-template'
					? await deleteVMTemplate(item.resourceId, item.nodeHostname)
					: await deleteJailTemplate(item.resourceId, item.nodeHostname);
			if (result.error) {
				toast.error('Failed to delete template', { position: 'bottom-center' });
				return;
			}

			deleteTemplateOpen = false;
			reload.leftPanel = true;
			toast.success('Template deleted', { position: 'bottom-center' });
		} finally {
			deleteTemplateLoading = false;
		}
	};
</script>

<li class="w-full">
	{#if hasContextMenu}
		<ContextMenu.Root>
			<ContextMenu.Trigger
				role="button"
				tabindex={0}
				class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
				onclick={handleLabelClick}
				onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e as any) : null)}
			>
				<div class="flex items-center space-x-1 text-sm">
					{#if item.icon === 'material-symbols--monitor-outline' || item.icon === 'hugeicons--prison'}
						<div class="flex items-center space-x-1 text-sm">
							<div class="relative">
								<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
								{#if item.state && item.state === 'active'}
									<div
										class="absolute -right-1 bottom-0.5 flex h-2 w-2 items-center justify-center rounded-full bg-green-500"
									>
										<span class="icon-[mdi--play] h-2 w-2 text-white"></span>
									</div>
								{/if}
							</div>
						</div>
					{:else}
						<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
					{/if}
					<p class="font-inter cursor-pointer whitespace-nowrap">
						{item.label}
					</p>
				</div>

				{#if item.children && item.children.length > 0}
					<span
						role="button"
						tabindex="0"
						class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5 cursor-pointer`}
						onclick={handleIconClick}
						onkeydown={(e) =>
							e.key === 'Enter' || e.key === ' ' ? handleIconClick(e as any) : null}
					></span>
				{/if}
			</ContextMenu.Trigger>
			<ContextMenu.Content>
				{#if item.resourceType === 'jail'}
					{#if item.state === 'active'}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('stop')}>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('start')}>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Separator />
					<ContextMenu.Item class="gap-2" onclick={() => openConvertTemplateDialog()}>
						<span class="icon-[mdi--content-copy] h-4 w-4"></span>
						Convert to Template
					</ContextMenu.Item>
				{:else if item.resourceType === 'vm'}
					{#if item.state === 'active'}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('reboot')}>
							<span class="icon-[mdi--restart] h-4 w-4"></span>
							Reboot
						</ContextMenu.Item>
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('shutdown')}>
							<span class="icon-[mdi--power] h-4 w-4"></span>
							Shutdown
						</ContextMenu.Item>
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('stop')}>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('start')}>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Separator />
					<ContextMenu.Item class="gap-2" onclick={() => openConvertTemplateDialog()}>
						<span class="icon-[mdi--content-copy] h-4 w-4"></span>
						Convert to Template
					</ContextMenu.Item>
				{:else if item.resourceType === 'jail-template'}
					<ContextMenu.Item class="gap-2" onclick={() => (viewTemplateOpen = true)}>
						<span class="icon-[mdi--eye-outline] h-4 w-4"></span>
						View Template
					</ContextMenu.Item>
					<ContextMenu.Item class="gap-2" onclick={() => (createFromTemplateOpen = true)}>
						<span class="icon-[mdi--plus-box-outline] h-4 w-4"></span>
						Create Jail
					</ContextMenu.Item>
					<ContextMenu.Separator />
					<ContextMenu.Item
						class="gap-2 text-destructive"
						onclick={() => (deleteTemplateOpen = true)}
					>
						<span class="icon-[mdi--delete-outline] h-4 w-4"></span>
						Delete Template
					</ContextMenu.Item>
				{:else if item.resourceType === 'vm-template'}
					<ContextMenu.Item class="gap-2" onclick={() => (viewTemplateOpen = true)}>
						<span class="icon-[mdi--eye-outline] h-4 w-4"></span>
						View Template
					</ContextMenu.Item>
					<ContextMenu.Item class="gap-2" onclick={() => (createFromTemplateOpen = true)}>
						<span class="icon-[mdi--plus-box-outline] h-4 w-4"></span>
						Create VM
					</ContextMenu.Item>
					<ContextMenu.Separator />
					<ContextMenu.Item
						class="gap-2 text-destructive"
						onclick={() => (deleteTemplateOpen = true)}
					>
						<span class="icon-[mdi--delete-outline] h-4 w-4"></span>
						Delete Template
					</ContextMenu.Item>
				{/if}
			</ContextMenu.Content>
		</ContextMenu.Root>
	{:else}
		<div
			role="button"
			tabindex="0"
			class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
			onclick={handleLabelClick}
			onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e as any) : null)}
		>
			<div class="flex items-center space-x-1 text-sm">
				{#if item.icon === 'material-symbols--monitor-outline' || item.icon === 'hugeicons--prison'}
					<div class="flex items-center space-x-1 text-sm">
						<div class="relative">
							<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
							{#if item.state && item.state === 'active'}
								<div
									class="absolute -right-1 bottom-0.5 flex h-2 w-2 items-center justify-center rounded-full bg-green-500"
								>
									<span class="icon-[mdi--play] h-2 w-2 text-white"></span>
								</div>
							{/if}
						</div>
					</div>
				{:else}
					<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
				{/if}
				<p class="font-inter cursor-pointer whitespace-nowrap">
					{item.label}
				</p>
			</div>

			{#if item.children && item.children.length > 0}
				<span
					role="button"
					tabindex="0"
					class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5 cursor-pointer`}
					onclick={handleIconClick}
					onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleIconClick(e as any) : null)}
				></span>
			{/if}
		</div>
	{/if}
</li>

{#if isOpen && item.children}
	<ul class="pl-5" transition:slide={{ duration: 200, easing: (t) => t }} style="overflow: hidden;">
		{#each item.children as child (child.id)}
			<SidebarElement
				item={child}
				{openIds}
				{onToggleId}
				nextGuestId={item.nextGuestId ?? nextGuestId}
			/>
		{/each}
	</ul>
{/if}

{#if (item.resourceType === 'jail' || item.resourceType === 'vm') && item.resourceId}
	<Dialog.Root bind:open={convertTemplateOpen}>
		<Dialog.Content class="max-w-md">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center gap-2">
						<span class="icon icon-[tabler--template]"></span>
						<span>Convert To Template</span>
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<CustomValueInput
				label="Template Name"
				placeholder="Template name"
				bind:value={convertTemplateName}
				disabled={convertTemplateLoading}
				classes="space-y-2"
			/>

			<Dialog.Footer>
				<Button
					size="sm"
					variant="outline"
					onclick={() => {
						convertTemplateOpen = false;
					}}
					disabled={convertTemplateLoading}>Cancel</Button
				>
				<Button
					size="sm"
					onclick={() => void handleConvertToTemplate()}
					disabled={convertTemplateLoading}
				>
					{#if convertTemplateLoading}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Convert
					{/if}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

{#if item.resourceType === 'jail-template' && item.resourceId}
	<ViewJailTemplate
		bind:open={viewTemplateOpen}
		templateId={item.resourceId}
		templateLabel={item.label}
		hostname={item.nodeHostname}
	/>
	<CreateJailFromTemplate
		bind:open={createFromTemplateOpen}
		templateId={item.resourceId}
		templateLabel={item.label}
		hostname={item.nodeHostname}
		{nextGuestId}
	/>

	<AlertDialog
		bind:open={deleteTemplateOpen}
		names={{ parent: 'template', element: item.label }}
		actions={{
			onConfirm: () => void handleDeleteTemplate(),
			onCancel: () => {
				deleteTemplateOpen = false;
			}
		}}
		loading={deleteTemplateLoading}
		confirmLabel="Delete"
		loadingLabel="Deleting..."
	/>
{/if}

{#if item.resourceType === 'vm-template' && item.resourceId}
	<ViewVMTemplate
		bind:open={viewTemplateOpen}
		templateId={item.resourceId}
		templateLabel={item.label}
		hostname={item.nodeHostname}
	/>
	<CreateVMFromTemplate
		bind:open={createFromTemplateOpen}
		templateId={item.resourceId}
		templateLabel={item.label}
		hostname={item.nodeHostname}
		{nextGuestId}
	/>

	<AlertDialog
		bind:open={deleteTemplateOpen}
		names={{ parent: 'template', element: item.label }}
		actions={{
			onConfirm: () => void handleDeleteTemplate(),
			onCancel: () => {
				deleteTemplateOpen = false;
			}
		}}
		loading={deleteTemplateLoading}
		confirmLabel="Delete"
		loadingLabel="Deleting..."
	/>
{/if}
