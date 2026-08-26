<script lang="ts">
	import { page } from '$app/state';
	import { convertJailToTemplate, deleteJailTemplate, jailAction } from '$lib/api/jail/jail';
	import CreateJailFromTemplate from '$lib/components/custom/Jail/Template/Create.svelte';
	import ViewJailTemplate from '$lib/components/custom/Jail/Template/View.svelte';
	import CreateVMFromTemplate from '$lib/components/custom/VM/Template/Create.svelte';
	import ViewVMTemplate from '$lib/components/custom/VM/Template/View.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		actionVm,
		captureVMTemplate,
		deleteVMTemplate,
		purgeVMRegistration
	} from '$lib/api/vm/vm';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { slide } from 'svelte/transition';
	import { toast } from 'svelte-sonner';
	import SidebarElement from './TreeViewCluster.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import {
		isResourceTreeGroup,
		type ResourceTreeDensity,
		type ResourceTreeItem
	} from '$lib/resource-tree';
	import type { ActiveLifecycleGuest } from '$lib/types/task/lifecycle';
	import { removeStaleCacheByRID } from '$lib/utils/vm/vm';

	type GuestAction = 'start' | 'reboot' | 'shutdown' | 'stop';

	interface Props {
		item: ResourceTreeItem;
		openIds: Set<string>;
		onToggleId: (id: string) => void;
		nextGuestId?: number;
		canMigrate?: boolean;
		onMigrate?: (item: ResourceTreeItem) => void;
		density?: ResourceTreeDensity;
		activeLifecycleGuests?: ActiveLifecycleGuest[];
	}

	let {
		item,
		openIds,
		onToggleId,
		nextGuestId = 100,
		canMigrate = false,
		onMigrate,
		density = 'comfortable',
		activeLifecycleGuests = []
	}: Props = $props();
	let isOpen = $derived(openIds.has(item.id));
	let isGroup = $derived(isResourceTreeGroup(item));
	let isOfflineNode = $derived(item.icon === 'mdi--server-off');
	let isCompact = $derived(density === 'compact');
	let rowPaddingClass = $derived(isCompact ? 'py-0' : 'py-0.5');
	let rowSpacingClass = $derived(isCompact ? 'my-0' : 'my-0.5');
	let iconSizePx = $derived(isCompact ? 16 : 18);
	let lifecycleActive = $derived(
		item.resourceId !== undefined &&
			item.nodeHostname !== undefined &&
			(item.resourceType === 'vm' || item.resourceType === 'jail') &&
			activeLifecycleGuests.some(
				(guest) =>
					guest.hostname === item.nodeHostname &&
					guest.guestType === item.resourceType &&
					guest.guestId === item.resourceId
			)
	);

	const handleLabelClick = (e: Event) => {
		e.preventDefault();
		if (item.href) {
			useSafeGoto(item.href, { replaceState: false, noScroll: false });
		}
	};

	const handleIconClick = (e: Event) => {
		e.preventDefault();
		e.stopPropagation();
		if (item.children && item.children.length > 0) {
			onToggleId(item.id);
		}
	};

	const handleGroupClick = (e: Event) => {
		e.preventDefault();
		onToggleId(item.id);
	};

	function statusDotClass(state?: 'active' | 'inactive' | 'orphan'): string | null {
		if (state === 'active') return 'bg-green-500';
		if (state === 'orphan') return 'bg-amber-500';
		if (state === 'inactive') return 'bg-neutral-500';
		return null;
	}

	const sidebarActive = 'rounded-md bg-muted font-inter font-medium';

	function isItemActive(menuItem: ResourceTreeItem, currentUrl: string): boolean {
		if (menuItem.href) {
			if (currentUrl.startsWith(menuItem.href)) {
				return true;
			}
			const basePath = menuItem.href.replace(/\/summary$/, '');
			if (basePath !== menuItem.href && currentUrl.startsWith(basePath)) {
				return true;
			}
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
	let actionInFlight = $state(false);
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
	let deleteVMOpen = $state(false);
	let deleteVMLoading = $state(false);

	function baseGuestName(label: string): string {
		return label.replace(/\s*\((?:CT|VM)?\s*\d+\)\s*$/i, '').trim();
	}

	const openConvertTemplateDialog = () => {
		if (item.state === 'active') {
			toast.error(
				item.resourceType === 'vm'
					? 'VM must be shut off to capture as a template'
					: 'Jail must be stopped to capture as a template',
				{ position: 'bottom-center' }
			);
			return;
		}
		const baseName = baseGuestName(item.label) || 'template';
		convertTemplateName = `${baseName} Template`;
		convertTemplateOpen = true;
	};

	const handleActionClick = async (action: GuestAction) => {
		if (
			lifecycleActive ||
			actionInFlight ||
			item.resourceId === undefined ||
			!item.nodeHostname ||
			(item.resourceType !== 'jail' && item.resourceType !== 'vm')
		) {
			return;
		}

		const resourceType = item.resourceType;
		const resourceId = item.resourceId;
		const hostname = item.nodeHostname;
		actionInFlight = true;

		try {
			let result: Awaited<ReturnType<typeof actionVm>> | Awaited<ReturnType<typeof jailAction>>;
			if (resourceType === 'jail') {
				if (action !== 'start' && action !== 'stop') {
					return;
				}
				result = await jailAction(resourceId, action, hostname);
			} else {
				result = await actionVm(resourceId, action, hostname);
			}

			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error(`Failed to ${action} ${resourceType === 'vm' ? 'VM' : 'jail'}`, {
					position: 'bottom-center'
				});
				return;
			}

			reload.leftPanel = true;

			console.log(`[cluster-tree] ${action} ${resourceType}`, {
				id: resourceId,
				hostname
			});
		} catch (error) {
			toast.error(error instanceof Error ? error.message : `Failed to ${action} ${resourceType}`, {
				position: 'bottom-center'
			});
		} finally {
			actionInFlight = false;
		}
	};

	const handleConvertToTemplate = async () => {
		if (
			convertTemplateLoading ||
			item.resourceId === undefined ||
			!item.nodeHostname ||
			(item.resourceType !== 'vm' && item.resourceType !== 'jail')
		) {
			return;
		}
		if (item.state === 'active') {
			toast.error(
				item.resourceType === 'vm'
					? 'VM must be shut off to capture as a template'
					: 'Jail must be stopped to capture as a template',
				{ position: 'bottom-center' }
			);
			return;
		}

		const resourceId = item.resourceId;
		const resourceType = item.resourceType;
		const hostname = item.nodeHostname;
		const name = convertTemplateName.trim();
		if (!name) {
			toast.error('Template name is required', { position: 'bottom-center' });
			return;
		}

		convertTemplateLoading = true;
		try {
			const result =
				resourceType === 'vm'
					? await captureVMTemplate(resourceId, { name }, hostname)
					: await convertJailToTemplate(resourceId, { name }, hostname);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				const error = Array.isArray(result.error) ? result.error[0] : result.error;
				const err = (error || '').toLowerCase();
				if (err.includes('template_name_already_in_use')) {
					toast.error('Template name already in use', { position: 'bottom-center' });
					return;
				}

				if (err.includes('template_name_required')) {
					toast.error('Template name is required', { position: 'bottom-center' });
					return;
				}

				if (err.includes('vm_must_be_shut_off')) {
					toast.error('VM must be shut off to capture as a template', {
						position: 'bottom-center'
					});
					return;
				}
				if (err.includes('jail_must_be_stopped')) {
					toast.error('Jail must be stopped to capture as a template', {
						position: 'bottom-center'
					});
					return;
				}

				toast.error(
					resourceType === 'vm'
						? 'Failed to capture VM template'
						: 'Failed to convert jail to template',
					{ position: 'bottom-center' }
				);
				return;
			}

			convertTemplateOpen = false;
			reload.leftPanel = true;
			toast.success(
				resourceType === 'vm' ? 'VM template capture queued' : 'Jail template conversion queued',
				{ position: 'bottom-center' }
			);
		} catch (error) {
			toast.error(error instanceof Error ? error.message : 'Failed to capture template', {
				position: 'bottom-center'
			});
		} finally {
			convertTemplateLoading = false;
		}
	};

	const handleDeleteTemplate = async () => {
		if (
			deleteTemplateLoading ||
			item.resourceId === undefined ||
			!item.nodeHostname ||
			(item.resourceType !== 'vm-template' && item.resourceType !== 'jail-template')
		) {
			return;
		}
		const resourceId = item.resourceId;
		const resourceType = item.resourceType;
		const hostname = item.nodeHostname;
		deleteTemplateLoading = true;
		try {
			const result =
				resourceType === 'vm-template'
					? await deleteVMTemplate(resourceId, hostname)
					: await deleteJailTemplate(resourceId, hostname);
			if (result.status === 'error') {
				handleAPIError(result);
				toast.error('Failed to delete template', { position: 'bottom-center' });
				return;
			}

			deleteTemplateOpen = false;
			reload.leftPanel = true;
			toast.success('Template deleted', { position: 'bottom-center' });
		} catch (error) {
			toast.error(error instanceof Error ? error.message : 'Failed to delete template', {
				position: 'bottom-center'
			});
		} finally {
			deleteTemplateLoading = false;
		}
	};

	const handleRemoveVMEntry = async () => {
		const rid = item.resourceId;
		const hostname = item.nodeHostname;
		if (!rid || !hostname) return;
		deleteVMLoading = true;
		try {
			const result = await purgeVMRegistration(rid, true, hostname);
			if (result.error) {
				if (result.message === 'vm_not_orphaned' || result.error.includes('vm_not_orphaned')) {
					deleteVMOpen = false;
					reload.leftPanel = true;
					toast.error(
						'This VM still has a live definition on its node \u2014 use Delete instead of removing a stale entry',
						{ position: 'bottom-center' }
					);
					return;
				}
				handleAPIError(result);
				return;
			}

			deleteVMOpen = false;
			reload.leftPanel = true;
			await removeStaleCacheByRID(rid, hostname);
			toast.success('VM entry removed (datasets preserved)', { position: 'bottom-center' });

			if (item.href && activeUrl.startsWith(item.href.replace(/\/summary$/, ''))) {
				useSafeGoto(`/${hostname}/summary`, { replaceState: false, noScroll: false });
			}
		} finally {
			deleteVMLoading = false;
		}
	};
