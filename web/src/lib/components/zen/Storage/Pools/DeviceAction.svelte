<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { attachDevice, detachDevice, replaceDevice } from '$lib/api/zfs/pool';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { allowed, nodePath, permissions } from '$lib/stores/permissions.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Disk } from '$lib/types/disk/disk';
	import type { Zpool, ZpoolStatusPool } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import {
		APIRequestError,
		handleAPIError,
		isRequestCancellation,
		removeCache
	} from '$lib/utils/http';
	import { watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		findPoolDeviceTarget,
		poolDeviceActionReason,
		poolDeviceFingerprint,
		poolDevicePreconditions,
		replacementDeviceChoices,
		type PoolDeviceAction,
		type PoolDeviceTarget
	} from './devices';
	import { readPoolEditSnapshot, type PoolEditSnapshot } from './edit';
	import State from './State.svelte';

	let {
		hostname,
		pool,
		pools,
		disks,
		status,
		statusUnavailable,
		scanBusy,
		target,
		action,
		open = $bindable(false),
		onChanged
	}: {
		hostname: string;
		pool: Zpool;
		pools: Zpool[];
		disks: Disk[] | null;
		status: ZpoolStatusPool | null;
		statusUnavailable: boolean;
		scanBusy: boolean;
		target: PoolDeviceTarget;
		action: PoolDeviceAction;
		open?: boolean;
		onChanged: () => Promise<void>;
	} = $props();
	const identity = untrack(() => ({ hostname, guid: pool.guid, name: pool.name, action }));
	const device = untrack(() => target);
	const baseline: PoolEditSnapshot = untrack(() => ({ pool, pools, disks, status }));
	const id = $props.id();
	const lifetime = new AbortController();
	const detaching = identity.action === 'detach';
	const parent = device.ancestors.at(-1);
	const attachVdev = parent?.kind.toLowerCase() === 'mirror' ? parent : device.node;
	const confirmationName = identity.action === 'attach' ? attachVdev.name : device.node.name;
	const operation = {
		attach: {
			label: 'Attach Device',
			icon: 'icon-[lucide--plug]',
			description: 'Add a mirror member to this VDEV without replacing the existing device.'
		},
		replace: {
			label: 'Replace Device',
			icon: 'icon-[lucide--replace]',
			description: 'Select a replacement and confirm the device change.'
		},
		detach: {
			label: 'Detach Device',
			icon: 'icon-[lucide--unplug]',
			description: 'Remove one member from its mirror, reducing redundancy.'
		}
	}[identity.action];
	let selectedPath = $state('');
	let confirmation = $state('');
	let saving = $state(false);
	let needsReopen = $state(false);
	let errorMessage = $state('');
	let canModify = $derived(allowed('Sys.Modify', nodePath(identity.hostname)));
	let choices = $derived(detaching ? [] : replacementDeviceChoices(baseline, device));
	let selected = $derived(choices.find((choice) => choice.path === selectedPath) ?? null);
	let remaining = $derived(
		parent?.children.filter((member) => member.guid !== device.node.guid) ?? []
	);
	let liveTarget = $derived(findPoolDeviceTarget({ pool, pools, disks, status }, device));
	let liveReason = $derived.by(() => {
		if (pool.name !== identity.name)
			return 'The pool identity changed. Close this dialog and reopen it.';
		if (statusUnavailable) return 'Refresh the pool status before changing devices.';
		if (scanBusy)
			return 'Wait for the current or queued scrub or resilver to finish before changing devices.';
		const reason = poolDeviceActionReason(pool, status, liveTarget, identity.action);
		if (reason) return reason;
		return liveTarget && poolDeviceFingerprint(liveTarget) !== poolDeviceFingerprint(device)
			? 'The device topology changed. Close and reopen this dialog to review it again.'
			: '';
	});
	let disabled = $derived(saving || needsReopen || !canModify || Boolean(liveReason));
	let valid = $derived(
		confirmation.length > 0 &&
			confirmation === confirmationName &&
			(detaching || (selected !== null && !selected.blocked))
	);

	function currentRequest() {
		const token = storage.token;
		const grants = permissions.data;
		return () => {
			lifetime.signal.throwIfAborted();
			if (token !== storage.token || grants !== permissions.data)
				throw new DOMException('Device action access changed.', 'AbortError');
			if (!allowed('Sys.Modify', nodePath(identity.hostname)))
				throw new Error('Host modification permission is required.');
		};
	}

	function verifyTarget(snapshot: PoolEditSnapshot) {
		if (snapshot.pool.name !== identity.name) throw new Error('The pool identity changed.');
		const current = findPoolDeviceTarget(snapshot, device);
		const reason = poolDeviceActionReason(snapshot.pool, snapshot.status, current, identity.action);
		if (reason || !current) throw new Error(reason || 'The source device is no longer available.');
		return current;
	}

	function refreshDetails() {
		if (!lifetime.signal.aborted) void onChanged().catch(() => {});
	}

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

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		if (disabled || !valid) return;
		const candidate = selected;
		const check = currentRequest();
		let submitted = false;
		saving = true;
		errorMessage = '';
		try {
			check();
			const fresh = await readPoolEditSnapshot(
				identity.hostname,
				identity.guid,
				lifetime.signal,
				!detaching
			);
			check();
			const current = verifyTarget(fresh);
			if (poolDeviceFingerprint(current) !== poolDeviceFingerprint(device))
				throw new Error('The device topology changed.');
			if (!detaching) {
				if (!fresh.disks || !candidate) throw new Error('New device inventory is unavailable.');
				const available = replacementDeviceChoices(fresh, current).find(
					(choice) => choice.path === candidate.path
				);
				if (!available || available.blocked || available.identity !== candidate.identity)
					throw new Error('The selected device changed or is no longer eligible.');
			}
			await invalidate();
			check();
			try {
				submitted = true;
				const options = { hostname: identity.hostname, signal: lifetime.signal };
				let response: APIResponse;
				if (detaching) {
					response = await detachDevice(
						identity.guid,
						current.node.guid,
						options,
						poolDevicePreconditions(device)
					);
				} else {
					const request = {
						...poolDevicePreconditions(device),
						guid: identity.guid,
						new: candidate!.path,
						expectedNewIdentity: candidate!.identity
					};
					response =
						identity.action === 'attach'
							? await attachDevice({ ...request, device: current.node.guid }, options)
							: await replaceDevice({ ...request, old: current.node.guid }, options);
				}
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
			toast.success(
				identity.action === 'attach'
					? `Attachment requested for ${candidate!.path} in ${identity.name}`
					: detaching
						? `Detached ${device.node.name} from ${identity.name}`
						: `Replacement requested for ${device.node.name} in ${identity.name}`,
				{ position: 'bottom-center' }
			);
			open = false;
		} catch (error) {
			if (!lifetime.signal.aborted && (submitted || !isRequestCancellation(error))) {
				const message =
					error instanceof Error ? error.message : 'The device operation could not be completed.';
				errorMessage = submitted
					? `${message} The request may have been applied. Close and reopen this dialog to review the current topology before retrying.`
					: `${message} No device change was submitted. Close and reopen this dialog to try again.`;
				needsReopen = true;
				if (!submitted) refreshDetails();
			}
		} finally {
			if (!lifetime.signal.aborted) saving = false;
		}
	}

	watch(
		[() => storage.token, () => permissions.data],
		() => {
			open = false;
		},
		{ lazy: true }
	);
	onDestroy(() => lifetime.abort());
