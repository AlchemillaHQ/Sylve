<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { editPool } from '$lib/api/zfs/pool';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { allowed, nodePath, permissions } from '$lib/stores/permissions.svelte';
	import type { Disk } from '$lib/types/disk/disk';
	import type { Zpool, ZpoolStatusPool } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import {
		APIRequestError,
		getAPIErrorMessages,
		handleAPIError,
		isRequestCancellation,
		removeCache
	} from '$lib/utils/http';
	import { watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		booleanPoolSettings,
		failModes,
		changedPoolSettings,
		currentPoolSpares,
		poolEditBlockReason,
		poolEditValues,
		poolSpareChoices,
		poolSupportsSpares,
		readPoolEditSnapshot,
		sameSparePaths,
		type PoolEditSnapshot,
		type PoolEditValues
	} from './edit';

	let {
		hostname,
		pool,
		pools,
		disks,
		status,
		open = $bindable(false),
		onChanged
	}: {
		hostname: string;
		pool: Zpool;
		pools: Zpool[];
		disks: Disk[] | null;
		status: ZpoolStatusPool | null;
		open?: boolean;
		onChanged: () => Promise<void>;
	} = $props();
	const identity = untrack(() => ({ hostname, guid: pool.guid }));
	const initial: PoolEditSnapshot = untrack(() => ({ pool, pools, disks, status }));
	const id = $props.id();
	const lifetime = new AbortController();
	let reads = new AbortController();
	let baseline = $state.raw<PoolEditSnapshot>(initial);
	let draft = $state<PoolEditValues>(poolEditValues(initial.pool));
	let selectedSpares = $state<string[]>(
		currentPoolSpares(initial.pool, initial.status).flatMap((device) =>
			device.path ? [device.path] : []
		)
	);
	let query = $state('');
	let spareRowHeights = $state<Record<string, number>>({});
	let reloading = $state(false);
	let saving = $state(false);
	let reloadError = $state('');
	let saveError = $state('');
	let needsReload = $state(false);
	let canModify = $derived(allowed('Sys.Modify', nodePath(identity.hostname)));
	let liveBlock = $derived(poolEditBlockReason(pool, status));
	let currentSpares = $derived(currentPoolSpares(baseline.pool, baseline.status));
	let originalPaths = $derived(
		currentSpares.flatMap((device) => (device.path ? [device.path] : []))
	);
	let sparesReadable = $derived(
		currentSpares.every((device) => device.path !== null && device.guid)
	);
	let supportsSpares = $derived(poolSupportsSpares(baseline.pool));
	let choices = $derived(poolSpareChoices(baseline));
	let filteredChoices = $derived(
		choices.filter((choice) =>
			`${choice.path} ${choice.description}`
				.toLocaleLowerCase()
				.includes(query.trim().toLocaleLowerCase())
		)
	);
	let spareListHeight = $derived.by(() => {
		if (filteredChoices.length <= 2) return undefined;
		const heights = filteredChoices
			.slice(0, 2)
			.map((choice) => spareRowHeights[`${choice.path}:${choice.identity}`]);
		return heights.every((height) => height > 0)
			? `calc(${heights[0] + heights[1]}px + 0.5rem)`
			: '10rem';
	});
	let changes = $derived(changedPoolSettings(poolEditValues(baseline.pool), draft));
	let sparesChanged = $derived(!sameSparePaths(originalPaths, selectedSpares));
	let dirty = $derived(Object.keys(changes).length > 0 || sparesChanged);
	let valid = $derived(
		Object.entries(changes).every(([key, value]) =>
			key === 'comment'
				? !value.includes('\0')
				: key === 'failmode'
					? ['wait', 'continue', 'panic'].includes(value)
					: ['on', 'off'].includes(value)
		)
	);
	let formDisabled = $derived(
		reloading || saving || !canModify || Boolean(liveBlock || reloadError) || needsReload
	);
	let sparesDisabled = $derived(
		formDisabled || !supportsSpares || !sparesReadable || baseline.disks === null
	);

	function currentRequest(signal: AbortSignal) {
		const token = storage.token;
		const grants = permissions.data;
		return () => {
			signal.throwIfAborted();
			if (token !== storage.token || grants !== permissions.data)
				throw new DOMException('Pool edit access changed.', 'AbortError');
			if (!allowed('Sys.Modify', nodePath(identity.hostname)))
				throw new Error('Host modification permission is required.');
		};
	}

	function resetDraft() {
		if (saving) return;
		draft = poolEditValues(baseline.pool);
		selectedSpares = currentPoolSpares(baseline.pool, baseline.status).flatMap((device) =>
			device.path ? [device.path] : []
		);
		query = '';
		saveError = '';
	}

	async function reloadValues() {
		if (saving || reloading || lifetime.signal.aborted) return;
		reads.abort();
		reads = new AbortController();
		const signal = AbortSignal.any([reads.signal, lifetime.signal]);
		const check = currentRequest(signal);
		reloading = true;
		reloadError = '';
		try {
			check();
			const snapshot = await readPoolEditSnapshot(identity.hostname, identity.guid, signal);
			check();
			baseline = snapshot;
			needsReload = false;
			resetDraft();
		} catch (error) {
			if (!isRequestCancellation(error) && !signal.aborted)
				reloadError =
					error instanceof Error ? error.message : 'Pool settings could not be reloaded.';
		} finally {
			if (!signal.aborted) reloading = false;
		}
	}

	onDestroy(() => {
		lifetime.abort();
		reads.abort();
	});
	watch(
		[() => storage.token, () => permissions.data],
		() => {
			open = false;
		},
		{ lazy: true }
	);

	async function invalidate() {
		await Promise.all(
			[
				'pool-list',
				'zfs-pools-full',
				'disk-list-inventory',
				'total-disk-usage-full',
				`zen-pool-status-${identity.guid}`,
				`zfs-dashboard-snapshot:${identity.hostname}`
			].map((key) => removeCache(key, identity.hostname))
		);
	}

	function refreshDetails() {
		if (lifetime.signal.aborted) return;
		void onChanged().catch(() => {});
	}

	async function save(event: SubmitEvent) {
		event.preventDefault();
		if (formDisabled || !dirty || !valid) return;
		const initial = baseline;
		const requested = { ...changes };
		const sparePaths = [...selectedSpares];
		const changeSpares = sparesChanged;
		const check = currentRequest(lifetime.signal);
		let submitted = false;
		saving = true;
		saveError = '';
		try {
			check();
			const fresh = await readPoolEditSnapshot(
				identity.hostname,
				identity.guid,
				lifetime.signal,
				changeSpares
			);
			check();
			const before = poolEditValues(initial.pool);
			const current = poolEditValues(fresh.pool);
			for (const key of Object.keys(requested) as Array<keyof PoolEditValues>) {
				if (current[key] !== before[key] && current[key] !== requested[key])
					throw new Error(
						'Pool settings changed while the editor was open. Reload current values before saving.'
					);
				if (current[key] === requested[key]) delete requested[key];
			}
			let spares: string[] | undefined;
			if (changeSpares) {
				if (!poolSupportsSpares(fresh.pool) || !fresh.disks || !sparesReadable)
					throw new Error(
						'Spare assignments could not be verified. Reload current values before changing them.'
					);
				const existing = currentPoolSpares(fresh.pool, fresh.status);
				if (
					existing.some((device) => !device.path || !device.guid) ||
					!sameSparePaths(
						existing.map((device) => device.path!),
						originalPaths
					) ||
					existing.some(
						(device) =>
							!currentSpares.some(
								(previous) => previous.path === device.path && previous.guid === device.guid
							)
					)
				)
					throw new Error('The pool’s spare devices changed. Reload current values before saving.');
				const available = poolSpareChoices(fresh);
				for (const device of existing) {
					if (device.state.toUpperCase() === 'INUSE' && !sparePaths.includes(device.path!))
						throw new Error(`${device.label} is in use and cannot be removed from spares.`);
				}
				for (const path of sparePaths) {
					if (existing.some((device) => device.path === path)) continue;
					const candidate = available.find((device) => device.path === path);
					const previous = choices.find((device) => device.path === path);
					if (
						!candidate ||
						candidate.blocked ||
						!previous ||
						candidate.identity !== previous.identity
					)
						throw new Error(
							`${path} is no longer an eligible spare device. Reload current values before saving.`
						);
				}
				spares = sparePaths;
			}
			if (Object.keys(requested).length === 0 && spares === undefined) {
				refreshDetails();
				open = false;
				return;
			}
			await invalidate();
			check();
			try {
				submitted = true;
				const response = await editPool(identity.guid, requested, spares, {
					hostname: identity.hostname,
					signal: lifetime.signal
				});
				check();
				if (response.status !== 'success') {
					handleAPIError(response);
					throw new APIRequestError(response);
				}
			} finally {
				await invalidate();
				refreshDetails();
			}
			check();
			toast.success(`Pool ${fresh.pool.name} updated`, { position: 'bottom-center' });
			open = false;
		} catch (error) {
			if (!lifetime.signal.aborted && (submitted || !isRequestCancellation(error))) {
				const message =
					error instanceof Error ? error.message : 'Pool settings could not be saved.';
				const storageBlocked =
					error instanceof APIRequestError &&
					getAPIErrorMessages(error.response).some((detail) =>
						/^download_storage_in_use(?::|$)/.test(detail)
					);
				saveError = storageBlocked
					? 'This edit was blocked because the pool is used by download storage. No pool settings or spare assignments were changed.'
					: submitted
						? `${message} Some changes may already have been applied. Reload current values before trying again.`
						: message;
				needsReload = true;
			}
		} finally {
			if (!lifetime.signal.aborted) saving = false;
		}
	}
