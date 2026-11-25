<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { createPool } from '$lib/api/zfs/pool';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { Disk } from '$lib/types/disk/disk';
	import type { VdevType, Zpool } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, tick, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import Panel from '../../Dashboard/Panel.svelte';
	import {
		createDeviceChoices,
		createError,
		deviceIcon,
		layoutLabel,
		layoutsFor,
		nameProblem,
		poolProblems,
		roleLabel,
		roleOptions,
		spareSupport,
		usableEstimate,
		vdevProblem,
		vdevWarning,
		type CreateDevice,
		type DraftVdev,
		type Layout
	} from './create';
	import { booleanPoolSettings, failModes } from './edit';
	import { poolHref } from './pools';

	let {
		hostname,
		pools,
		disks,
		inventoryUnavailable,
		canModify,
		onCreated
	}: {
		hostname: string;
		pools: Zpool[];
		disks: Disk[];
		inventoryUnavailable: boolean;
		canModify: boolean;
		onCreated: (name: string) => Promise<void>;
	} = $props();

	const id = $props.id();
	let destroyed = false;
	onDestroy(() => (destroyed = true));

	let name = $state('');
	let nameTouched = $state(false);
	let mountpoint = $state('');
	let comment = $state('');
	let nextKey = 1;
	let vdevs = $state<DraftVdev[]>([{ key: 0, role: 'data', layout: 'stripe', devices: [] }]);
	let spares = $state<string[]>([]);
	let selected = $state<string[]>([]);
	let target = $state('0');
	let query = $state('');
	let settings = $state<Record<string, string>>({
		ashift: '12',
		autoexpand: 'off',
		autotrim: 'off',
		delegation: 'on',
		autoreplace: 'off',
		failmode: 'wait'
	});
	let force = $state(false);
	let advancedOpen = $state(false);
	let attempted = $state(false);
	let creating = $state(false);
	let error = $state('');

	let choices = $derived(createDeviceChoices(pools, disks));
	let byPath = $derived(new Map(choices.map((choice) => [choice.path, choice])));
	let assigned = $derived(new Set([...vdevs.flatMap((vdev) => vdev.devices), ...spares]));
	let available = $derived(choices.filter((choice) => !assigned.has(choice.path)));
	let shown = $derived(
		available.filter((choice) =>
			`${choice.name} ${choice.description}`.toLowerCase().includes(query.trim().toLowerCase())
		)
	);
	let spareDevices = $derived(spares.flatMap((path) => byPath.get(path) ?? []));
	let canSpare = $derived(spareSupport(vdevs));
	let targets = $derived([
		...vdevs.map((vdev, index) => ({
			value: String(vdev.key),
			label: `VDEV ${index + 1} · ${roleLabel(vdev.role)}`
		})),
		...(canSpare || spares.length > 0 ? [{ value: 'spares', label: 'Spares' }] : [])
	]);
	let destination = $derived(
		targets.some((option) => option.value === target) ? target : (targets[0]?.value ?? '')
	);
	let problemName = $derived(nameProblem(name, pools));
	let nameError = $derived(attempted || (nameTouched && name.trim()) ? problemName : '');
	let problems = $derived(poolProblems(vdevs, spareDevices, byPath, force));
	let usable = $derived(usableEstimate(vdevs, byPath));
	let valid = $derived(
		!problemName &&
			vdevs.every((vdev) => !vdevProblem(vdev, devicesOf(vdev), force)) &&
			problems.length === 0
	);
	let ashiftOptions = $derived(
		Array.from({ length: 8 }, (_, index) => {
			const value = String(index + 9);
			return { value, label: `${value} · ${formatBytesBinary(2 ** (index + 9))} sectors` };
		})
	);

	$effect(() => {
		const eligible = new Set(
			choices.filter((choice) => !choice.blocked).map((choice) => choice.path)
		);
		untrack(() => {
			for (const vdev of vdevs) {
				if (vdev.devices.some((path) => !eligible.has(path))) {
					vdev.devices = vdev.devices.filter((path) => eligible.has(path));
				}
			}
			if (spares.some((path) => !eligible.has(path)))
				spares = spares.filter((path) => eligible.has(path));
			if (selected.some((path) => !eligible.has(path)))
				selected = selected.filter((path) => eligible.has(path));
		});
	});

	function devicesOf(vdev: DraftVdev): CreateDevice[] {
		return vdev.devices.flatMap((path) => byPath.get(path) ?? []);
	}

	function toggle(path: string, checked: boolean) {
		selected = checked ? [...selected, path] : selected.filter((item) => item !== path);
	}

	function addSelected() {
		const paths = selected.filter(
			(path) => byPath.get(path)?.blocked === '' && !assigned.has(path)
		);
		if (paths.length === 0) return;
		if (destination === 'spares') spares = [...spares, ...paths];
		else {
			const vdev = vdevs.find((item) => String(item.key) === destination);
			if (!vdev) return;
			vdev.devices = [...vdev.devices, ...paths];
		}
		selected = [];
	}

	async function addVdev() {
		const key = nextKey++;
		const layout = vdevs.find((vdev) => vdev.role === 'data')?.layout ?? 'stripe';
		vdevs.push({ key, role: 'data', layout, devices: [] });
		target = String(key);
		await tick();
		document
			.getElementById(`${id}-vdev-${key}`)
			?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
	}

	function setRole(vdev: DraftVdev, role: VdevType) {
		vdev.role = role;
		if (!layoutsFor(role).some((option) => option.value === vdev.layout)) vdev.layout = 'stripe';
	}

	async function create(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		nameTouched = true;
		if (creating || !valid || !canModify) return;
		const poolName = name.trim();
		creating = true;
		error = '';
		try {
			const response = await createPool(
				{
					name: poolName,
					vdevs: vdevs.map((vdev, index) => ({
						name: `vdev-${index}`,
						type: vdev.role,
						raidType: vdev.layout,
						devices: [...vdev.devices]
					})),
					properties: {
						ashift: settings.ashift,
						autoexpand: settings.autoexpand,
						autotrim: settings.autotrim,
						delegation: settings.delegation,
						failmode: settings.failmode,
						...(spares.length > 0 ? { autoreplace: settings.autoreplace } : {}),
						...(comment.trim() ? { comment: comment.trim() } : {})
					},
					...(mountpoint.trim() ? { mountpoint: mountpoint.trim() } : {}),
					spares: [...spares],
					createForce: force
				},
				{ hostname }
			);
			if (destroyed) return;
			if (response.status !== 'success') {
				error = createError(response);
				handleAPIError(response);
				return;
			}
			toast.success(`Pool ${poolName} created`, { position: 'bottom-center' });
			await onCreated(poolName);
		} catch (caught) {
			if (!isRequestCancellation(caught) && !destroyed)
				error = 'Could not create the pool. Please try again.';
		} finally {
			if (!destroyed) creating = false;
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
		<Tooltip.Content side="top" align="end" sideOffset={6} class="max-w-64 text-left leading-5">
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet field(key: string, label: string, description: string)}
	<div class="flex min-h-5 items-center justify-between gap-2">
		<Label for="{id}-{key}">{label}</Label>
		{@render hint(label, description, `${id}-${key}-help`)}
	</div>
{/snippet}

{#snippet deviceChips(devices: CreateDevice[], label: string, onRemove: (path: string) => void)}
	<ul class="flex flex-wrap gap-2" aria-label={label}>
		{#each devices as device (device.path)}
			<li
				class="bg-muted/50 inline-flex max-w-full items-center gap-1.5 rounded-md border py-1 pr-1 pl-2 text-xs"
				title={device.description}
			>
				<span
					class="{deviceIcon(device)} text-muted-foreground size-3.5 shrink-0"
					aria-hidden="true"
				></span>
				<span class="flex min-w-0 items-baseline gap-1.5 leading-4">
					<span class="min-w-0 truncate font-mono text-[12px] font-medium">{device.name}</span>
					<span class="text-muted-foreground shrink-0 tabular-nums"
						>{formatBytesBinary(device.size)}</span
					>
				</span>
				<span class="sr-only">{device.description}</span>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					class="size-6 shrink-0"
					disabled={creating}
					aria-label="Remove {device.name}"
					onclick={() => onRemove(device.path)}
					><span class="icon-[lucide--x] size-3.5" aria-hidden="true"></span></Button
				>
			</li>
		{/each}
	</ul>
{/snippet}

<header class="mb-5 flex items-center gap-3">
	<span
		class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
		aria-hidden="true"><span class="icon-[bi--hdd-stack-fill] size-5"></span></span
	>
	<Breadcrumb.Root class="min-w-0">
		<Breadcrumb.List class="items-baseline gap-2 sm:gap-2">
			<Breadcrumb.Item>
				<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
			</Breadcrumb.Item>
			<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
			<Breadcrumb.Item>
				<Breadcrumb.Link
					href={poolHref(hostname)}
					class="focus-visible:ring-ring rounded-sm text-lg font-medium tracking-tight focus-visible:ring-2 focus-visible:outline-none sm:text-xl"
					>Pools</Breadcrumb.Link
				>
			</Breadcrumb.Item>
			<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
			<Breadcrumb.Item>
				<h1 class="text-lg leading-7 tracking-tight sm:text-xl">
					<Breadcrumb.Page class="font-semibold">New Pool</Breadcrumb.Page>
				</h1>
			</Breadcrumb.Item>
		</Breadcrumb.List>
	</Breadcrumb.Root>
</header>

{#if !canModify}
	<p class="text-muted-foreground mb-4 text-sm" role="status">
		Creating pools requires permission to modify this node.
	</p>
{:else if inventoryUnavailable}
	<p
		class="mb-4 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2.5 text-sm"
		role="status"
	>
		The disk inventory could not be loaded, so no devices can be offered. Refresh and try again.
	</p>
{/if}

<form class="grid gap-4 @4xl:grid-cols-[minmax(0,1fr)_24rem]" onsubmit={create}>
	<div class="flex min-w-0 flex-col gap-4">
		<Panel title="General" icon="icon-[lucide--settings-2]">
			<div class="grid gap-4 sm:grid-cols-2">
				<div class="min-w-0 space-y-2">
					<div class="flex min-h-5 items-center gap-1">
						<Label for="{id}-name">Name</Label>
						{#if nameError}
							<span id="{id}-name-error" class="text-destructive text-xs">({nameError})</span>
						{/if}
					</div>
					<Input
						id="{id}-name"
						bind:value={name}
						placeholder="tank"
						maxlength={24}
						autocomplete="off"
						spellcheck={false}
						disabled={creating}
						onblur={() => (nameTouched = true)}
						aria-invalid={nameError ? true : undefined}
						aria-describedby={nameError ? `${id}-name-error` : undefined}
					/>
				</div>
				<div class="min-w-0 space-y-2">
					{@render field(
						'mountpoint',
						'Mount Point',
						'Where the pool’s root dataset is mounted. Leave empty to use /name.'
					)}
					<Input
						id="{id}-mountpoint"
						bind:value={mountpoint}
						placeholder={`/${name.trim() || 'tank'}`}
						autocomplete="off"
						spellcheck={false}
						disabled={creating}
						aria-describedby="{id}-mountpoint-help"
					/>
				</div>
				<div class="min-w-0 space-y-2 sm:col-span-2">
					<Label for="{id}-comment">Comment</Label>
					<Input
						id="{id}-comment"
						bind:value={comment}
						placeholder="Optional notes about this pool"
						disabled={creating}
					/>
				</div>
			</div>
		</Panel>

		<Panel title="VDEVs" icon="icon-[tabler--topology-bus]">
			{#snippet aside()}
				<Button
					type="button"
					variant="outline"
					size="sm"
					class="h-8 gap-1.5"
					disabled={creating}
					onclick={addVdev}
				>
					<span class="icon-[lucide--plus] size-3.5" aria-hidden="true"></span>Add VDEV
				</Button>
			{/snippet}
			<ol class="space-y-3">
				{#each vdevs as vdev, index (vdev.key)}
					{@const members = devicesOf(vdev)}
					{@const problem = vdevProblem(vdev, members, force)}
					{@const warning = vdevWarning(members)}
					<li id="{id}-vdev-{vdev.key}" class="rounded-md border">
						<div class="flex flex-wrap items-center gap-2 border-b px-3 py-2.5">
							<span class="mr-auto text-sm font-medium">VDEV {index + 1}</span>
							<Select.Root
								type="single"
								value={vdev.role}
								onValueChange={(value) => setRole(vdev, value as VdevType)}
								disabled={creating}
							>
								<Select.Trigger
									class="bg-background h-8 w-36 dark:hover:bg-input/30"
									aria-label="Role for VDEV {index + 1}"
								>
									{roleLabel(vdev.role)}
								</Select.Trigger>
								<Select.Content>
									{#each roleOptions as option (option.value)}
										<Select.Item value={option.value} label={option.label} />
									{/each}
								</Select.Content>
							</Select.Root>
							<Select.Root
								type="single"
								value={vdev.layout}
								onValueChange={(value) => (vdev.layout = value as Layout)}
								disabled={creating}
							>
								<Select.Trigger
									class="bg-background h-8 w-28 dark:hover:bg-input/30"
									aria-label="Layout for VDEV {index + 1}"
								>
									{layoutLabel(vdev.layout)}
								</Select.Trigger>
								<Select.Content>
									{#each layoutsFor(vdev.role) as option (option.value)}
										<Select.Item
											value={option.value}
											label="{option.label} · {option.minimum}+ devices"
										/>
									{/each}
								</Select.Content>
							</Select.Root>
							<Button
								type="button"
								variant="outline"
								size="icon"
								class="text-destructive hover:text-destructive size-8"
								disabled={creating || vdevs.length === 1}
								aria-label="Remove VDEV {index + 1}"
								onclick={() => vdevs.splice(index, 1)}
								><span class="icon-[lucide--trash-2] size-3.5" aria-hidden="true"></span></Button
							>
						</div>
						{#if members.length > 0}
							<div class="p-3">
								{@render deviceChips(members, `VDEV ${index + 1} devices`, (removed) => {
									vdev.devices = vdev.devices.filter((path) => path !== removed);
								})}
							</div>
						{:else}
							<p class="text-muted-foreground px-3 py-4 text-center text-sm">
								No devices yet. Add them from Available Devices.
							</p>
						{/if}
						{#if (attempted || members.length > 0) && problem}
							<p class="text-destructive border-t px-3 py-2 text-xs leading-5" role="alert">
								{problem}
							</p>
						{:else if warning}
							<p class="border-t px-3 py-2 text-xs leading-5 text-amber-700 dark:text-amber-400">
								{warning}
							</p>
						{/if}
					</li>
				{/each}
			</ol>
		</Panel>

		{#if canSpare || spares.length > 0}
			<Panel title="Spares" icon="icon-[lucide--life-buoy]">
				{#if spareDevices.length > 0}
					{@render deviceChips(spareDevices, 'Spare devices', (removed) => {
						spares = spares.filter((path) => path !== removed);
					})}
				{:else}
					<p class="text-muted-foreground text-sm">
						Spares take over automatically when a device fails. Add them from Available Devices.
					</p>
				{/if}
			</Panel>
		{/if}
	</div>

	<Panel title="Available Devices" icon="icon-[mdi--harddisk]" class="@4xl:min-h-0">
		{#snippet aside()}{available.filter((choice) => !choice.blocked).length} free{/snippet}
		{#if choices.length > 6}
			<Input
				type="search"
				class="mb-3"
				bind:value={query}
				placeholder="Find devices…"
				aria-label="Find devices"
				disabled={creating}
			/>
		{/if}
		<ScrollArea
			class="max-h-96 overflow-hidden rounded-md border @4xl:max-h-none @4xl:min-h-40 @4xl:flex-1 @4xl:basis-0"
			fade
			fadeClasses="from-card"
			scrollbarYClasses="hidden"
			viewportProps={{
				class: 'max-h-96 overscroll-contain @4xl:max-h-none',
				role: 'region',
				'aria-label': 'Available devices',
				tabindex: 0
			}}
		>
			<ul class="divide-y">
				{#each shown as device (device.path)}
					<li>
						<label
							class="flex items-start gap-3 px-3 py-2 {device.blocked
								? 'cursor-not-allowed opacity-60'
								: 'cursor-pointer'}"
							for="{id}-device-{device.name}"
						>
							<Checkbox
								id="{id}-device-{device.name}"
								class="mt-0.5"
								checked={selected.includes(device.path)}
								disabled={creating || Boolean(device.blocked)}
								onCheckedChange={(checked) => toggle(device.path, checked === true)}
							/>
							<span class="min-w-0 flex-1">
								<span class="flex items-baseline gap-x-1.5 text-sm">
									<span
										class="{deviceIcon(device)} text-muted-foreground size-3.5 shrink-0 self-center"
										aria-hidden="true"
									></span>
									<span class="min-w-0 truncate font-mono text-[13px] font-medium"
										>{device.name}</span
									>
									<span class="text-muted-foreground ml-auto shrink-0 pl-2 text-xs tabular-nums">
										{formatBytesBinary(device.size)}
									</span>
								</span>
								<span class="text-muted-foreground block text-xs break-words"
									>{device.description}</span
								>
								{#if device.blocked}
									<span class="text-muted-foreground mt-0.5 block text-xs leading-5"
										>{device.blocked}</span
									>
								{/if}
							</span>
						</label>
					</li>
				{:else}
					<li class="text-muted-foreground px-4 py-6 text-center text-sm">
						{query
							? 'No matching devices.'
							: choices.length > 0
								? 'Every free device has been added.'
								: 'No free disks or partitions on this node.'}
					</li>
				{/each}
			</ul>
		</ScrollArea>
		<div class="mt-3 flex items-center gap-2">
			<Select.Root type="single" bind:value={target} disabled={creating}>
				<Select.Trigger
					class="bg-background h-9 min-w-0 flex-1 dark:hover:bg-input/30"
					aria-label="Add selected devices to"
				>
					<span class="truncate"
						>To {targets.find((option) => option.value === destination)?.label}</span
					>
				</Select.Trigger>
				<Select.Content>
					{#each targets as option (option.value)}
						<Select.Item value={option.value} label={option.label} />
					{/each}
				</Select.Content>
			</Select.Root>
			<Button
				type="button"
				variant="outline"
				class="h-9 shrink-0 gap-1.5"
				disabled={creating || selected.length === 0}
				onclick={addSelected}
			>
				<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>Add{selected.length > 0
					? ` ${selected.length}`
					: ''}
			</Button>
		</div>
	</Panel>

	<Panel title="Advanced" icon="icon-[lucide--sliders-horizontal]" class="@4xl:col-span-2">
		{#snippet aside()}
			<Button
				type="button"
				variant="ghost"
				size="sm"
				class="h-8 gap-1.5"
				aria-expanded={advancedOpen}
				aria-controls="{id}-advanced"
				onclick={() => (advancedOpen = !advancedOpen)}
			>
				{advancedOpen ? 'Hide' : 'Show'}
				<span
					class="size-4 {advancedOpen
						? 'icon-[lucide--chevron-up]'
						: 'icon-[lucide--chevron-down]'}"
					aria-hidden="true"
				></span>
			</Button>
		{/snippet}
		{#if advancedOpen}
			<div id="{id}-advanced" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				<div class="min-w-0 space-y-2">
					{@render field(
						'ashift',
						'Ashift',
						'The sector size ZFS writes in. 12 (4 KiB) suits most drives, and it cannot be changed after the pool is created.'
					)}
					<Select.Root type="single" bind:value={settings.ashift} disabled={creating}>
						<Select.Trigger id="{id}-ashift" class="bg-background w-full dark:hover:bg-input/30">
							{ashiftOptions.find((option) => option.value === settings.ashift)?.label}
						</Select.Trigger>
						<Select.Content>
							{#each ashiftOptions as option (option.value)}
								<Select.Item value={option.value} label={option.label} />
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
				{#each booleanPoolSettings as setting (setting.key)}
					{@const locked = setting.key === 'autoreplace' && spares.length === 0}
					<div class="min-w-0 space-y-2">
						{@render field(
							setting.key,
							setting.label,
							locked
								? `${setting.description} Add a spare device to enable Auto Replace.`
								: setting.description
						)}
						<Select.Root
							type="single"
							bind:value={settings[setting.key]}
							disabled={creating || locked}
						>
							<Select.Trigger
								id="{id}-{setting.key}"
								class="bg-background w-full dark:hover:bg-input/30"
							>
								{settings[setting.key] === 'on' ? 'Enabled' : 'Disabled'}
							</Select.Trigger>
							<Select.Content>
								<Select.Item value="on" label="Enabled" />
								<Select.Item value="off" label="Disabled" />
							</Select.Content>
						</Select.Root>
					</div>
				{/each}
				<div class="min-w-0 space-y-2">
					{@render field(
						'failmode',
						'Fail Mode',
						'Control how ZFS handles a catastrophic pool failure. Panic can crash this node if the pool fails.'
					)}
					<Select.Root type="single" bind:value={settings.failmode} disabled={creating}>
						<Select.Trigger id="{id}-failmode" class="bg-background w-full dark:hover:bg-input/30">
							{failModes.find((mode) => mode.value === settings.failmode)?.label}
						</Select.Trigger>
						<Select.Content>
							{#each failModes as mode (mode.value)}
								<Select.Item value={mode.value} label={mode.label} />
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
				<div
					class="flex min-w-0 items-start gap-3 rounded-md border p-3 sm:col-span-2 lg:col-span-3"
				>
					<Checkbox id="{id}-force" class="mt-0.5" bind:checked={force} disabled={creating} />
					<div class="space-y-1">
						<Label for="{id}-force">Force Create</Label>
						<p class="text-muted-foreground text-xs leading-5">
							Skips ZFS’s safety checks, such as devices that appear to hold data, devices of
							different sizes, and Data VDEVs with different layouts. Existing data on the devices
							is destroyed.
						</p>
					</div>
				</div>
			</div>
		{:else}
			<p class="text-muted-foreground text-xs">
				Ashift {settings.ashift} · Fail mode {failModes.find(
					(mode) => mode.value === settings.failmode
				)?.label}{force ? ' · Force Create on' : ''}
			</p>
		{/if}
	</Panel>

	<Panel title="Summary" icon="icon-[lucide--clipboard-list]" class="@4xl:col-span-2">
		<div class="flex flex-wrap items-end gap-x-8 gap-y-4">
			<dl class="flex gap-8 text-sm">
				<div>
					<dt class="text-muted-foreground flex items-center gap-1 text-xs">
						Usable Capacity
						{@render hint(
							'Usable Capacity',
							'Counts Data VDEVs only, before ZFS metadata and reserved space.',
							`${id}-usable-help`
						)}
					</dt>
					<dd class="mt-1 font-medium tabular-nums" aria-describedby="{id}-usable-help">
						{usable > 0 ? `About ${formatBytesBinary(usable)}` : '—'}
					</dd>
				</div>
				<div>
					<dt class="text-muted-foreground text-xs">Devices</dt>
					<dd class="mt-1 font-medium tabular-nums">{assigned.size}</dd>
				</div>
			</dl>
			<div class="ml-auto flex gap-2">
				<Button variant="outline" class="h-9" href={poolHref(hostname)}>Cancel</Button>
				<Button
					type="submit"
					class="h-9 gap-2"
					disabled={creating || !canModify || (attempted && !valid)}
				>
					{#if creating}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{creating ? 'Creating…' : 'Create Pool'}
				</Button>
			</div>
		</div>
		{#if (attempted || assigned.size > 0) && problems.length > 0}
			<ul class="text-destructive mt-3 space-y-1 text-xs leading-5" role="alert">
				{#each problems as problem (problem)}<li>{problem}</li>{/each}
			</ul>
		{/if}
		{#if error}<p class="text-destructive mt-3 text-sm break-words" role="alert">{error}</p>{/if}
	</Panel>
</form>
