<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import type { WireGuardServerPeer } from '$lib/types/network/wireguard';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { convertDbTime } from '$lib/utils/time';

	interface Props {
		peers: WireGuardServerPeer[];
		onEdit: (peer: WireGuardServerPeer) => void;
		onToggle: (id: number) => void;
		onExport: (id: number) => void;
		onDelete: (id: number) => void;
	}

	let { peers, onEdit, onToggle, onExport, onDelete }: Props = $props();

	function formatHandshake(time: string): string {
		if (!time || time.startsWith('0001')) return 'Never';
		return convertDbTime(time);
	}

	function isStale(time: string): boolean {
		if (!time || time.startsWith('0001')) return true;
		return Date.now() - new Date(time).getTime() > 5 * 60 * 1000;
	}

	function dotColor(peer: WireGuardServerPeer): string {
		if (!peer.enabled) return 'bg-red-500';
		if (isStale(peer.lastHandshake)) return 'bg-yellow-500';
		return 'bg-green-500';
	}
</script>

{#if peers.length === 0}
	<p class="text-sm text-muted-foreground">No peers configured yet.</p>
{:else}
	<div class="overflow-x-auto rounded-md border">
		<table class="w-full text-sm">
			<thead class="bg-muted/30">
				<tr>
					<th
						class="w-px whitespace-nowrap px-3 py-2 text-left text-xs font-medium text-muted-foreground"
						>Name</th
					>
					<th class="px-3 py-2 text-left text-xs font-medium text-muted-foreground">Client IPs</th>
					<th class="px-3 py-2 text-left text-xs font-medium text-muted-foreground">Routable IPs</th
					>
					<th
						class="w-px whitespace-nowrap px-3 py-2 text-left text-xs font-medium text-muted-foreground"
					></th>
				</tr>
			</thead>
			<tbody>
				{#each peers as peer (peer.id)}
					<tr class="border-t hover:bg-muted/20 transition-colors [&>td]:align-top">
						<td class="px-3 py-2">
							<div class="flex flex-col gap-0.5 whitespace-nowrap">
								<div class="flex items-center gap-1.5">
									<span
										class="size-1.5 shrink-0 rounded-full {dotColor(peer)}"
										title={peer.enabled
											? isStale(peer.lastHandshake)
												? 'No recent handshake'
												: 'Connected'
											: 'Disabled'}
									></span>
									<span class="font-medium">{peer.name}</span>
								</div>
								<div class="flex flex-col gap-0.5 text-[10px] text-muted-foreground">
									<span class="flex items-center gap-0.5 tabular-nums">
										<span class="icon icon-[mdi--arrow-down-thin] size-3"></span>
										{formatBytesBinary(peer.rx)}
										<span class="icon icon-[mdi--arrow-up-thin] size-3"></span>
										{formatBytesBinary(peer.tx)}
									</span>
									<span class="flex items-center gap-0.5">
										<span class="icon icon-[mdi--handshake-outline] size-3"></span>
										{formatHandshake(peer.lastHandshake)}
									</span>
								</div>
							</div>
						</td>
						<td class="px-3 py-2 font-mono text-xs break-all">
							{#each peer.clientIPs as ip (ip)}
								<div>{ip}</div>
							{/each}
						</td>
						<td class="px-3 py-2 font-mono text-xs break-all">
							{#each peer.routableIPs ?? [] as ip (ip)}
								<div>{ip}</div>
							{:else}
								<span>—</span>
							{/each}
						</td>
						<td class="px-3 py-2">
							<div class="flex items-center gap-0.5">
								<Button
									size="icon"
									variant="ghost"
									class="h-7 w-7"
									title={peer.enabled ? 'Disable' : 'Enable'}
									onclick={() => onToggle(peer.id)}
								>
									<span
										class="icon size-4 {peer.enabled
											? 'icon-[mdi--toggle-switch] text-green-500'
											: 'icon-[mdi--toggle-switch-off-outline] text-muted-foreground'}"
									></span>
								</Button>
								<Button
									size="icon"
									variant="ghost"
									class="h-7 w-7"
									title="Export config"
									onclick={() => onExport(peer.id)}
								>
									<span class="icon icon-[mdi--export-variant] size-3.5"></span>
								</Button>
								<Button
									size="icon"
									variant="ghost"
									class="h-7 w-7"
									title="Edit"
									onclick={() => onEdit(peer)}
								>
									<span class="icon icon-[mdi--pencil-outline] size-3.5"></span>
								</Button>
								<Button
									size="icon"
									variant="ghost"
									class="h-7 w-7 text-destructive hover:text-destructive hover:bg-destructive/10"
									title="Delete peer"
									onclick={() => onDelete(peer.id)}
								>
									<span class="icon icon-[mdi--trash-can-outline] size-3.5"></span>
								</Button>
							</div>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
