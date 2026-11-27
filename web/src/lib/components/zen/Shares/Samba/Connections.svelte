<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { SambaStatus } from '$lib/types/samba/shares';
	import { dateToAgo } from '$lib/utils/time';
	import { plural } from './samba';

	let { status, onClose }: { status: SambaStatus; onClose: () => void } = $props();

	type Protection = { label: string; icon: string; detail: string; tone: 'good' | 'muted' };
	type Client = {
		key: string;
		name: string;
		guest: boolean;
		address: string;
		protocol: string;
		protection?: Protection;
		connections: SambaStatus['connections'];
	};

	let clients = $derived.by(() => {
		const result: Client[] = status.sessions.map((session) => ({
			key: session.id,
			name: session.username === 'nobody' ? 'Guest' : session.username || 'Unknown User',
			guest: session.username === 'nobody',
			address: session.remoteMachine || session.hostname,
			protocol: protocol(session.dialect),
			protection: protection(session.encryption, session.signing),
			connections: status.connections.filter((connection) => connection.sessionId === session.id)
		}));
		const known = status.sessions.map((session) => session.id);
		const orphans = status.connections.filter(
			(connection) => !known.includes(connection.sessionId)
		);
		if (orphans.length) {
			result.push({
				key: 'orphans',
				name: 'Other Connections',
				guest: false,
				address: '',
				protocol: '',
				connections: orphans
			});
		}
		return result;
	});

	function protocol(dialect: string): string {
		const match = /^SMB(\d)_(\d)(\d)$/.exec(dialect.trim());
		if (!match) return dialect === 'NT1' ? 'SMB 1' : dialect;
		const [, major, minor, patch] = match;
		return patch === '0' ? `SMB ${major}.${minor}` : `SMB ${major}.${minor}.${patch}`;
	}

	function active(value: string): boolean {
		return Boolean(value) && value !== 'none' && !value.endsWith(' none');
	}

	function protection(encryption: string, signing: string): Protection {
		if (active(encryption)) {
			return { label: 'Encrypted', icon: 'icon-[lucide--lock]', detail: encryption, tone: 'good' };
		}
		if (active(signing)) {
			return {
				label: 'Signed',
				icon: 'icon-[lucide--shield-check]',
				detail: signing,
				tone: 'good'
			};
		}
		return {
			label: 'Not Encrypted',
			icon: 'icon-[lucide--lock-open]',
			detail: 'Traffic is neither encrypted nor signed',
			tone: 'muted'
		};
	}

	function since(value: string): string {
		const parsed = Date.parse(value);
		return Number.isFinite(parsed) ? dateToAgo(new Date(parsed)) : '';
	}
</script>

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-xl"
		showCloseButton={false}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 gap-1 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="icon-[mdi--lan-connect] text-zen-highlight-foreground size-5 shrink-0"
						aria-hidden="true"
					></span>
					Connected Clients
				</Dialog.Title>
				<Dialog.Description class="text-muted-foreground text-sm">
					{plural(status.sessions.length, 'client')} · {plural(status.openFiles, 'open file')}
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Close>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0">
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
			<div class="p-4">
				{#if clients.length === 0}
					<div
						class="text-muted-foreground flex flex-col items-center gap-2 py-10 text-center text-sm"
					>
						<span class="icon-[mdi--lan-disconnect] size-8" aria-hidden="true"></span>
						No clients are connected.
					</div>
				{:else}
					<ul class="space-y-3">
						{#each clients as client (client.key)}
							<li class="bg-card overflow-hidden rounded-lg border">
								<div class="flex items-center gap-3 p-3">
									<span
										class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-full"
										aria-hidden="true"
									>
										<span
											class="{client.guest ? 'icon-[lucide--globe]' : 'icon-[lucide--user]'} size-4"
										></span>
									</span>
									<div class="min-w-0 flex-1">
										<p class="truncate text-sm font-medium">{client.name}</p>
										{#if client.address || client.protocol}
											<p class="text-muted-foreground truncate text-xs">
												{[client.address, client.protocol].filter(Boolean).join(' · ')}
											</p>
										{/if}
									</div>
									{#if client.protection}
										<span
											class="flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-xs {client
												.protection.tone === 'good'
												? 'border-emerald-500/30 text-emerald-700 dark:text-emerald-400'
												: 'text-muted-foreground'}"
											title={client.protection.detail}
										>
											<span class="{client.protection.icon} size-3.5" aria-hidden="true"></span>
											{client.protection.label}
										</span>
									{/if}
								</div>
								{#if client.connections.length}
									<ul class="divide-y border-t">
										{#each client.connections as connection (`${connection.sessionId}:${connection.share}:${connection.pid}`)}
											<li class="flex min-w-0 items-center gap-2 px-3 py-2 text-sm">
												<span
													class="icon-[mdi--folder-network] text-muted-foreground size-4 shrink-0"
													aria-hidden="true"
												></span>
												<span class="min-w-0 flex-1 truncate">{connection.share}</span>
												{#if since(connection.connectedAt)}
													<span
														class="text-muted-foreground shrink-0 text-xs"
														title={new Date(connection.connectedAt).toLocaleString()}
													>
														{since(connection.connectedAt)}
													</span>
												{/if}
											</li>
										{/each}
									</ul>
								{:else}
									<p class="text-muted-foreground border-t px-3 py-2 text-xs">No shares open</p>
								{/if}
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		</ScrollArea>
	</Dialog.Content>
</Dialog.Root>