</script>

{#snippet hint(label: string, description: string, helpId: string)}
	<span id={helpId} class="sr-only">{description}</span>
	<Tooltip.Root delayDuration={150}>
		<Tooltip.Trigger
			type="button"
			aria-label="Help for {label}"
			class="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex size-5 shrink-0 items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
		>
			<span class="icon-[lucide--circle-help] size-3.5" aria-hidden="true"></span>
		</Tooltip.Trigger>
		<Tooltip.Content
			side="top"
			align="end"
			sideOffset={6}
			class="z-60 max-w-64 text-left leading-5"
		>
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

<Dialog.Root
	{open}
	onOpenChange={(value) => {
		if (!saving) open = value;
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		aria-busy={reloading || saving}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={save}>
			<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 gap-1 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="icon-[lucide--settings-2] text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 break-words">Edit Pool — {pool.name}</span>
					</Dialog.Title>
					<Dialog.Description class="text-xs">
						Change pool settings and spare device assignments.
					</Dialog.Description>
				</Dialog.Header>
				<Dialog.Close disabled={saving}>
					{#snippet child({ props })}
						<Button
							{...props}
							variant="ghost"
							size="icon"
							class="size-9 shrink-0"
							disabled={saving}
						>
							<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
							<span class="sr-only">Close</span>
						</Button>
					{/snippet}
				</Dialog.Close>
			</div>
			<div class="min-h-0 space-y-5 overflow-y-auto p-4">
				<div class="grid gap-4 sm:grid-cols-6">
					{#each booleanPoolSettings as field (field.key)}
						{#if field.key !== 'autoreplace' || supportsSpares}
							<div
								class="min-w-0 space-y-2 {supportsSpares &&
								['delegation', 'autoreplace'].includes(field.key)
									? 'sm:col-span-2'
									: 'sm:col-span-3'}"
							>
								<div class="flex min-h-5 items-center justify-between gap-2">
									<Label for="{id}-{field.key}">{field.label}</Label>
									{@render hint(
										field.label,
										field.key === 'autoreplace' &&
											selectedSpares.length === 0 &&
											draft.autoreplace !== 'on'
											? `${field.description} Add a spare device to enable Auto Replace.`
											: field.description,
										`${id}-${field.key}-help`
									)}
								</div>
								<Select.Root
									type="single"
									bind:value={draft[field.key]}
									disabled={formDisabled ||
										(field.key === 'autoreplace' &&
											selectedSpares.length === 0 &&
											draft.autoreplace !== 'on')}
								>
									<Select.Trigger
										id="{id}-{field.key}"
										class="bg-background w-full dark:hover:bg-input/30"
										aria-describedby="{id}-{field.key}-help"
									>
										{draft[field.key] === 'on'
											? 'Enabled'
											: draft[field.key] === 'off'
												? 'Disabled'
												: 'Not Reported'}
									</Select.Trigger>
									<Select.Content class="z-60">
										<Select.Item value="on" label="Enabled" />
										<Select.Item value="off" label="Disabled" />
									</Select.Content>
								</Select.Root>
							</div>
						{/if}
					{/each}
					<div class="min-w-0 space-y-2 {supportsSpares ? 'sm:col-span-2' : 'sm:col-span-3'}">
						<div class="flex min-h-5 items-center justify-between gap-2">
							<Label for="{id}-failmode">Fail Mode</Label>
							{@render hint(
								'Fail Mode',
								'Control how ZFS handles a catastrophic pool failure. Panic can crash this node if the pool fails.',
								`${id}-failmode-help`
							)}
						</div>
						<Select.Root type="single" bind:value={draft.failmode} disabled={formDisabled}>
							<Select.Trigger
								id="{id}-failmode"
								class="bg-background w-full dark:hover:bg-input/30"
								aria-describedby="{id}-failmode-help"
							>
								{failModes.find((mode) => mode.value === draft.failmode)?.label ?? 'Not Reported'}
							</Select.Trigger>
							<Select.Content class="z-60">
								{#each failModes as mode (mode.value)}
									<Select.Item value={mode.value} label={mode.label} />
								{/each}
							</Select.Content>
						</Select.Root>
					</div>
				</div>
				<div class="space-y-2">
					<Label for="{id}-comment">Comment</Label>
					<Textarea
						id="{id}-comment"
						bind:value={draft.comment}
						disabled={formDisabled}
						placeholder="Optional notes about this pool"
						class="bg-background min-h-20 text-sm"
					/>
				</div>
				{#if supportsSpares || currentSpares.length > 0}
					<fieldset class="min-w-0 space-y-2 border-t pt-1">
						<legend class="pr-2 text-sm font-semibold">
							<span class="inline-flex items-center gap-1.5">
								<span
									class="icon-[lucide--hard-drive] text-muted-foreground size-3.5"
									aria-hidden="true"
								></span>
								Spare Devices
							</span>
						</legend>
						<p class="text-muted-foreground text-xs leading-5">
							Existing spares stay assigned unless deselected. ZFS verifies device use and exact
							size when saving.
						</p>
						{#if !sparesReadable}
							<p class="text-muted-foreground text-xs leading-5" role="status">
								Some existing spare identities or paths could not be verified. Spare assignments
								will be left unchanged.
							</p>
						{:else if baseline.disks === null}
							<p class="text-muted-foreground text-xs leading-5" role="status">
								Disk inventory is unavailable. Pool settings can still be saved; spare assignments
								will be left unchanged.
							</p>
						{/if}
						{#if choices.length > 6}
							<Input
								type="search"
								bind:value={query}
								disabled={saving}
								placeholder="Find spare devices…"
								aria-label="Find spare devices"
							/>
						{/if}
						<ScrollArea
							class="min-w-0 rounded-md"
							fade
							fadeClasses="from-background"
							scrollbarYClasses="hidden"
							viewportProps={{
								class: 'overscroll-contain',
								style: spareListHeight ? `max-height: ${spareListHeight};` : undefined,
								role: 'region',
								'aria-label': 'Spare devices',
								tabindex: 0
							}}
						>
							<div class="space-y-2">
								{#each filteredChoices as choice, index (`${choice.path}:${choice.identity}`)}
									<label
										bind:offsetHeight={spareRowHeights[`${choice.path}:${choice.identity}`]}
										class="flex cursor-pointer items-start gap-3 rounded-md border p-3 text-sm"
										for="{id}-spare-{index}"
									>
										<Checkbox
											id="{id}-spare-{index}"
											class="mt-0.5"
											checked={selectedSpares.includes(choice.path)}
											disabled={sparesDisabled || Boolean(choice.blocked)}
											onCheckedChange={(checked) => {
												if (sparesDisabled || choice.blocked) return;
												selectedSpares = checked
													? [...selectedSpares, choice.path]
													: selectedSpares.filter((path) => path !== choice.path);
											}}
										/>
										<span class="min-w-0 flex-1">
											<span class="flex flex-wrap items-baseline justify-between gap-2">
												<span class="font-mono text-[13px] break-all">{choice.path}</span>
												<span class="text-muted-foreground text-xs tabular-nums">
													{choice.size > 0 ? formatBytesBinary(choice.size) : 'Size not reported'}
												</span>
											</span>
											<span class="text-muted-foreground mt-1 block text-xs leading-5 break-words">
												{choice.description}
											</span>
											{#if choice.blocked}
												<span class="text-muted-foreground mt-1 block text-xs leading-5"
													>{choice.blocked}</span
												>
											{/if}
										</span>
									</label>
								{:else}
									<p
										class="text-muted-foreground rounded-md border border-dashed px-4 py-5 text-center text-sm"
									>
										{query
											? 'No matching spare devices.'
											: 'No eligible spare devices are available.'}
									</p>
								{/each}
							</div>
						</ScrollArea>
					</fieldset>
				{/if}
				{#if liveBlock}<p class="text-muted-foreground text-sm" role="status">{liveBlock}</p>{/if}
				{#if !canModify}
					<p class="text-muted-foreground text-sm" role="status">
						Host modification permission is required.
					</p>
				{/if}
				{#if saveError || reloadError}
					<div class="space-y-3">
						<p class="text-destructive text-sm break-words" role="alert">
							{reloadError || saveError}
						</p>
						{#if needsReload || reloadError}
							<Button
								type="button"
								variant="outline"
								size="sm"
								disabled={saving || reloading}
								onclick={reloadValues}
							>
								{reloading ? 'Reloading…' : 'Reload Current Values'}
							</Button>
						{/if}
					</div>
				{/if}
			</div>
			<div class="flex shrink-0 items-center justify-between gap-3 border-t p-4">
				<Button
					type="button"
					variant="outline"
					class="h-9"
					disabled={formDisabled || !dirty}
					onclick={resetDraft}
				>
					Reset
				</Button>
				<Button type="submit" class="h-9 gap-2" disabled={formDisabled || !dirty || !valid}>
					{#if saving}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{saving ? 'Saving…' : 'Save Changes'}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
