<script lang="ts">
	import { resolve } from '$app/paths';
	import { getSylveRelease, setSylveReleaseChannel } from '$lib/api/system/updates';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SylveIcon from '$lib/components/custom/SylveIcon.svelte';
	import Controls from '$lib/components/custom/System/Updates/Controls.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import {
		hostUpdateReason,
		type HostUpdateContext,
		type SylveRelease
	} from '$lib/types/system/updates';
	import { isAPIResponse, isRequestCancellation, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, watch } from 'runed';
	import { getContext, onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
	const updates = getContext<HostUpdateContext>('hostUpdates');
	const view = $derived(updates.view);
	const cached = $derived(
		data.node === updates.node &&
			data.sylveRelease &&
			data.sylveRelease.releaseChannel === view?.releaseChannel &&
			!view?.releaseError &&
			(!view?.releaseCheckedAt ||
				(data.sylveRelease.releaseCheckedAt &&
					Date.parse(data.sylveRelease.releaseCheckedAt) > Date.parse(view.releaseCheckedAt)))
			? data.sylveRelease
			: view
	);
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
	let releaseRequestError = $state('');
	const releases = resource(
		[
			() => updates.node,
			() => view?.releaseChannel,
			() => view?.sylve.mode === 'standalone' || previewOnly,
			() => view?.sylve.sha256,
			() => data
		],
		async (
			[node, channel, enabled],
			_,
			{ signal, data: previous }
		): Promise<{
			node: string;
			channel: typeof channel;
			view: SylveRelease | null;
			verified: boolean;
		}> => {
			releaseRequestError = '';
			if (!enabled || !channel) return { node, channel, view: null, verified: false };
			const fallback =
				previous?.node === node && previous.channel === channel ? previous.view : cached;
			const requestSignal = AbortSignal.any([signal, controller.signal]);
			const result = await getSylveRelease(true, {
				hostname: node,
				signal: requestSignal
			});
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) {
				releaseRequestError = result.message || 'release_fetch_failed';
				return { node, channel, view: fallback, verified: false };
			}
			if (result.releaseChannel !== channel) {
				await updates.refresh();
				requestSignal.throwIfAborted();
				return { node, channel, view: fallback, verified: false };
			}
			await updateCache(`sylve-release-${channel}`, result, node);
			requestSignal.throwIfAborted();
			return { node, channel, view: result, verified: true };
		},
		{
			initialValue: untrack(() => ({
				node: updates.node,
				channel: view?.releaseChannel,
				view: cached,
				verified: false
			}))
		}
	);
	const published = $derived(
		releases.current.node === updates.node && releases.current.channel === view?.releaseChannel
			? (releases.current.view ?? cached)
			: cached
	);
	const releaseVerified = $derived(
		releases.current.node === updates.node &&
			releases.current.channel === view?.releaseChannel &&
			releases.current.view?.sylve.sha256 === view?.sylve.sha256 &&
			releases.current.verified
	);
	const releaseError = $derived(releaseRequestError || published?.releaseError || '');
	const checking = $derived(releases.loading);
	const busy = $derived(
		!updates.enabled ||
			!releaseVerified ||
			savingChannel ||
			checking ||
			!!releaseError ||
			channel !== view?.releaseChannel
	);
	watch([() => updates.node, () => view?.releaseChannel], () => {
		channel = view?.releaseChannel ?? 'rolling';
		savingChannel = false;
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
					onclick={() => releases.refetch()}
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
					{:else if (published?.releaseAvailable || previewOnly) && published?.release}
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
									<dd>{published.release.version}</dd>
								</div>
								<div class="space-y-1">
									<dt class="text-muted-foreground text-xs">Built at</dt>
									<dd title={published.release.builtAt}>
										{convertDbTime(published.release.builtAt, 'yyyy-MM-dd hh:mm:ss a')}
									</dd>
								</div>
								<div class="min-w-0 space-y-1">
									<dt class="text-muted-foreground text-xs">Commit</dt>
									<dd class="break-all">{published.release.commit.slice(0, 12)}</dd>
								</div>
								<div class="space-y-1">
									<dt class="text-muted-foreground text-xs">Built for</dt>
									<dd>FreeBSD {published.release.freebsd}</dd>
								</div>
							</dl>
							<Button
								size="sm"
								onclick={() => updates.run('sylve-preview')}
								disabled={busy || previewOnly}>Update &amp; restart</Button
							>
						</div>
					{:else if checking && !published?.releaseCheckedAt}
						<p class="text-muted-foreground text-sm" role="status">Loading latest release...</p>
					{:else if published?.releaseCheckedAt && !previewOnly}
						<p class="text-sm" role="status">
							{published.release?.sha256 === view.sylve.sha256
								? 'Sylve is up to date.'
								: 'No newer eligible release.'}
						</p>
					{:else}
						<p class="text-muted-foreground text-sm" role="status">
							Release information is unavailable.
						</p>
					{/if}
					{#if published?.releaseCheckedAt}<p class="text-muted-foreground text-xs">
							Last checked {convertDbTime(published.releaseCheckedAt)}.
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
