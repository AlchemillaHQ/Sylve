<script lang="ts">
	import * as Card from '$lib/components/ui/card/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import * as Table from '$lib/components/ui/table/index.js';
	import { getQGAInfo } from '$lib/api/vm/vm';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { resource, watch } from 'runed';
	import type { APIResponse } from '$lib/types/common';
	import type { QGAInfo } from '$lib/types/vm/vm';
	import { fade } from 'svelte/transition';

	interface Props {
		rid: number;
		initialGaInfo: QGAInfo | APIResponse;
		refreshSignal?: number;
	}

	let { rid, initialGaInfo, refreshSignal = 0 }: Props = $props();
	let activeGaView = $state('os');
	let normalizedInitialGaInfo = $derived.by(() =>
		isAPIResponse(initialGaInfo) ? null : initialGaInfo
	);
	let fallbackGaInfo = $state<QGAInfo | null>(null);
	let hasCompletedInitialGaFetch = $state(false);

	const gaInfo = resource(
		() => `vm-qga-${rid}`,
		async (key) => {
			const result = await getQGAInfo(rid);
			if (isAPIResponse(result)) {
				return null;
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: null }
	);

	let hasGaCache = $derived.by(() => !!fallbackGaInfo);
	let isGaInfoStale = $derived.by(
		() => hasGaCache && !gaInfo.current && hasCompletedInitialGaFetch
	);
	let displayGaInfo = $derived.by(() => gaInfo.current || fallbackGaInfo);

	watch(
		() => normalizedInitialGaInfo,
		(currentInitialGaInfo) => {
			if (!fallbackGaInfo && currentInitialGaInfo) {
				fallbackGaInfo = currentInitialGaInfo;
			}
		}
	);

	watch(
		() => gaInfo.current,
		(currentGaInfo) => {
			if (currentGaInfo) {
				fallbackGaInfo = currentGaInfo;
			}
		}
	);

	watch(
		() => gaInfo.loading,
		(loading, prevLoading) => {
			if (prevLoading === true && loading === false) {
				hasCompletedInitialGaFetch = true;
			}
		}
	);

	watch(
		() => refreshSignal,
		(curr, prev) => {
			if (curr !== prev) {
				hasCompletedInitialGaFetch = false;
				gaInfo.refetch();
			}
		}
	);
</script>

{#if displayGaInfo}
	<div class="space-y-4 px-4 pb-4">
		<Card.Root class="w-full gap-0 p-4">
			<Card.Header class="p-0">
				<Card.Description
					class="text-md flex items-center justify-between font-normal text-blue-600 dark:text-blue-500"
				>
					<div class="flex items-center gap-2">
						<span class="icon icon-[lucide--bot] h-6 w-6"></span>
						<span>Guest Information</span>
						{#if isGaInfoStale}
							<span
								class="text-xs text-muted-foreground rounded-md border bg-muted px-2 py-0.5"
								in:fade={{ duration: 180 }}
								out:fade={{ duration: 120 }}
							>
								Stale Cache
							</span>
						{/if}
					</div>

					<div class="flex items-center gap-2">
						<SimpleSelect
							options={[
								{ label: 'OS Info', value: 'os' },
								{ label: 'Network', value: 'network' }
							]}
							bind:value={activeGaView}
							classes={{ trigger: 'h-6! text-white min-w-[100px]' }}
							onChange={(v: string) => {
								activeGaView = v;
							}}
						/>

						<Button
							variant="secondary"
							size="icon"
							class="h-5 w-5"
							onclick={async () => {
								await gaInfo.refetch();
							}}
							disabled={gaInfo.loading}
						>
							<span class="icon-[mdi--refresh] h-4 w-4 {gaInfo.loading ? 'animate-spin' : ''}"
							></span>
						</Button>
					</div>
				</Card.Description>
			</Card.Header>

			<Card.Content class="mt-3 p-0">
				{#if activeGaView === 'os'}
					<Table.Root class="w-full">
						<Table.Body>
							<Table.Row>
								<Table.Cell class="font-medium">OS Name</Table.Cell>
								<Table.Cell
									>{displayGaInfo.osInfo['pretty-name'] ||
										displayGaInfo.osInfo.name ||
										'Unknown'}</Table.Cell
								>
							</Table.Row>
							<Table.Row>
								<Table.Cell class="font-medium">Kernel</Table.Cell>
								<Table.Cell>{displayGaInfo.osInfo['kernel-release'] || '-'}</Table.Cell>
							</Table.Row>
							<Table.Row>
								<Table.Cell class="font-medium">Architecture</Table.Cell>
								<Table.Cell>{displayGaInfo.osInfo.machine || '-'}</Table.Cell>
							</Table.Row>
							<Table.Row>
								<Table.Cell class="font-medium">Version ID</Table.Cell>
								<Table.Cell>{displayGaInfo.osInfo['version-id'] || '-'}</Table.Cell>
							</Table.Row>
						</Table.Body>
					</Table.Root>
				{:else if activeGaView === 'network'}
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
								{#if displayGaInfo.interfaces && displayGaInfo.interfaces.length > 0}
									{#each displayGaInfo.interfaces as iface}
										<Table.Row>
											<Table.Cell class="font-mono font-bold">{iface.name || 'unknown'}</Table.Cell>
											<Table.Cell class="font-mono text-xs"
												>{iface['hardware-address'] || '-'}</Table.Cell
											>
											<Table.Cell>
												<div class="flex flex-col gap-1">
													{#if iface['ip-addresses'] && iface['ip-addresses'].length > 0}
														{#each iface['ip-addresses'] as ip}
															<span class="text-xs">
																<span class="text-muted-foreground mr-1 uppercase"
																	>{ip['ip-address-type']}:</span
																>
																{ip['ip-address']}/{ip.prefix}
															</span>
														{/each}
													{:else}
														<span class="text-muted-foreground text-xs">-</span>
													{/if}
												</div>
											</Table.Cell>
											<Table.Cell class="text-right text-xs">
												{#if iface.statistics}
													<div>↓ {formatBytesBinary(iface.statistics['rx-bytes'] || 0)}</div>
													<div>↑ {formatBytesBinary(iface.statistics['tx-bytes'] || 0)}</div>
												{:else}
													<span class="text-muted-foreground italic">N/A</span>
												{/if}
											</Table.Cell>
										</Table.Row>
									{/each}
								{:else}
									<Table.Row>
										<Table.Cell colspan={4} class="text-center text-muted-foreground py-4">
											No network interfaces reported by agent.
										</Table.Cell>
									</Table.Row>
								{/if}
							</Table.Body>
						</Table.Root>
					</div>
				{/if}
			</Card.Content>
		</Card.Root>
	</div>
{/if}
