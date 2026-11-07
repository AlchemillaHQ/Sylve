<script lang="ts">
	import * as Alert from '$lib/components/ui/alert/index.js';
	import { initializationHostChanges } from '$lib/system-services';
	import type { InitializeHostState } from '$lib/types/basic';
	import type { AvailableService } from '$lib/types/system/settings';

	interface Props {
		services: AvailableService[];
		hostState: InitializeHostState | null;
	}

	let { services, hostState }: Props = $props();

	let changes = $derived(initializationHostChanges(services, hostState));
</script>

{#snippet text(line: string)}
	{#each line.split('`') as part, index (index)}
		{#if index % 2 === 1}
			<code class="bg-muted rounded px-1 text-xs break-all">{part}</code>
		{:else}
			{part}
		{/if}
	{/each}
{/snippet}

<div class="max-h-[55dvh] space-y-3 overflow-y-auto text-sm">
	{#each changes as change (change.label)}
		{#if change.warning}
			<Alert.Root variant="destructive">
				<Alert.Title>
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--alert-circle-outline] h-4 w-4 shrink-0"></span>
						<span>{change.label}</span>
					</div>
				</Alert.Title>
				<Alert.Description>
					<ul class="list-inside list-disc space-y-1">
						{#each change.lines as line (line)}
							<li>{@render text(line)}</li>
						{/each}
					</ul>
					{#if change.link}
						<a
							href={change.link.href}
							target="_blank"
							rel="noopener noreferrer"
							class="mt-1 inline-block underline"
						>
							{change.link.label}
						</a>
					{/if}
				</Alert.Description>
			</Alert.Root>
		{:else}
			<div class="grid grid-cols-[7rem_1fr] gap-2">
				<span class="font-medium">{change.label}</span>
				<ul class="text-muted-foreground space-y-1">
					{#each change.lines as line (line)}
						<li>{@render text(line)}</li>
					{/each}
				</ul>
			</div>
		{/if}
	{/each}

	{#if !hostState}
		<p class="text-muted-foreground">
			The host check could not run, so existing configuration on this host is not shown.
		</p>
	{/if}

	<p class="text-muted-foreground text-xs">
		Services are started with {@render text('`service <name> onestart`')}; their
		{@render text('`rc.conf`')} settings are left alone, apart from {@render text('`pf_enable`')}.
		<a
			href="https://sylve.io/guides/node/settings/system/services/"
			target="_blank"
			rel="noopener noreferrer"
			class="underline"
		>
			Existing configuration and backups
		</a>
	</p>
</div>
