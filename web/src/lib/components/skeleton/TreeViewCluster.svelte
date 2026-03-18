<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		convertJailToTemplate,
		createJailFromTemplate,
		deleteJailTemplate,
		jailAction
	} from '$lib/api/jail/jail';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { actionVm } from '$lib/api/vm/vm';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { slide } from 'svelte/transition';
	import { toast } from 'svelte-sonner';
	import SidebarElement from './TreeViewCluster.svelte';

	interface SidebarProps {
		id: string;
		label: string;
		icon: string;
		href?: string;
		state?: 'active' | 'inactive';
		resourceId?: number;
		resourceType?: 'vm' | 'jail' | 'jail-template';
		sourceCtId?: number;
		nodeHostname?: string;
		children?: SidebarProps[];
	}

	interface Props {
		item: SidebarProps;
		openIds: Set<string>;
		onToggleId: (id: string) => void;
	}

	let { item, openIds, onToggleId }: Props = $props();
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
		item.resourceType === 'vm' || item.resourceType === 'jail' || item.resourceType === 'jail-template'
	);
	let lastActiveUrl = $derived.by(() => {
		const segments = activeUrl.split('/');
		return segments[segments.length - 1];
	});
	let createFromTemplateOpen = $state(false);
	let createMode = $state<'single' | 'multiple'>('single');
	let singleCTID = $state(item.sourceCtId || 0);
	let singleName = $state('');
	let multipleStartCTID = $state(item.sourceCtId || 0);
	let multipleCount = $state(1);
	let multipleNamePrefix = $state(item.label.replace(/\s*\(CT\s*\d+\)$/i, ''));
	let actionLoading = $state(false);

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
		actionLoading = true;
		const result = await convertJailToTemplate(item.resourceId, item.nodeHostname);
		actionLoading = false;
		if (result.error) {
			toast.error('Failed to convert jail to template', { position: 'bottom-center' });
			return;
		}
		reload.leftPanel = true;
		toast.success('Template conversion queued', { position: 'bottom-center' });
	};

	const handleDeleteTemplate = async () => {
		if (!item.resourceId) return;
		if (!confirm(`Delete template "${item.label}"?`)) return;
		actionLoading = true;
		const result = await deleteJailTemplate(item.resourceId, item.nodeHostname);
		actionLoading = false;
		if (result.error) {
			toast.error('Failed to delete template', { position: 'bottom-center' });
			return;
		}
		reload.leftPanel = true;
		toast.success('Template deleted', { position: 'bottom-center' });
	};

	const handleCreateFromTemplate = async () => {
		if (!item.resourceId) return;
		actionLoading = true;
		const result =
			createMode === 'single'
				? await createJailFromTemplate(
						item.resourceId,
						{
							mode: 'single',
							ctid: Number(singleCTID),
							name: singleName || undefined
						},
						item.nodeHostname
					)
				: await createJailFromTemplate(
						item.resourceId,
						{
							mode: 'multiple',
							startCtid: Number(multipleStartCTID),
							count: Number(multipleCount),
							namePrefix: multipleNamePrefix || undefined
						},
						item.nodeHostname
					);
		actionLoading = false;
		if (result.error) {
			toast.error('Failed to create jail from template', { position: 'bottom-center' });
			return;
		}
		createFromTemplateOpen = false;
		reload.leftPanel = true;
		toast.success('Template restore job queued', { position: 'bottom-center' });
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
					<ContextMenu.Item class="gap-2" onclick={() => void handleConvertToTemplate()}>
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
				{:else if item.resourceType === 'jail-template'}
					<ContextMenu.Item class="gap-2" onclick={() => (createFromTemplateOpen = true)}>
						<span class="icon-[mdi--plus-box-outline] h-4 w-4"></span>
						Create Jail
					</ContextMenu.Item>
					<ContextMenu.Item class="gap-2 text-destructive" onclick={() => void handleDeleteTemplate()}>
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
			<SidebarElement item={child} {openIds} {onToggleId} />
		{/each}
	</ul>
{/if}

{#if item.resourceType === 'jail-template'}
	<Dialog.Root bind:open={createFromTemplateOpen}>
		<Dialog.Content class="max-w-lg">
			<Dialog.Header class="p-0">
				<Dialog.Title>Create Jail From Template</Dialog.Title>
			</Dialog.Header>
			<div class="grid gap-4 py-2">
				<div class="flex gap-2">
					<Button
						size="sm"
						variant={createMode === 'single' ? 'default' : 'outline'}
						onclick={() => (createMode = 'single')}>Single</Button
					>
					<Button
						size="sm"
						variant={createMode === 'multiple' ? 'default' : 'outline'}
						onclick={() => (createMode = 'multiple')}>Multiple</Button
					>
				</div>

				{#if createMode === 'single'}
					<div class="grid gap-2">
						<Label for={`single-ctid-${item.id}`}>CTID</Label>
						<Input id={`single-ctid-${item.id}`} type="number" min="1" bind:value={singleCTID} />
					</div>
					<div class="grid gap-2">
						<Label for={`single-name-${item.id}`}>Name (optional)</Label>
						<Input id={`single-name-${item.id}`} bind:value={singleName} />
					</div>
				{:else}
					<div class="grid gap-2">
						<Label for={`multi-start-${item.id}`}>Starting CTID</Label>
						<Input
							id={`multi-start-${item.id}`}
							type="number"
							min="1"
							bind:value={multipleStartCTID}
						/>
					</div>
					<div class="grid gap-2">
						<Label for={`multi-count-${item.id}`}>Count</Label>
						<Input id={`multi-count-${item.id}`} type="number" min="1" bind:value={multipleCount} />
					</div>
					<div class="grid gap-2">
						<Label for={`multi-prefix-${item.id}`}>Name Prefix</Label>
						<Input id={`multi-prefix-${item.id}`} bind:value={multipleNamePrefix} />
					</div>
				{/if}
			</div>
			<Dialog.Footer>
				<Button
					size="sm"
					disabled={actionLoading}
					onclick={() => void handleCreateFromTemplate()}>Create Jail</Button
				>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}
