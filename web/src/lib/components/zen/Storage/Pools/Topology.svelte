<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { Disk } from '$lib/types/disk/disk';
	import type { Zpool, ZpoolStatusPool } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import DeviceAction from './DeviceAction.svelte';
	import {
		poolDeviceActionReason,
		poolDeviceTargets,
		replacementDeviceChoices,
		type PoolDeviceAction,
		type PoolDeviceTarget
	} from './devices';
	import { deviceLeaves, topologyGroups, vdevLabel, type DeviceNode } from './pools';
	import State from './State.svelte';
	const diskIcons: Record<string, string> = {
		HDD: 'icon-[mdi--harddisk]',
		SSD: 'icon-[icon-park-outline--ssd]',
		NVME: 'icon-[sylve--nvme-vertical]',
		VIRTUAL: 'icon-[mdi--nas]'
	};
	let {
		hostname,
		pool,
		pools,
		status,
		disks,
		inventoryUnavailable,
		statusUnavailable,
		scanBusy,
		onChanged
	}: {
		hostname: string;
		pool: Zpool;
		pools: Zpool[];
		status: ZpoolStatusPool | null;
		disks: Disk[];
		inventoryUnavailable: boolean;
		statusUnavailable: boolean;
		scanBusy: boolean;
		onChanged: () => Promise<void>;
	} = $props();
	const operations = [
		{ action: 'replace', label: 'Replace Device', icon: 'icon-[lucide--replace]' },
		{ action: 'detach', label: 'Detach Device', icon: 'icon-[lucide--unplug]' }
	] as const;
	let actionOpen = $state(false);
	let pending = $state.raw<{ target: PoolDeviceTarget; action: PoolDeviceAction } | null>(null);
	let canModify = $derived(allowed('Sys.Modify', nodePath(hostname)));
	let targets = $derived(
		new Map(poolDeviceTargets(pool, status, disks).map((target) => [target.node.id, target]))
	);
	let newDeviceAvailable = $derived.by(() => {
		if (!canModify || inventoryUnavailable || disks.length === 0) return new Set<string>();
		const snapshot = { pool, pools, status, disks };
		return new Set(
			[...targets]
				.filter(([, target]) =>
					replacementDeviceChoices(snapshot, target).some((choice) => !choice.blocked)
				)
				.map(([id]) => id)
		);
	});
	let groups = $derived(
		topologyGroups(pool, status, disks).map((group) => {
			const nodes = group.nodes.flatMap((node) =>
				node.kind.toLowerCase() === 'root' && node.children.length > 0 ? node.children : [node]
			);
			return {
				...group,
				nodes,
				vdevs: ['l2cache', 'spares'].includes(group.key) ? null : nodes.length,
				devices: deviceLeaves(group.nodes).length
			};
		})
	);

	function actionReason(target: PoolDeviceTarget, action: PoolDeviceAction) {
		if (statusUnavailable) return 'Refresh the pool status before changing devices.';
		if (scanBusy)
			return 'Wait for the current or queued scrub or resilver to finish before changing devices.';
		const reason = poolDeviceActionReason(pool, status, target, action);
		if (reason || action === 'detach') return reason;
		return newDeviceAvailable.has(target.node.id)
			? ''
			: 'No eligible unused devices are available for this action.';
	}

	function attachTarget(node: DeviceNode): PoolDeviceTarget | undefined {
		if (node.kind.toLowerCase() === 'mirror') {
			const members = node.children.flatMap((member) => {
				const target = targets.get(member.id);
				return target ? [target] : [];
			});
			return members.find((target) => !actionReason(target, 'attach'));
		}
		const target = targets.get(node.id);
		const parent = target?.ancestors.at(-1);
		return target &&
			node.kind.toLowerCase() === 'disk' &&
			!['spares', 'l2cache'].includes(target.role) &&
			(!parent || parent.kind.toLowerCase() === 'root')
			? target
			: undefined;
	}

	function openAction(target: PoolDeviceTarget, action: PoolDeviceAction) {
		if (actionOpen || !canModify || actionReason(target, action)) return;
		pending = { target, action };
		actionOpen = true;
	}
