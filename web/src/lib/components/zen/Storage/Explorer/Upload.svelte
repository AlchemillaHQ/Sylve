<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { addFileOrFolder, revertFileExplorerUpload } from '$lib/api/system/file-explorer';
	import FilePond from '$lib/components/custom/FilePond.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { isDemoMode } from '$lib/demo/runtime';
	import {
		getFilePondRequestHeaders,
		getFilePondRequestURL,
		parseFilePondUploadID
	} from '$lib/utils/filepond';
	import type {
		FilePondErrorDescription,
		FilePondFile,
		ProcessServerConfigFunction
	} from 'filepond';
	import { untrack } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { explorerErrorMessage, uploadErrorMessage } from './explorer';

	let {
		hostname,
		directory,
		files = [],
		onClose,
		onUploaded
	}: {
		hostname: string;
		directory: string;
		files?: File[];
		onClose: () => void;
		onUploaded: () => void;
	} = $props();

	const idleLabel = 'Drop files here or <span class="filepond--label-action">browse</span>';
	const target = untrack(() => directory);
	const initial = untrack(() => files);
	const active = new SvelteSet<string>();
	let uploaded = $state(0);
	let busy = $derived(active.size > 0);
	let ready = $derived(isDemoMode || Boolean(storage.token?.trim()));

	function revert(uniqueFileID: unknown, load: () => void, error: (message: string) => void) {
		const uploadId = typeof uniqueFileID === 'string' ? uniqueFileID.trim() : '';
		if (!uploadId) {
			load();
			return;
		}
		void revertFileExplorerUpload(uploadId, hostname).then((result) => {
			if (result.status !== 'success') {
				error(explorerErrorMessage(result, 'The upload could not be removed'));
				return;
			}
			uploaded = Math.max(0, uploaded - 1);
			load();
			onUploaded();
		});
	}

	const processDemoUpload: ProcessServerConfigFunction = (
		_fieldName,
		file,
		_metadata,
		load,
		error,
		progress,
		abort
	) => {
		let cancelled = false;
		void addFileOrFolder(target, file.name, false, hostname, file.size)
			.then((result) => {
				if (cancelled) return;
				if (result.status !== 'success') {
					error(explorerErrorMessage(result, 'The upload failed'));
					return;
				}
				progress(true, file.size, file.size);
				load(`demo-upload-${Date.now().toString(36)}`);
			})
			.catch(() => {
				if (!cancelled) error('The upload failed');
			});
		return {
			abort: () => {
				cancelled = true;
				abort();
			}
		};
	};

	let server = $derived({
		process: isDemoMode
			? processDemoUpload
			: {
					url: getFilePondRequestURL(
						`/api/system/file-explorer/upload?path=${encodeURIComponent(target)}`,
						hostname
					),
					method: 'POST' as const,
					headers: getFilePondRequestHeaders(),
					onload: (response: string) => parseFilePondUploadID(response),
					onerror: (response: string) => uploadErrorMessage(response)
				},
		revert
	});
</script>

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !busy) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-lg"
		showCloseButton={false}
		escapeKeydownBehavior={busy ? 'ignore' : 'close'}
		interactOutsideBehavior={busy ? 'ignore' : 'close'}
		aria-busy={busy}
	>
		<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="icon-[lucide--upload] text-zen-highlight-foreground size-5 shrink-0"
						aria-hidden="true"
					></span>
					Upload Files
				</Dialog.Title>
				<Dialog.Description class="text-muted-foreground font-mono text-xs break-all">
					{target}
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Close disabled={busy}>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0" disabled={busy}>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</Dialog.Close>
		</div>
		<div class="min-h-0 space-y-3 overflow-y-auto p-4">
			{#if ready}
				<FilePond
					name="filepond"
					{server}
					files={initial}
					instantUpload
					allowMultiple
					maxParallelUploads={2}
					credits={false}
					labelIdle={idleLabel}
					onprocessfilestart={(file: FilePondFile) => active.add(file.id)}
					onprocessfile={(error: FilePondErrorDescription | null, file: FilePondFile) => {
						active.delete(file.id);
						if (error) return;
						uploaded += 1;
						onUploaded();
					}}
					onprocessfileabort={(file: FilePondFile) => active.delete(file.id)}
					onremovefile={(_error: FilePondErrorDescription | null, file: FilePondFile) =>
						active.delete(file.id)}
				/>
			{:else}
				<p class="text-destructive text-sm" role="alert">
					Uploading needs a fresh sign-in. Sign in again, then try again.
				</p>
			{/if}
			{#if busy}
				<p class="text-muted-foreground text-sm" role="status">
					Uploading {active.size}
					{active.size === 1 ? 'file' : 'files'}. Closing becomes available when they finish or are
					cancelled.
				</p>
			{/if}
		</div>
		<div class="flex shrink-0 items-center justify-between gap-3 border-t p-4">
			<p class="text-muted-foreground text-sm" aria-live="polite">
				{uploaded === 0 ? 'Nothing uploaded yet' : `${uploaded} uploaded`}
			</p>
			<Button class="h-9" disabled={busy} onclick={onClose}>Done</Button>
		</div>
	</Dialog.Content>
</Dialog.Root>