</script>

<Dialog.Root
	{open}
	onOpenChange={(value) => {
		if (!saving) open = value;
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-xl"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		aria-busy={saving}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={submit}>
			<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 gap-1 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="{operation.icon} text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span>{operation.label}</span>
					</Dialog.Title>
					<Dialog.Description class="text-xs">
						{operation.description}
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
			<div class="min-h-0 space-y-4 overflow-y-auto p-4">
				<dl
					class="grid grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-4 gap-y-2 rounded-md border p-3 text-sm"
				>
					<dt class="text-muted-foreground">Pool</dt>
					<dd class="break-all font-medium">{identity.name}</dd>
					{#if identity.action === 'attach'}
						<dt class="text-muted-foreground">VDEV</dt>
						<dd class="font-mono text-[13px] break-all">{attachVdev.name}</dd>
					{/if}
					<dt class="text-muted-foreground">
						{identity.action === 'attach' ? 'Existing Member' : 'Device'}
					</dt>
					<dd class="font-mono text-[13px] break-all">{device.node.path || device.node.name}</dd>
					<dt class="text-muted-foreground">GUID</dt>
					<dd class="font-mono text-xs break-all">{device.node.guid}</dd>
					<dt class="text-muted-foreground">Health</dt>
					<dd><State value={device.node.state} /></dd>
					{#if !detaching}
						<dt class="text-muted-foreground">Required Size</dt>
						<dd class="tabular-nums">
							{device.node.replacementSize === null
								? 'Unavailable'
								: formatBytesBinary(device.node.replacementSize)}
						</dd>
					{:else}
						<dt class="text-muted-foreground">Remaining</dt>
						<dd class="font-mono text-[13px] break-all">
							{remaining.map((member) => member.path || member.name).join(', ')}
						</dd>
					{/if}
				</dl>
				{#if !detaching}
					<div class="space-y-2">
						<Label for="{id}-replacement">
							{identity.action === 'attach' ? 'New Mirror Device' : 'Replacement Device'}
						</Label>
						<Select.Root
							type="single"
							bind:value={selectedPath}
							disabled={disabled || baseline.disks === null}
						>
							<Select.Trigger
								id="{id}-replacement"
								class="bg-background w-full dark:hover:bg-input/30"
							>
								<span class="min-w-0 truncate">
									{selected
										? `${selected.path} · ${formatBytesBinary(selected.size)}`
										: 'Select an unused device'}
								</span>
							</Select.Trigger>
							<Select.Content class="z-60 max-h-64">
								{#each choices as choice (choice.path)}
									<Select.Item
										value={choice.path}
										label={choice.path}
										disabled={Boolean(choice.blocked)}
									>
										<div class="min-w-0 py-0.5">
											<p class="font-mono text-[13px] break-all">
												{choice.path}
												<span class="text-muted-foreground font-sans text-xs"
													>· {formatBytesBinary(choice.size)}</span
												>
											</p>
											<p class="text-muted-foreground mt-0.5 text-xs break-words">
												{choice.blocked || choice.description}
											</p>
										</div>
									</Select.Item>
								{:else}
									<p class="text-muted-foreground px-2 py-3 text-sm">No unused devices found.</p>
								{/each}
							</Select.Content>
						</Select.Root>
						{#if selected}
							<p class="text-muted-foreground text-xs break-words">{selected.description}</p>
						{/if}
						{#if baseline.disks === null}
							<p class="text-muted-foreground text-xs" role="status">
								Disk inventory is unavailable. Close this dialog and refresh the pool details before
								retrying.
							</p>
						{:else if !choices.some((choice) => !choice.blocked)}
							<p class="text-muted-foreground text-xs" role="status">
								No eligible device is available. Use an unused disk or partition at least as large
								as the required size; boot, swap and assigned devices are excluded.
							</p>
						{/if}
					</div>
				{/if}
				<div class="flex items-start gap-2.5 rounded-md border p-3 text-sm leading-5" role="note">
					<span
						class="icon-[lucide--triangle-alert] mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
						aria-hidden="true"
					></span>
					<p>
						{#if identity.action === 'attach'}
							This adds a mirror member, not extra pool capacity. Data on the new device may be
							overwritten, and ZFS will resilver it. Existing pool labels are checked without
							forcing the attachment.
						{:else if !detaching}
							Data on the replacement device may be overwritten. ZFS may start a resilver and will
							check exact size and existing pool labels without forcing the replacement.
						{:else if remaining.length === 1}
							This leaves only one device in this VDEV and removes its mirror redundancy. Detaching
							does not securely erase the detached device.
						{:else}
							This reduces the mirror to {remaining.length} devices. Detaching does not securely erase
							the detached device.
						{/if}
					</p>
				</div>
				<div class="space-y-2">
					<Label for="{id}-confirmation" class="block leading-5">
						Type <span class="font-mono break-all">{confirmationName}</span> to confirm
					</Label>
					<Input
						id="{id}-confirmation"
						bind:value={confirmation}
						placeholder={confirmationName}
						{disabled}
						autocomplete="off"
						autocapitalize="none"
						spellcheck={false}
					/>
				</div>
				{#if liveReason}<p class="text-muted-foreground text-sm" role="status">{liveReason}</p>{/if}
				{#if !canModify}
					<p class="text-muted-foreground text-sm" role="status">
						Host modification permission is required.
					</p>
				{/if}
				{#if errorMessage}
					<p class="text-destructive text-sm break-words" role="alert">{errorMessage}</p>
				{/if}
			</div>
			<div class="flex shrink-0 items-center justify-end gap-3 border-t p-4">
				<Button
					type="submit"
					variant={detaching ? 'destructive' : 'default'}
					disabled={disabled || !valid}
				>
					{#if saving}<span
							class="icon-[lucide--loader-circle] size-4 animate-spin"
							aria-hidden="true"
						></span>{/if}
					{saving ? 'Applying…' : operation.label}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