</script>

<li class="w-full">
	{#if isGroup}
		<div
			role="button"
			tabindex="0"
			class={`${rowSpacingClass} text-muted-foreground hover:bg-muted/40 dark:hover:bg-muted/40 flex w-full cursor-pointer items-center justify-between rounded-md px-1.5 ${rowPaddingClass}`}
			onclick={handleGroupClick}
			onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleGroupClick(e) : null)}
		>
			<div class="flex min-w-0 items-center gap-1.5">
				<span class={`icon-[${item.icon}] size-3.5 shrink-0`}></span>
				<span class="truncate text-[11px] font-semibold tracking-wide uppercase">{item.label}</span>
				<span class="text-muted-foreground/70 text-[10px] font-normal">{item.children?.length}</span
				>
			</div>
			<div class="flex size-5 shrink-0 items-center justify-center">
				<span class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5`}
				></span>
			</div>
		</div>
	{:else if hasContextMenu}
		<ContextMenu.Root>
			<ContextMenu.Trigger
				role="button"
				tabindex={0}
				class={`${rowSpacingClass} data-[state=open]:bg-muted flex w-full cursor-pointer items-center justify-between px-1.5 ${rowPaddingClass} ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
				onclick={handleLabelClick}
				onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e) : null)}
			>
				<div class="flex min-w-0 items-center space-x-1 text-sm">
					{#if item.icon === 'material-symbols--monitor-outline' || item.icon === 'hugeicons--prison'}
						<div class="flex items-center space-x-1 text-sm">
							<div class="relative">
								<span
									class={`icon-[${item.icon}]`}
									style={`width: ${iconSizePx}px; height: ${iconSizePx}px;`}
								></span>
								{#if statusDotClass(item.state)}
									<div
										class={`absolute -right-1 bottom-0.5 h-2 w-2 rounded-full ring-2 ring-background ${statusDotClass(item.state)}`}
									></div>
								{/if}
							</div>
						</div>
					{:else}
						<span
							class={`icon-[${item.icon}]`}
							style={`width: ${iconSizePx}px; height: ${iconSizePx}px;`}
						></span>
					{/if}
					<p
						class="font-inter cursor-pointer truncate whitespace-nowrap"
						title={item.meta ? `${item.label} · ${item.meta}` : item.label}
					>
						{item.label}
						{#if item.meta}
							<span class="text-muted-foreground ml-1.5 text-[11px] font-normal">· {item.meta}</span
							>
						{/if}
					</p>
				</div>

				{#if item.children && item.children.length > 0}
					<div class="flex shrink-0 items-center gap-0.5">
						<span
							role="button"
							tabindex="0"
							class="hover:bg-background flex size-5 shrink-0 cursor-pointer items-center justify-center rounded"
							onclick={handleIconClick}
							onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleIconClick(e) : null)}
						>
							<span
								class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5`}
							></span>
						</span>
					</div>
				{/if}
			</ContextMenu.Trigger>
			<ContextMenu.Content>
				{#if item.resourceType === 'jail'}
					{#if item.state === 'active'}
						<ContextMenu.Item
							class="gap-2"
							disabled={actionInFlight || lifecycleActive}
							onSelect={() => void handleActionClick('stop')}
						>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item
							class="gap-2"
							disabled={actionInFlight || lifecycleActive}
							onSelect={() => void handleActionClick('start')}
						>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Separator />
					{#if canMigrate && onMigrate}
						<ContextMenu.Item class="gap-2" onSelect={() => onMigrate(item)}>
							<span class="icon-[mdi--swap-horizontal] h-4 w-4"></span>
							Migrate
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Item
						class="gap-2"
						disabled={item.state === 'active' || convertTemplateLoading}
						onclick={() => openConvertTemplateDialog()}
					>
						<span class="icon-[mdi--content-copy] h-4 w-4"></span>
						Create Template
					</ContextMenu.Item>
				{:else if item.resourceType === 'vm'}
					{#if item.state === 'active'}
						<ContextMenu.Item
							class="gap-2"
							disabled={actionInFlight || lifecycleActive}
							onSelect={() => void handleActionClick('reboot')}
						>
							<span class="icon-[mdi--restart] h-4 w-4"></span>
							Reboot
						</ContextMenu.Item>
						<ContextMenu.Item
							class="gap-2"
							disabled={actionInFlight || lifecycleActive}
							onSelect={() => void handleActionClick('shutdown')}
						>
							<span class="icon-[mdi--power] h-4 w-4"></span>
							Shutdown
						</ContextMenu.Item>
						<ContextMenu.Item
							class="gap-2"
							disabled={actionInFlight || lifecycleActive}
							onSelect={() => void handleActionClick('stop')}
						>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item
							class="gap-2"
							disabled={actionInFlight || lifecycleActive}
							onSelect={() => void handleActionClick('start')}
						>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Separator />
					{#if canMigrate && onMigrate && item.state !== 'orphan'}
						<ContextMenu.Item class="gap-2" onSelect={() => onMigrate(item)}>
							<span class="icon-[mdi--swap-horizontal] h-4 w-4"></span>
							Migrate
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Item
						class="gap-2"
						disabled={item.state === 'active' || convertTemplateLoading}
						onclick={() => openConvertTemplateDialog()}
					>
						<span class="icon-[mdi--content-copy] h-4 w-4"></span>
						Create Template
					</ContextMenu.Item>
					{#if item.state === 'orphan'}
						<ContextMenu.Item class="gap-2 text-destructive" onclick={() => (deleteVMOpen = true)}>
							<span class="icon-[mdi--delete-sweep] h-4 w-4"></span>
							Remove stale entry
						</ContextMenu.Item>
					{/if}
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
			class={`${rowSpacingClass} flex w-full cursor-pointer items-center justify-between px-1.5 ${rowPaddingClass} ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}${isOfflineNode ? ' opacity-60' : ''}`}
			onclick={handleLabelClick}
			onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e) : null)}
		>
			<div class="flex min-w-0 items-center space-x-1 text-sm">
				{#if item.icon === 'material-symbols--monitor-outline' || item.icon === 'hugeicons--prison'}
					<div class="flex items-center space-x-1 text-sm">
						<div class="relative">
							<span
								class={`icon-[${item.icon}]`}
								style={`width: ${iconSizePx}px; height: ${iconSizePx}px;`}
							></span>
							{#if statusDotClass(item.state)}
								<div
									class={`absolute -right-1 bottom-0.5 h-2 w-2 rounded-full ring-2 ring-background ${statusDotClass(item.state)}`}
								></div>
							{/if}
						</div>
					</div>
				{:else}
					<span
						class={`icon-[${item.icon}]`}
						style={`width: ${iconSizePx}px; height: ${iconSizePx}px;`}
					></span>
				{/if}
				<p
					class="font-inter cursor-pointer truncate whitespace-nowrap"
					title={item.meta ? `${item.label} · ${item.meta}` : item.label}
				>
					{item.label}
					{#if item.meta}
						<span class="text-muted-foreground ml-1.5 text-[11px] font-normal">· {item.meta}</span>
					{/if}
				</p>
				{#if isOfflineNode}
					<span
						class="bg-muted text-muted-foreground shrink-0 rounded px-1 py-0.5 text-[10px] font-medium"
						>Offline</span
					>
				{/if}
			</div>

			{#if item.children && item.children.length > 0}
				<span
					role="button"
					tabindex="0"
					class="flex size-5 shrink-0 cursor-pointer items-center justify-center rounded"
					onclick={handleIconClick}
					onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleIconClick(e) : null)}
				>
					<span class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5`}
					></span>
				</span>
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
				{canMigrate}
				{onMigrate}
				{density}
				{activeLifecycleGuests}
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
						<span>Create {item.resourceType === 'jail' ? 'Jail' : 'VM'} Template</span>
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
						{item.resourceType === 'vm' ? 'Create Template' : 'Convert'}
					{/if}
				</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

{#if item.resourceType === 'vm' && item.resourceId}
	<AlertDialog
		bind:open={deleteVMOpen}
		customTitle={`Remove the stale inventory entry for <span class="font-semibold">${item.label}</span>? Only the local VM record and any local libvirt domain are removed &mdash; ZFS datasets are preserved and nothing is deleted on other nodes.`}
		actions={{
			onConfirm: handleRemoveVMEntry,
			onCancel: () => {
				deleteVMOpen = false;
			}
		}}
		loading={deleteVMLoading}
		confirmLabel="Remove"
		loadingLabel="Removing..."
	/>
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
			onConfirm: handleDeleteTemplate,
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
			onConfirm: handleDeleteTemplate,
			onCancel: () => {
				deleteTemplateOpen = false;
			}
		}}
		loading={deleteTemplateLoading}
		confirmLabel="Delete"
		loadingLabel="Deleting..."
	/>
{/if}