</script>

{#snippet deviceActions(target: PoolDeviceTarget)}
	<DropdownMenu.Root>
		<DropdownMenu.Trigger>
			{#snippet child({ props })}
				<Button
					{...props}
					variant="ghost"
					size="icon"
					class="bg-zen-highlight/60 text-zen-highlight-foreground size-5 shrink-0 rounded-sm hover:bg-zen-accent/20 hover:text-zen-highlight-foreground dark:hover:bg-zen-accent/25"
					aria-label="Device actions for {target.node.name}"
					disabled={actionOpen}
				>
					<span class="icon-[lucide--wrench] size-3" aria-hidden="true"></span>
				</Button>
			{/snippet}
		</DropdownMenu.Trigger>
		<DropdownMenu.Content
			align="start"
			class="w-44"
			onCloseAutoFocus={(event) => {
				if (actionOpen) event.preventDefault();
			}}
		>
			{#each operations as operation (operation.action)}
				{@const reason = actionReason(target, operation.action)}
				{#if reason}
					<Tooltip.Root delayDuration={500}>
						<Tooltip.Trigger>
							{#snippet child({ props })}
								<DropdownMenu.Item
									{...props}
									disabled
									class="data-[disabled]:pointer-events-auto data-[disabled]:cursor-help"
								>
									<span class="{operation.icon} size-4" aria-hidden="true"></span>{operation.label}
									<span class="sr-only">: {reason}</span>
									<span class="icon-[lucide--info] ml-auto size-3.5" aria-hidden="true"></span>
								</DropdownMenu.Item>
							{/snippet}
						</Tooltip.Trigger>
						<Tooltip.Content side="left" sideOffset={8} class="z-60 max-w-64 text-left text-pretty">
							{reason}
						</Tooltip.Content>
					</Tooltip.Root>
				{:else}
					<DropdownMenu.Item
						class="cursor-pointer"
						onSelect={() => openAction(target, operation.action)}
					>
						<span class="{operation.icon} size-4" aria-hidden="true"></span>{operation.label}
					</DropdownMenu.Item>
				{/if}
			{/each}
		</DropdownMenu.Content>
	</DropdownMenu.Root>
{/snippet}

{#snippet row(node: DeviceNode, depth: number)}
	{@const target = targets.get(node.id)}
	{@const attach = attachTarget(node)}
	{@const diskType = node.disk?.type.trim() || ''}
	{@const diskIcon = diskIcons[diskType.toUpperCase()] ?? 'icon-[lucide--hard-drive]'}
	{@const metadata = node.disk
		? [
				{ label: 'Model', value: node.disk.model },
				{ label: 'Serial', value: node.disk.serial }
			].filter((detail) => detail.value.trim())
		: []}
	<div
		class="topology-row border-border/40 relative min-h-10 border-b px-2 py-3"
		class:tree-parent={node.children.length > 0 && depth < 3}
	>
		<div class="node-identity flex min-w-0 items-start gap-2">
			<span class="text-muted-foreground size-5 shrink-0 dark:text-muted-foreground/75">
				{#if node.children.length > 0}<span
						class="vdev-toggle icon-[lucide--chevron-right] block size-5 transition-transform"
						aria-hidden="true"
					></span>{:else}<span
						class="block size-5 {diskIcon}"
						role="img"
						aria-label={diskType ? `${diskType} device` : 'Device type not reported'}
						title={diskType || 'Device type not reported'}
					></span>{/if}
			</span>
			<div class="min-w-0">
				<div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
					<div class="flex min-w-0 max-w-full items-center gap-2">
						<span
							class="min-w-0 break-all leading-5 {node.children.length > 0
								? 'text-sm font-semibold'
								: 'text-foreground/90 font-mono text-[13px] font-medium'}">{node.name}</span
						>
						{#if canModify && target && node.kind.toLowerCase() === 'disk' && operations.some((operation) => !actionReason(target, operation.action))}
							{@render deviceActions(target)}
						{/if}
						{#if canModify && attach && !actionReason(attach, 'attach')}
							<Button
								variant="ghost"
								size="icon"
								class="bg-zen-highlight/60 text-zen-highlight-foreground size-5 shrink-0 rounded-sm hover:bg-zen-accent/20 hover:text-zen-highlight-foreground dark:hover:bg-zen-accent/25"
								aria-label="Attach device to {node.name}"
								aria-haspopup="dialog"
								disabled={actionOpen}
								onclick={(event) => {
									event.preventDefault();
									event.stopPropagation();
									openAction(attach, 'attach');
								}}
							>
								<span class="icon-[lucide--plus] size-3" aria-hidden="true"></span>
							</Button>
						{/if}
					</div>
					{#if node.children.length > 0}
						<span
							class="node-description text-muted-foreground text-xs leading-4 dark:text-muted-foreground/75"
							>{vdevLabel(node.kind, node.name)} · {node.children.length}
							{node.children.length === 1 ? 'Member' : 'Members'}</span
						>
					{:else if node.kind.toLowerCase() !== 'disk'}
						<span
							class="node-description text-muted-foreground text-xs leading-4 dark:text-muted-foreground/75"
							>{vdevLabel(node.kind, node.name)}</span
						>
					{/if}
				</div>
				{#if node.path && node.path !== node.name}<p
						class="text-muted-foreground font-mono text-xs break-all dark:text-muted-foreground/80"
					>
						{node.path}
					</p>{/if}
				{#if metadata.length > 0}
					<dl
						class="node-metadata text-muted-foreground mt-0.5 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-2 text-xs leading-4 dark:text-muted-foreground/80"
					>
						{#each metadata as detail (detail.label)}
							<dt>{detail.label}:</dt>
							<dd
								class="min-w-0 break-words"
								class:font-mono={detail.label === 'Serial'}
								class:break-all={detail.label === 'Serial'}
							>
								{detail.value}
							</dd>
						{/each}
					</dl>
				{/if}
			</div>
		</div>
		<dl
			class="node-layout text-muted-foreground text-[13px] leading-5 font-medium dark:text-muted-foreground/75"
		>
			<dt class="sr-only">Topology</dt>
			<dd>
				{node.children.length > 0 || node.kind.toLowerCase() !== 'disk'
					? vdevLabel(node.kind, node.name)
					: '—'}
			</dd>
		</dl>
		<dl
			class="node-members text-muted-foreground text-[13px] leading-5 font-medium tabular-nums dark:text-muted-foreground/75"
		>
			<dt class="sr-only">Devices</dt>
			<dd title={node.children.length > 0 ? `${node.children.length} members` : undefined}>
				{node.children.length > 0 ? deviceLeaves(node.children).length : '—'}
			</dd>
		</dl>
		<dl class="node-health">
			<div>
				<dt class="sr-only">Health</dt>
				<dd><State value={node.state} quiet size="md" /></dd>
			</div>
		</dl>
		<div class="node-numbers min-w-0 text-[13px] leading-5 font-medium tabular-nums">
			<dl class="node-size">
				<div class="flex items-baseline gap-1 @2xl:block">
					<dt class="text-muted-foreground @2xl:sr-only">Size</dt>
					<dd>{node.size === null ? '—' : formatBytesBinary(node.size)}</dd>
				</div>
			</dl>
			<dl class="node-errors">
				{#each [{ label: 'Read', count: node.errors.read }, { label: 'Write', count: node.errors.write }, { label: 'Checksum', count: node.errors.checksum }] as error (error.label)}
					<div class="flex min-w-0 items-baseline gap-1 @2xl:block">
						<dt class="text-muted-foreground @2xl:sr-only">{error.label}</dt>
						<dd
							class="break-all {error.count !== null && error.count > 0
								? 'font-semibold text-red-600 dark:text-red-400'
								: 'text-muted-foreground/85 dark:text-muted-foreground/65'}"
							aria-label={`${error.label} errors: ${error.count === null ? 'unavailable' : error.count}`}
						>
							{error.count === null ? '—' : error.count.toLocaleString()}
						</dd>
					</div>
				{/each}
			</dl>
		</div>
	</div>
{/snippet}

{#snippet branches(values: DeviceNode[], depth = 0)}
	<ul
		class="topology-branches"
		class:tree-nested={depth > 0 && depth <= 3}
		data-topology-depth={depth}
		style:--tree-indent="{Math.min(depth, 3) * 16}px"
	>
		{#each values as node (node.id)}
			<li data-topology-node={node.children.length > 0 ? 'vdev' : 'device'}>
				{#if node.children.length > 0}
					<details>
						<summary
							class="focus-visible:ring-ring cursor-pointer list-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:outline-none @2xl:hover:bg-muted/30 [&::-webkit-details-marker]:hidden"
							aria-label={`${node.name} members`}>{@render row(node, depth)}</summary
						>
						{@render branches(node.children, depth + 1)}
					</details>
				{:else}{@render row(node, depth)}{/if}
			</li>
		{/each}
	</ul>
{/snippet}

<section class="bg-card @container flex min-w-0 flex-col rounded-lg border px-4 pt-2 pb-4">
	<header
		class="topology-row topology-head border-border/60 px-2 py-2.5 text-[13px] leading-5 font-medium"
		class:border-b={groups.length > 0}
	>
		<div class="node-identity flex min-w-0 items-center gap-2 text-base leading-6">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-7 shrink-0 items-center justify-center rounded-md"
				aria-hidden="true"
			>
				<span class="icon-[tabler--topology-bus] size-5"></span>
			</span>
			<h2 class="font-bold">Topology & Devices</h2>
		</div>
		{#if groups.length > 0}
			<span
				class="node-layout text-muted-foreground dark:text-muted-foreground/75"
				aria-hidden="true">Topology</span
			>
			<span
				class="node-members text-muted-foreground dark:text-muted-foreground/75"
				aria-hidden="true">Devices</span
			>
			<span
				class="node-health text-muted-foreground dark:text-muted-foreground/75"
				aria-hidden="true">Health</span
			>
			<div
				class="node-numbers text-muted-foreground dark:text-muted-foreground/75"
				aria-hidden="true"
			>
				<span class="node-size">Size</span>
				<div class="node-errors"><span>Read</span><span>Write</span><span>Checksum</span></div>
			</div>
		{/if}
	</header>
	{#if !status}<p class="text-muted-foreground my-2 px-2 text-xs">
			Device error counters are unavailable until detailed ZFS status can be read.
		</p>{/if}
	<div>
		{#each groups as group (group.key)}
			<section aria-label={group.label}>
				<div
					class="topology-row topology-group border-border/60 border-b px-2 py-2 text-[13px] leading-5 font-medium"
				>
					<div class="node-identity flex flex-wrap items-center gap-x-4 gap-y-0.5">
						<h3 class="text-zen-highlight-foreground flex items-center gap-2 text-sm font-semibold">
							<span
								class="bg-zen-highlight text-zen-highlight-foreground flex size-7 shrink-0 items-center justify-center rounded-md"
								aria-hidden="true"
							>
								<span class="{group.icon} size-5"></span>
							</span>
							{group.key === 'vdevs' ? 'Data' : group.label}
						</h3>
						<p
							class="node-description text-muted-foreground text-xs font-normal tabular-nums dark:text-muted-foreground/75"
						>
							{#if group.vdevs !== null}{group.vdevs}
								{group.vdevs === 1 ? 'VDEV' : 'VDEVs'}<span class="mx-1">·</span>
							{/if}{group.devices}
							{group.devices === 1 ? 'Device' : 'Devices'}
						</p>
					</div>
					<dl class="node-layout text-muted-foreground dark:text-muted-foreground/75">
						<dt class="sr-only">Topology</dt>
						<dd>
							{group.vdevs === null
								? '—'
								: `${group.vdevs} ${group.vdevs === 1 ? 'VDEV' : 'VDEVs'}`}
						</dd>
					</dl>
					<dl class="node-members text-muted-foreground tabular-nums dark:text-muted-foreground/75">
						<dt class="sr-only">Devices</dt>
						<dd>{group.devices}</dd>
					</dl>
				</div>
				{@render branches(group.nodes)}
			</section>
		{:else}<p class="text-muted-foreground px-2 py-3 text-sm">
				No device topology has been reported for this pool.
			</p>{/each}
	</div>
</section>

{#if actionOpen && pending}
	<DeviceAction
		{hostname}
		{pool}
		{pools}
		disks={inventoryUnavailable ? null : disks}
		{status}
		{statusUnavailable}
		{scanBusy}
		target={pending.target}
		action={pending.action}
		bind:open={actionOpen}
		{onChanged}
	/>
{/if}

<style>
	.topology-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		grid-template-areas: 'identity health' 'numbers numbers';
		align-items: center;
		gap: 0.25rem 1rem;
	}
	.node-identity {
		grid-area: identity;
		padding-left: calc(1.5rem + var(--tree-indent, 0px));
	}
	.node-health {
		grid-area: health;
	}
	.node-layout {
		grid-area: layout;
		display: none;
	}
	.node-members {
		grid-area: members;
		display: none;
		text-align: left;
	}
	.node-numbers {
		grid-area: numbers;
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.25rem 1rem;
		position: relative;
		z-index: 1;
		background: var(--card);
	}
	.node-errors {
		display: flex;
		flex-wrap: wrap;
		justify-content: flex-end;
		gap: 0.25rem 0.75rem;
	}
	.topology-head,
	.topology-group {
		grid-template-columns: minmax(0, 1fr);
		grid-template-areas: 'identity';
		gap: 0;
	}
	.topology-head .node-identity {
		padding-left: 0;
	}
	.topology-group .node-identity {
		padding-left: 0.5rem;
	}
	.topology-head .node-health,
	.topology-head .node-numbers {
		display: none;
	}
	.topology-branches > li {
		position: relative;
	}
	.tree-nested > li::before,
	.tree-nested > li > .topology-row::before,
	.tree-nested > li > details > summary > .topology-row::before,
	details[open] > summary > .tree-parent::after {
		content: '';
		position: absolute;
		border-color: var(--border);
		pointer-events: none;
	}
	.tree-nested > li::before {
		inset-block: 0;
		left: calc(var(--tree-indent) + 1.625rem);
		border-left-width: 1px;
	}
	.tree-nested > li:last-child::before {
		bottom: auto;
		height: 1.375rem;
	}
	.tree-nested > li > .topology-row::before,
	.tree-nested > li > details > summary > .topology-row::before {
		top: 1.375rem;
		left: calc(var(--tree-indent) + 1.625rem);
		width: 0.375rem;
		border-top-width: 1px;
	}
	details[open] > summary > .tree-parent::after {
		top: 2rem;
		bottom: 0;
		left: calc(var(--tree-indent) + 2.625rem);
		border-left-width: 1px;
	}
	@container (max-width: 27rem) {
		.topology-row:not(.topology-head):not(.topology-group) {
			grid-template-columns: minmax(0, 1fr);
			grid-template-areas: 'identity' 'health' 'numbers';
		}
		.node-health {
			padding-left: calc(1.5rem + var(--tree-indent, 0px));
		}
	}
	@container (min-width: 45rem) {
		.topology-row {
			grid-template-columns: minmax(0, 1fr) 6.5rem 20rem;
			grid-template-areas: 'identity health numbers';
		}
		.topology-head,
		.topology-group {
			column-gap: 1rem;
		}
		.node-numbers {
			display: grid;
			grid-template-columns: 4.5rem minmax(0, 1fr);
			gap: 1rem;
			background: none;
		}
		.node-size {
			text-align: right;
		}
		.node-errors {
			display: grid;
			grid-template-columns: repeat(3, minmax(0, 1fr));
			gap: 0.5rem;
			text-align: right;
		}
		.topology-head .node-health {
			display: block;
		}
		.topology-head .node-numbers {
			display: grid;
		}
	}
	@container (min-width: 60rem) {
		.topology-row {
			grid-template-columns: minmax(14rem, 2fr) 6.5rem 4rem minmax(6.5rem, 1fr) 20rem;
			grid-template-areas: 'identity layout members health numbers';
		}
		.node-layout,
		.node-members {
			display: block;
		}
		.node-description {
			display: none;
		}
	}
	details[open] > summary .vdev-toggle {
		transform: rotate(90deg);
	}
</style>
