<script lang="ts">
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { packageKindLabel, safeAdvisoryURL, type JailPackage } from '$lib/types/jail/packages';
	import { formatBytesBinary } from '$lib/utils/bytes';

	let {
		open = $bindable(),
		pkg,
		eligible,
		auditStale,
		onUpgrade
	}: {
		open: boolean;
		pkg: JailPackage;
		eligible: boolean;
		auditStale: boolean;
		onUpgrade: () => Promise<void>;
	} = $props();
	const metadata = $derived({
		'Installed version': pkg.version,
		'Available version': pkg.available || '—',
		Repository: pkg.repository,
		Origin: pkg.origin,
		'Installed size': formatBytesBinary(pkg.size),
		Installation: pkg.automatic ? 'Dependency' : 'Manual',
		Locked: pkg.locked ? 'Yes' : 'No',
		Type: packageKindLabel(pkg.kind)
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl"
		controlsClass="top-5"
		aria-describedby={undefined}
	>
		<Dialog.Header class="border-b px-6 py-5 pr-12 text-left">
			<Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--package-variant]"
					size="h-5 w-5"
					gap="gap-2"
					title={`Package Details — ${pkg.name}`}
					class="[&>span:first-child]:shrink-0 [&>span:last-child]:break-all"
				/></Dialog.Title
			>
		</Dialog.Header>
		<div class="min-h-0 space-y-5 overflow-y-auto px-6 py-5 text-sm">
			<dl class="grid grid-cols-1 gap-x-8 gap-y-4 sm:grid-cols-2">
				{#each Object.entries(metadata) as [key, value] (key)}<div class="min-w-0 space-y-1">
						<dt class="text-muted-foreground text-xs">{key}</dt>
						<dd class="break-words">{value}</dd>
					</div>{/each}
			</dl>
			{#if pkg.kind === 'base' || pkg.kind === 'kernel'}<p class="text-muted-foreground text-xs">
					Use Updates → Preview all upgrades for base and kernel packages.
				</p>
			{:else if pkg.locked}<p class="text-muted-foreground text-xs">
					Locked packages cannot be upgraded here.
				</p>{/if}
			<section class="space-y-3 border-t pt-5">
				<h3 class="font-medium">Vulnerabilities</h3>
				{#if auditStale}<p class="text-muted-foreground text-xs">
						Recorded findings may be out of date. Run an audit to refresh them.
					</p>
				{:else if pkg.advisories.length === 0}<p class="text-muted-foreground">
						No known vulnerabilities found.
					</p>{/if}
				{#if pkg.advisories.length > 0}<ul class="divide-y">
						{#each pkg.advisories as advisory (advisory.id)}
							{@const url = safeAdvisoryURL(advisory.url)}
							<li class="space-y-1.5 py-3 first:pt-0 last:pb-0">
								<p class="leading-relaxed">{advisory.description}</p>
								{#if advisory.cves.length > 0}<p class="text-muted-foreground text-xs">
										{advisory.cves.join(', ')}
									</p>{/if}
								<p class="text-muted-foreground text-xs break-all">
									{#if url}<a
											class="underline underline-offset-2"
											href={url}
											target="_blank"
											rel="noopener noreferrer">{advisory.id}</a
										>{:else}{advisory.id}{/if}
								</p>
							</li>
						{/each}
					</ul>{/if}
			</section>
		</div>
		{#if eligible}<Dialog.Footer class="border-t px-6 py-4"
				><Button
					onclick={() => {
						open = false;
						void onUpgrade();
					}}>Preview Upgrade</Button
				></Dialog.Footer
			>{/if}
	</Dialog.Content>
</Dialog.Root>
