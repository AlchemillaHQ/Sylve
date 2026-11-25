<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { addFileOrFolder, renameFileOrFolder } from '$lib/api/system/file-explorer';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { handleAPIError } from '$lib/utils/http';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { childPath, entryName, explorerErrorMessage, isFolder, nameProblem } from './explorer';

	let {
		hostname,
		mode,
		directory,
		entry,
		taken,
		onClose,
		onDone
	}: {
		hostname: string;
		mode: 'folder' | 'file' | 'rename';
		directory: string;
		entry?: FileNode;
		taken: Set<string>;
		onClose: () => void;
		onDone: (name: string) => void;
	} = $props();

	const id = $props.id();
	const target = untrack(() => entry);
	const kind = untrack(() => mode);
	const current = target ? entryName(target.id) : '';
	const folder = kind === 'folder' || (target ? isFolder(target) : false);
	const title =
		kind === 'rename' ? `Rename ${folder ? 'Folder' : 'File'}` : folder ? 'New Folder' : 'New File';
	let name = $state(current);
	let touched = $state(false);
	let attempted = $state(false);
	let saving = $state(false);
	let saveError = $state('');
	let input = $state<HTMLInputElement | null>(null);
	let problem = $derived(nameProblem(name, taken, current || undefined));
	let error = $derived(attempted || (touched && name.trim()) ? problem : '');
	let unchanged = $derived(kind === 'rename' && name.trim() === current);

	function focusName(event: Event) {
		event.preventDefault();
		if (!input) return;
		input.focus();
		const dot = current.lastIndexOf('.');
		input.setSelectionRange(0, !folder && dot > 0 ? dot : current.length);
	}

	async function save(event: SubmitEvent) {
		event.preventDefault();
		attempted = true;
		if (saving || problem || unchanged) return;
		saving = true;
		saveError = '';
		const value = name.trim();
		try {
			const response = target
				? await renameFileOrFolder(target.id, value, hostname)
				: await addFileOrFolder(directory, value, kind === 'folder', hostname);
			if (response.status !== 'success') {
				handleAPIError(response);
				saveError = explorerErrorMessage(
					response,
					target ? 'It could not be renamed.' : `The ${kind} could not be created.`
				);
				return;
			}
			toast.success(
				target
					? `Renamed to ${value}`
					: `${kind === 'folder' ? 'Folder' : 'File'} ${childPath(directory, value)} created`,
				{ position: 'bottom-center' }
			);
			onDone(value);
			onClose();
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !saving) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-md"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		interactOutsideBehavior={saving ? 'ignore' : 'close'}
		onOpenAutoFocus={focusName}
		aria-busy={saving}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={save} novalidate>
			<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 text-left">
					<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="{kind === 'rename'
								? 'icon-[lucide--text-cursor-input]'
								: kind === 'folder'
									? 'icon-[lucide--folder-plus]'
									: 'icon-[lucide--file-plus]'} text-zen-highlight-foreground size-5 shrink-0"
							aria-hidden="true"
						></span>
						<span class="min-w-0 break-all">{title}</span>
					</Dialog.Title>
					<Dialog.Description class="text-muted-foreground text-xs break-all">
						{target ? target.id : directory}
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
			<div class="space-y-4 p-4">
				<div class="space-y-2">
					<div class="flex min-h-5 flex-wrap items-baseline gap-x-1">
						<Label for="{id}-name">Name</Label>
						{#if error}
							<span id="{id}-name-error" class="text-destructive text-xs">({error})</span>
						{/if}
					</div>
					<Input
						id="{id}-name"
						bind:ref={input}
						bind:value={name}
						placeholder={kind === 'folder' ? 'New folder' : kind === 'file' ? 'notes.txt' : current}
						autocomplete="off"
						spellcheck={false}
						disabled={saving}
						onblur={() => (touched = true)}
						aria-invalid={error ? true : undefined}
						aria-describedby={error ? `${id}-name-error` : undefined}
					/>
				</div>
				{#if saveError}
					<p class="text-destructive text-sm break-words" role="alert">{saveError}</p>
				{/if}
			</div>
			<div class="flex shrink-0 items-center justify-end gap-3 border-t p-4">
				<Button
					type="submit"
					class="h-9 gap-2"
					disabled={saving || unchanged || (attempted && Boolean(problem))}
				>
					{#if saving}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{#if kind === 'rename'}
						{saving ? 'Renaming…' : 'Rename'}
					{:else}
						{saving ? 'Creating…' : 'Create'}
					{/if}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
