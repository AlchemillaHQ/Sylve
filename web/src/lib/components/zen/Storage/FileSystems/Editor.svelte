<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { createFileSystem, editFileSystem } from '$lib/api/zfs/datasets';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Command from '$lib/components/ui/command/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { APIRequestError, handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { generateZFSEncryptionKey } from '$lib/utils/zfs';
	import { watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { isEncrypted, isKeyUnavailable, isPoolRoot, type FileSystemRow } from './filesystems';
	import {
		INHERIT,
		advancedTunables,
		compressionTunable,
		createProperties,
		editDraft,
		editProperties,
		encryptionChoices,
		mountpointProblem,
		nameProblem,
		needsPassphrase,
		newDraft,
		parentProblem,
		passphraseProblem,
		quotaProblem,
		hasEditChanges,
		readOnlyTunable,
		saveErrorMessage,
		tunableChoices,
		type Choice,
		type Draft
	} from './form';

	let {
		hostname,
		row,
		parent: initialParent = '',
		parents,
		names,
		onClose,
		onSaved
	}: {
		hostname: string;
		row?: FileSystemRow;
		parent?: string;
		parents: FileSystemRow[];
		names: Set<string>;
		onClose: () => void;
		onSaved: (created?: string) => Promise<unknown> | void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const target = untrack(() => row?.dataset);
	const mode = target ? 'edit' : 'new';
	const initial: Draft = untrack(() =>
		target
			? editDraft(target)
			: newDraft(parents.find((entry) => entry.dataset.name === initialParent)?.dataset)
	);
	let draft = $state<Draft>({ ...initial });
	let touched = $state<Record<string, boolean>>({});
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');
	let advanced = $state(false);
	let parentOpen = $state(false);
	let revealed = $state(false);

	let parent = $derived(parents.find((entry) => entry.dataset.name === draft.parent)?.dataset);
	let source = $derived(target ?? parent);
	let editParent = $derived(
		target ? parents.find((entry) => entry.dataset.name === initial.parent)?.dataset : undefined
	);
	let problems = $derived<Record<string, string>>({
		parent: mode === 'new' ? parentProblem(parent) : '',
		name: mode === 'new' ? nameProblem(draft.name, draft.parent, names) : '',
		quota: quotaProblem(
			draft.quota,
			mode === 'new' ? { max: parent?.available } : { min: target?.used }
		),
		mountpoint: mountpointProblem(draft.mountpoint, mode === 'edit'),
		passphrase: mode === 'new' ? passphraseProblem(draft) : ''
	});
	let valid = $derived(Object.values(problems).every((problem) => !problem));
	let changes = $derived(editProperties(initial, draft));
	let dirty = $derived(mode === 'new' || hasEditChanges(changes));
	let advancedChanged = $derived(
		advancedTunables.filter((tunable) => draft[tunable.key] !== initial[tunable.key]).length +
			Number(mode === 'edit' && draft.readonly !== initial.readonly)
	);
	let mountPlaceholder = $derived.by(() => {
		if (mode === 'edit') return '/path/to/mount';
		const base = parent?.mountpoint.trim() ?? '';
		const name = draft.name.trim() || 'name';
		return base.startsWith('/') ? `${base.replace(/\/$/, '')}/${name}` : 'Inherited from parent';
	});

	watch(
		() => draft.parent,
		() => {
			const encrypted = parent ? isEncrypted(parent) : false;
			if (draft.encryption === INHERIT && !encrypted) draft.encryption = 'off';
			if (draft.encryption === 'off' && encrypted) draft.encryption = INHERIT;
		},
		{ lazy: true }
	);

	onDestroy(() => lifetime.abort());

	function problem(key: keyof Draft): string {
		if (attempted) return problems[key] ?? '';
		return touched[key] && draft[key].trim() ? (problems[key] ?? '') : '';
	}

	function reset() {
		if (saving) return;
		draft = { ...initial };
		touched = {};
		attempted = false;
		saveError = '';
	}

	function generate() {
		draft.passphrase = generateZFSEncryptionKey();
		revealed = true;
		touched.passphrase = true;
	}

	async function copy() {
		try {
			await navigator.clipboard.writeText(draft.passphrase);
			toast.success('Passphrase copied', { position: 'bottom-center' });
		} catch {
			toast.error('The passphrase could not be copied', { position: 'bottom-center' });
		}
	}

	async function save(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		if (saving || !valid || !dirty) return;
		saving = true;
		saveError = '';
		const options = { hostname, signal: lifetime.signal };
		try {
			const response = target
				? await editFileSystem(target.guid, changes.properties, options, changes.inherit)
				: await createFileSystem(draft.name.trim(), draft.parent, createProperties(draft), options);
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			toast.success(
				target
					? `File system ${target.name} updated`
					: `File system ${draft.parent}/${draft.name.trim()} created`,
				{ position: 'bottom-center' }
			);
			void onSaved(target ? undefined : `${draft.parent}/${draft.name.trim()}`);
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			saveError = saveErrorMessage(
				error instanceof Error ? error.message : 'The file system could not be saved.'
			);
			if (target) void onSaved();
		} finally {
			if (!lifetime.signal.aborted) saving = false;
		}
	}
</script>

{#snippet hint(label: string, description: string)}
	{@const helpId = `${id}-${label.toLowerCase().replaceAll(' ', '-')}-help`}
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

{#snippet label(key: keyof Draft, text: string, description?: string)}
	{@const error = problem(key)}
	<div class="flex min-h-5 items-center justify-between gap-2">
		<div class="flex min-w-0 flex-wrap items-baseline gap-x-1">
			<Label for="{id}-{key}">{text}</Label>
			{#if error}
				<span id="{id}-{key}-error" class="text-destructive text-xs">({error})</span>
			{/if}
		</div>
		{#if description}{@render hint(text, description)}{/if}
	</div>
{/snippet}

{#snippet select(key: keyof Draft, text: string, description: string, choices: Choice[])}
	<div class="min-w-0 space-y-2">
		{@render label(key, text, description)}
		<Select.Root type="single" bind:value={draft[key]} disabled={saving}>
			<Select.Trigger id="{id}-{key}" class="bg-background w-full dark:hover:bg-input/30">
				<span class="truncate">
					{choices.find((choice) => choice.value === draft[key])?.label ?? draft[key]}
				</span>
			</Select.Trigger>
			<Select.Content class="z-60">
				{#each choices as choice (choice.value)}
					<Select.Item value={choice.value} label={choice.label} />
				{/each}
			</Select.Content>
		</Select.Root>
	</div>
{/snippet}

{#snippet input(
	key: 'name' | 'quota' | 'mountpoint',
	text: string,
	placeholder: string,
	description?: string
)}
	{@const error = problem(key)}
	<div class="min-w-0 space-y-2 {key === 'mountpoint' ? 'sm:col-span-2' : ''}">
		{@render label(key, text, description)}
		<Input
			id="{id}-{key}"
			bind:value={draft[key]}
			{placeholder}
			autocomplete="off"
			spellcheck={false}
			disabled={saving}
			class={key === 'mountpoint' ? 'font-mono text-sm' : undefined}
			onblur={() => (touched[key] = true)}
			aria-invalid={error ? true : undefined}
			aria-describedby={error ? `${id}-${key}-error` : undefined}
		/>
	</div>
{/snippet}

{#snippet parentPicker()}
	{@const error = problem('parent')}
	<div class="min-w-0 space-y-2 sm:col-span-2">
		{@render label('parent', 'Parent')}
		<Popover.Root
			bind:open={parentOpen}
			onOpenChange={(open) => {
				if (!open) touched.parent = true;
			}}
		>
			<Popover.Trigger disabled={saving}>
				{#snippet child({ props })}
					<Button
						{...props}
						id="{id}-parent"
						variant="outline"
						role="combobox"
						aria-expanded={parentOpen}
						aria-invalid={error ? true : undefined}
						aria-describedby={error ? `${id}-parent-error` : undefined}
						class="bg-background w-full justify-between px-3 font-normal dark:hover:bg-input/30"
					>
						{#if parent}
							<span class="flex min-w-0 items-center gap-2">
								<span
									class="{isPoolRoot(parent)
										? 'icon-[bi--hdd-stack-fill]'
										: 'icon-[eos-icons--file-system]'} text-muted-foreground size-4 shrink-0"
									aria-hidden="true"
								></span>
								<span class="truncate">{parent.name}</span>
							</span>
						{:else}
							<span class="text-muted-foreground">Choose a parent file system</span>
						{/if}
						<span
							class="icon-[lucide--chevrons-up-down] size-4 shrink-0 opacity-50"
							aria-hidden="true"
						></span>
					</Button>
				{/snippet}
			</Popover.Trigger>
			<Popover.Content
				class="z-60 w-(--bits-popover-anchor-width) min-w-64 p-0"
				align="start"
				sideOffset={4}
			>
				<Command.Root>
					<Command.Input placeholder="Search file systems…" />
					<Command.List class="max-h-64">
						<Command.Empty>No matching file systems.</Command.Empty>
						{#each parents as entry (entry.dataset.name)}
							{@const locked = isKeyUnavailable(entry.dataset)}
							<Command.Item
								value={entry.dataset.name}
								disabled={locked}
								onSelect={() => {
									draft.parent = entry.dataset.name;
									touched.parent = true;
									parentOpen = false;
								}}
							>
								<span
									class="{isPoolRoot(entry.dataset)
										? 'icon-[bi--hdd-stack-fill]'
										: 'icon-[eos-icons--file-system]'} text-muted-foreground size-4 shrink-0"
									aria-hidden="true"
								></span>
								<span class="min-w-0 truncate">{entry.dataset.name}</span>
								{#if locked}
									<span class="text-muted-foreground ml-auto shrink-0 text-xs">Key Not Loaded</span>
								{:else if draft.parent === entry.dataset.name}
									<span class="icon-[lucide--check] ml-auto size-4 shrink-0" aria-hidden="true"
									></span>
								{/if}
							</Command.Item>
						{/each}
					</Command.List>
				</Command.Root>
			</Popover.Content>
		</Popover.Root>
	</div>
{/snippet}

{#snippet passphraseField()}
	{@const error = problem('passphrase')}
	<div class="min-w-0 space-y-2 sm:col-span-2">
		{@render label(
			'passphrase',
			'Passphrase',
			'At least 32 characters. Sylve stores it on this node to unlock the file system, but keep a copy somewhere safe too.'
		)}
		<div class="flex gap-2">
			<Input
				id="{id}-passphrase"
				type={revealed ? 'text' : 'password'}
				bind:value={draft.passphrase}
				placeholder="Enter or generate a passphrase"
				autocomplete="new-password"
				spellcheck={false}
				disabled={saving}
				class="min-w-0 flex-1 font-mono text-sm"
				onblur={() => (touched.passphrase = true)}
				aria-invalid={error ? true : undefined}
				aria-describedby={error ? `${id}-passphrase-error` : undefined}
			/>
			<Button
				type="button"
				variant="outline"
				size="icon"
				class="size-9 shrink-0"
				disabled={saving}
				onclick={() => (revealed = !revealed)}
				aria-label={revealed ? 'Hide passphrase' : 'Show passphrase'}
				aria-pressed={revealed}
			>
				<span
					class="{revealed ? 'icon-[lucide--eye-off]' : 'icon-[lucide--eye]'} size-4"
					aria-hidden="true"
				></span>
			</Button>
			<Button
				type="button"
				variant="outline"
				size="icon"
				class="size-9 shrink-0"
				disabled={saving}
				onclick={generate}
				aria-label="Generate passphrase"
				title="Generate passphrase"
			>
				<span class="icon-[lucide--dices] size-4" aria-hidden="true"></span>
			</Button>
			<Button
				type="button"
				variant="outline"
				size="icon"
				class="size-9 shrink-0"
				disabled={saving || !draft.passphrase}
				onclick={copy}
				aria-label="Copy passphrase"
				title="Copy passphrase"
			>
				<span class="icon-[lucide--copy] size-4" aria-hidden="true"></span>
			</Button>
		</div>
	</div>
{/snippet}

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !saving) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		interactOutsideBehavior={saving ? 'ignore' : 'close'}
		aria-busy={saving}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={save} novalidate>
			<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="{target
								? 'icon-[lucide--pencil]'
								: 'icon-[lucide--plus]'} text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 break-all">
							{target ? `Edit File System — ${target.name}` : 'New File System'}
						</span>
					</Dialog.Title>
					<Dialog.Description class="sr-only">
						{target
							? `Change the settings of ${target.name}.`
							: 'Create a ZFS file system under an existing one.'}
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
			<ScrollArea
				class="flex min-h-0 flex-auto flex-col"
				fade
				scrollbarYClasses="hidden"
				viewportProps={{ class: 'h-auto min-h-0 w-full flex-auto overscroll-contain' }}
			>
				<div class="space-y-5 p-4">
					<div class="grid gap-4 sm:grid-cols-2">
						{#if mode === 'new'}
							{@render parentPicker()}
							{@render input('name', 'Name', 'data')}
						{/if}
						{@render input(
							'quota',
							'Quota',
							'No limit',
							'Limits how much space this file system and everything inside it can use. Use a size like 512M or 10G, or leave it empty for no limit.'
						)}
						{#if mode === 'edit'}
							{@render select(
								'compression',
								'Compression',
								compressionTunable.description,
								tunableChoices(compressionTunable, 'edit', target, editParent)
							)}
						{/if}
						{@render input(
							'mountpoint',
							'Mount Point',
							mountPlaceholder,
							mode === 'new'
								? 'Where the file system appears on this node. Leave it empty to place it under the parent’s mount point. Use none to keep it unmounted, or legacy to mount it outside ZFS.'
								: 'Where the file system appears on this node. Use none to keep it unmounted, or legacy to mount it outside ZFS.'
						)}
						{#if mode === 'new'}
							{@render select(
								'compression',
								'Compression',
								compressionTunable.description,
								tunableChoices(compressionTunable, 'new', parent)
							)}
							{@render select(
								'encryption',
								'Encryption',
								'Encrypts this file system. It can’t be turned on or off later. Under an encrypted parent it shares the parent’s key unless you pick an algorithm to give it its own passphrase.',
								encryptionChoices(parent)
							)}
							{#if needsPassphrase(draft)}
								{@render passphraseField()}
							{/if}
						{/if}
					</div>
					<section class="space-y-4">
						<button
							type="button"
							class="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex w-full cursor-pointer items-center gap-2 rounded-sm text-sm font-medium focus-visible:ring-2 focus-visible:outline-none"
							aria-expanded={advanced}
							aria-controls="{id}-advanced"
							onclick={() => (advanced = !advanced)}
						>
							<span
								class="{advanced
									? 'icon-[lucide--chevron-down]'
									: 'icon-[lucide--chevron-right]'} size-4 shrink-0"
								aria-hidden="true"
							></span>
							Advanced Settings
							{#if !advanced && advancedChanged > 0}
								<span class="text-xs font-normal">· {advancedChanged} changed</span>
							{/if}
							<span class="bg-border ml-2 h-px flex-1" aria-hidden="true"></span>
						</button>
						{#if advanced}
							<div id="{id}-advanced" class="grid gap-4 sm:grid-cols-2">
								{#each advancedTunables as tunable (tunable.key)}
									{@render select(
										tunable.key,
										tunable.label,
										tunable.description,
										tunableChoices(tunable, mode, source, editParent)
									)}
								{/each}
								{#if mode === 'edit'}
									{@render select(
										'readonly',
										'Read Only',
										'Prevents any changes to the files in this file system.',
										tunableChoices(readOnlyTunable, 'edit', target, editParent)
									)}
								{/if}
							</div>
						{/if}
					</section>
					{#if saveError}
						<p class="text-destructive text-sm break-words" role="alert">{saveError}</p>
					{/if}
				</div>
			</ScrollArea>
			<div class="flex shrink-0 items-center justify-between gap-3 border-t p-4">
				<Button
					type="button"
					variant="outline"
					class="h-9"
					disabled={saving || (mode === 'edit' && !dirty)}
					onclick={reset}
				>
					Reset
				</Button>
				<Button
					type="submit"
					class="h-9 gap-2"
					disabled={saving || (mode === 'edit' && !dirty) || (attempted && !valid)}
				>
					{#if saving}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{#if target}
						{saving ? 'Saving…' : 'Save Changes'}
					{:else}
						{saving ? 'Creating…' : 'Create File System'}
					{/if}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
