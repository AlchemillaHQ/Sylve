<script lang="ts">
	import { resolve } from '$app/paths';
	import { getSylveRelease, setSylveReleaseChannel } from '$lib/api/system/updates';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SylveIcon from '$lib/components/custom/SylveIcon.svelte';
	import Controls from '$lib/components/custom/System/Updates/Controls.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { hostUpdateReason, type HostUpdateContext } from '$lib/types/system/updates';
	import { isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { watch } from 'runed';
	import { getContext, onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	const updates = getContext<HostUpdateContext>('hostUpdates');
	const view = $derived(updates.view);
	const previewOnly = $derived(
		!!view &&
			view.sylve.mode !== 'standalone' &&
			view.sylve.mode !== 'package' &&
			!!view.sylve.path &&
			view.sylve.path !== '/usr/local/sbin/sylve'
	);
	let channel = $state(untrack(() => view?.releaseChannel ?? 'rolling'));
	let savingChannel = $state(false);
	const controller = new AbortController();
	onDestroy(() => controller.abort());
	let checking = $state(false);
	let checkError = $state('');
	// The page shows the last stored release; GitHub is only asked when the user
	// checks. Approving an update resolves and verifies the release again anyway.
	async function check() {
		if (checking) return;
		const target = updates.node;
		checking = true;
		checkError = '';
		try {
			const result = await getSylveRelease(true, { hostname: target, signal: controller.signal });
			if (controller.signal.aborted || target !== updates.node) return;
			if (isAPIResponse(result)) {
				checkError = result.message || 'release_fetch_failed';
				return;
			}
			await updates.refresh();
		} catch (cause) {
			if (!isRequestCancellation(cause)) throw cause;
		} finally {
			if (target === updates.node) checking = false;
		}
	}
	const releaseError = $derived(checkError || view?.releaseError || '');
	const busy = $derived(
		!updates.enabled ||
			savingChannel ||
			checking ||
			!!releaseError ||
			channel !== view?.releaseChannel
	);
	watch([() => updates.node, () => view?.releaseChannel], () => {
		channel = view?.releaseChannel ?? 'rolling';
		savingChannel = false;
		checkError = '';
	});
	async function saveChannel() {
		if (!view || !updates.enabled || savingChannel || channel === view.releaseChannel) return;
		const target = updates.node;
		savingChannel = true;
		try {
			const result = await setSylveReleaseChannel(channel, {
				hostname: target,
				signal: controller.signal
			});
			if (controller.signal.aborted || target !== updates.node) return;
			if (isAPIResponse(result)) {
				channel = view.releaseChannel;
				toast.error(hostUpdateReason(result.message), { position: 'bottom-center' });
				return;
			}
			await updates.refresh();
			if (controller.signal.aborted || target !== updates.node) return;
			toast.success('Release channel saved', { position: 'bottom-center' });
			void check();
		} catch (cause) {
			if (!isRequestCancellation(cause)) throw cause;
		} finally {
			if (target === updates.node) savingChannel = false;
		}
	}
	const metadata = $derived({
		Version: view?.sylve.version || 'Unknown',
		'Built at': view?.sylve.builtAt
			? convertDbTime(view.sylve.builtAt, 'yyyy-MM-dd hh:mm:ss a')
			: 'Unknown',
		Commit: view?.sylve.commit || 'Unknown',
		Installation:
			view?.sylve.mode === 'package'
				? 'Package-managed'
				: view?.sylve.mode === 'standalone'
					? 'Standalone'
					: view?.sylve.reason === 'sylve_standalone_path_required'
						? 'Manual'
						: 'Unverified',
		Executable: view?.sylve.path || 'Unknown'
	});
</script>

<svelte:head><title>Sylve Updates · {updates.node}</title></svelte:head>

<div class="space-y-4 p-4">
	<Card.Root>
		<Card.Header class="flex flex-row items-center justify-between gap-3">
			<Card.Title
				><span class="flex items-center gap-2"
					><span class="icon-[mdi--update] h-6 w-6" aria-hidden="true"></span>Current Build</span
				></Card.Title
			>
			<div class="flex items-center gap-2">
				<Controls showRefresh={view?.sylve.mode !== 'standalone' || !!updates.error} />
			</div>
		</Card.Header>
		<Card.Content>
			<dl class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
				{#each Object.entries(metadata) as [label, value] (label)}
					<div class="min-w-0 space-y-1">
						<dt class="text-muted-foreground text-xs">{label}</dt>
						<dd
							class="text-sm break-all"
							title={label === 'Built at' ? view?.sylve.builtAt : undefined}
						>
							{value}
						</dd>
					</div>
				{/each}
			</dl>
		</Card.Content>
	</Card.Root>
	{#if view && (view.sylve.mode === 'standalone' || previewOnly)}
		<Card.Root>
			<Card.Header class="flex flex-row flex-wrap items-center justify-between gap-3">
				<Card.Title class="flex items-center gap-2">
					<SylveIcon class="h-6 w-6" /><span>Sylve Updates</span>
				</Card.Title>
				<Button
					size="sm"
					class="ml-auto shrink-0"
					onclick={() => void check()}
					disabled={checking || savingChannel || channel !== view.releaseChannel}
					variant="outline"
					><span
						class="mr-1 h-4 w-4 {checking
							? 'icon-[mdi--loading] animate-spin'
							: 'icon-[mdi--refresh]'}"
						aria-hidden="true"
					></span>{checking ? 'Checking...' : 'Check For Updates'}</Button
				>
			</Card.Header>
			<Card.Content class="space-y-5">
				<div class="max-w-sm space-y-2">
					<SimpleSelect
						label="Release channel"
						bind:value={channel}
						onChange={() => void saveChannel()}
						disabled={!updates.enabled || savingChannel}
						options={[
							{ label: 'Rolling Releases - Recommended', value: 'rolling' },
							{ label: 'Tagged Releases - Not recommended', value: 'tagged' }
						]}
					/>
					{#if !previewOnly}
						<p class="text-muted-foreground text-xs">
							This channel is also used by scheduled monitoring.
						</p>
					{/if}
				</div>
				{#if channel === view.releaseChannel}
					{#if releaseError}
						<div class="space-y-2 text-sm" role="alert">
							<p class="font-medium">
								{releaseError === 'sylve_release_requires_newer_freebsd'
									? 'Update incompatible'
									: 'Check unavailable'}
							</p>
							<p class="text-destructive">{hostUpdateReason(releaseError)}</p>
						</div>
					{:else if (view?.releaseAvailable || previewOnly) && view?.release}
						<div class="space-y-4 border-t pt-4 text-sm">
							<div class="flex flex-wrap items-center justify-between gap-2">
								<p class="font-medium">
									{previewOnly ? 'Published build' : 'Latest eligible build'}
								</p>
								<Badge variant="secondary"
									>{previewOnly ? 'Preview only' : 'Update available'}</Badge
								>
							</div>
							<dl class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
								<div class="space-y-1">
									<dt class="text-muted-foreground text-xs">Version</dt>
									<dd>{view.release.version}</dd>
								</div>
								<div class="space-y-1">
									<dt class="text-muted-foreground text-xs">Built at</dt>
									<dd title={view.release.builtAt}>
										{convertDbTime(view.release.builtAt, 'yyyy-MM-dd hh:mm:ss a')}
									</dd>
								</div>
								<div class="min-w-0 space-y-1">
									<dt class="text-muted-foreground text-xs">Commit</dt>
									<dd class="break-all">{view.release.commit.slice(0, 12)}</dd>
								</div>
								<div class="space-y-1">
									<dt class="text-muted-foreground text-xs">Built for</dt>
									<dd>FreeBSD {view.release.freebsd}</dd>
								</div>
							</dl>
							<Button
								size="sm"
								onclick={() => updates.run('sylve-preview')}
								disabled={busy || previewOnly}>Update &amp; restart</Button
							>
						</div>
					{:else if checking && !view?.releaseCheckedAt}
						<p class="text-muted-foreground text-sm" role="status">Loading latest release...</p>
					{:else if view?.releaseCheckedAt && !previewOnly}
						<p class="text-sm" role="status">
							{view.release?.sha256 === view.sylve.sha256
								? 'Sylve is up to date.'
								: 'No newer eligible release.'}
						</p>
					{:else}
						<p class="text-muted-foreground text-sm" role="status">
							Release information is unavailable.
						</p>
					{/if}
					{#if view?.releaseCheckedAt}<p class="text-muted-foreground text-xs">
							Last checked {convertDbTime(view.releaseCheckedAt)}.
						</p>{/if}
				{:else if savingChannel}
					<p class="text-muted-foreground text-xs" role="status">Saving release channel...</p>
				{/if}
			</Card.Content>
		</Card.Root>
	{:else if view}
		<Card.Root>
			<Card.Content class="space-y-4 text-sm">
				{#if view.sylve.mode === 'package'}
					<p>Update Sylve through Packages. Locked packages remain package-managed.</p>
					<Button
						size="sm"
						variant="outline"
						href={resolve('/[node]/system/updates/packages', { node: updates.node })}
						>View Packages</Button
					>
				{:else if view.sylve.reason}<p class="text-destructive" role="alert">
						{hostUpdateReason(view.sylve.reason)}
					</p>{/if}
			</Card.Content>
		</Card.Root>
	{/if}
</div>
