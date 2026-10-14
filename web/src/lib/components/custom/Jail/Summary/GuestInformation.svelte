<script lang="ts">
	import { getJailGuestInfo } from '$lib/api/jail/jail';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { JailGuestInfo } from '$lib/types/jail/jail';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { isAPIResponse } from '$lib/utils/http';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { fade } from 'svelte/transition';

	interface Props {
		node: string;
		ctId: number;
		jailRunning: boolean;
		startedAt: string | null;
	}

	let { node, ctId, jailRunning, startedAt }: Props = $props();
	let activeView = $state('os');
	const visible = new IsDocumentVisible();
	// The jail detail poll replaces its object every second, even when these values are unchanged.
	let currentIdentity = $derived(`${node}\u0000${ctId}\u0000${startedAt ?? ''}`);
	let isRunning = $derived(jailRunning);
	type GuestSnapshot = { identity: string; info: JailGuestInfo };
	const guestInfo = resource(
		[() => currentIdentity, () => isRunning],
		async ([identity, running], _, { signal }): Promise<GuestSnapshot | null> => {
			if (!running) return null;
			const result = await getJailGuestInfo(ctId, { hostname: node, signal });
			if (isAPIResponse(result) || !result.available || !result.osInfo) return null;
			return { identity, info: result };
		},
		{ initialValue: null }
	);
	let displayInfo = $derived.by(() =>
		isRunning && !guestInfo.error && guestInfo.current?.identity === currentIdentity
			? guestInfo.current.info
			: null
	);

	function refresh() {
		if (isRunning && visible.current && !guestInfo.loading) void guestInfo.refetch();
	}

	// This also retries quietly if rc has not finished when the jail first starts.
	useInterval(15000, { callback: refresh });
	watch(
		() => visible.current,
		(isVisible, wasVisible) => {
			if (isVisible && wasVisible === false) refresh();
		}
	);
	watch(
		() => isRunning,
		(running) => {
			if (!running) activeView = 'os';
		}
	);
</script>

{#if displayInfo?.osInfo}
	<div class="space-y-4 px-4 pb-4" in:fade={{ duration: 180 }}>
		<Card.Root class="w-full gap-0 p-4">
			<Card.Header class="p-0">
				<Card.Description
					class="text-md flex items-center justify-between font-normal text-blue-600 dark:text-blue-500"
				>
					<div class="flex items-center gap-2">
						<span class="icon icon-[lucide--bot] h-6 w-6"></span>
						<span>Guest Information</span>
					</div>
					<div class="flex items-center gap-2">
						<SimpleSelect
							options={[
								{ label: 'Basic Info', value: 'os' },
								{ label: 'Network', value: 'network' }
							]}
							bind:value={activeView}
							classes={{ trigger: 'h-6! text-white min-w-[100px]' }}
							onChange={(value) => (activeView = value)}
						/>
						<Button
							variant="secondary"
							size="icon"
							class="h-5 w-5"
							onclick={refresh}
							disabled={guestInfo.loading}
							title="Refresh guest information"
							aria-label="Refresh guest information"
						>
							<span class="icon-[mdi--refresh] h-4 w-4 {guestInfo.loading ? 'animate-spin' : ''}"
							></span>
						</Button>
					</div>
				</Card.Description>
			</Card.Header>
			<Card.Content class="mt-3 p-0">
				{#if activeView === 'os'}
					<Table.Root class="w-full">
						<Table.Body>
							<Table.Row>
								<Table.Cell class="font-medium">OS Name</Table.Cell>
								<Table.Cell>{displayInfo.osInfo.name}</Table.Cell>
							</Table.Row>
							<Table.Row>
								<Table.Cell class="font-medium">Userland Release</Table.Cell>
								<Table.Cell>{displayInfo.osInfo.version}</Table.Cell>
							</Table.Row>
							<Table.Row>
								<Table.Cell class="font-medium">Host Kernel (Shared)</Table.Cell>
								<Table.Cell>{displayInfo.osInfo.kernelRelease}</Table.Cell>
							</Table.Row>
							<Table.Row>
								<Table.Cell class="font-medium">Host Architecture</Table.Cell>
								<Table.Cell>{displayInfo.osInfo.architecture}</Table.Cell>
							</Table.Row>
						</Table.Body>
					</Table.Root>
				{:else if displayInfo.networkMode === 'disabled'}
					<p class="text-muted-foreground text-sm">Networking is disabled for this jail.</p>
				{:else}
					{#if displayInfo.networkMode === 'shared'}
						<p class="text-muted-foreground mb-3 text-sm">
							Shared host network. Interfaces are inherited; traffic counters are not jail-specific.
						</p>
					{/if}
					<div class="max-h-64 overflow-y-auto rounded-md border">
						<Table.Root class="w-full">
							<Table.Header class="sticky top-0 bg-muted/50 backdrop-blur-sm">
								<Table.Row>
									<Table.Head>Interface</Table.Head>
									<Table.Head>MAC Address</Table.Head>
									<Table.Head>IP Addresses</Table.Head>
									<Table.Head class="text-right">RX / TX</Table.Head>
								</Table.Row>
							</Table.Header>
							<Table.Body>
								{#each displayInfo.interfaces as iface (iface.name)}
									<Table.Row>
										<Table.Cell class="font-mono font-bold">{iface.name}</Table.Cell>
										<Table.Cell class="font-mono text-xs">{iface.hardwareAddress || '-'}</Table.Cell
										>
										<Table.Cell>
											<div class="flex flex-col gap-1">
												{#each iface.ipAddresses as ip (`${ip.type}:${ip.address}:${ip.prefix}`)}
													<span class="text-xs">
														<span class="text-muted-foreground mr-1 uppercase">{ip.type}:</span>
														{ip.address}/{ip.prefix}
													</span>
												{:else}
													<span class="text-muted-foreground text-xs">-</span>
												{/each}
											</div>
										</Table.Cell>
										<Table.Cell class="text-right text-xs">
											{#if iface.statistics}
												<div>↓ {formatBytesBinary(iface.statistics.rxBytes)}</div>
												<div>↑ {formatBytesBinary(iface.statistics.txBytes)}</div>
											{:else}
												<span class="text-muted-foreground italic">N/A</span>
											{/if}
										</Table.Cell>
									</Table.Row>
								{:else}
									<Table.Row>
										<Table.Cell colspan={4} class="text-muted-foreground py-4 text-center">
											No network interfaces reported.
										</Table.Cell>
									</Table.Row>
								{/each}
							</Table.Body>
						</Table.Root>
					</div>
				{/if}
			</Card.Content>
		</Card.Root>
	</div>
{/if}
