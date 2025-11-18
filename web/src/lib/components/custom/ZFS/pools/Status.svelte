<script lang="ts">
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { capitalizeFirstLetter } from '$lib/utils/string';

	interface Props {
		open: boolean;
		pool: Zpool | null;
	}

	let { open = $bindable(), pool }: Props = $props();

	let status = $derived(pool?.status);
	let spares = $derived(pool?.spares as Zpool['spares']);
	let cacheDevices = $derived(pool?.cache as Zpool['cache']);

	let statusColor = $derived.by(() => {
		if (!pool) return 'bg-gray-500 text-white';
		switch (status?.state) {
			case 'ONLINE':
				return 'bg-green-500 text-white';
			case 'DEGRADED':
				return 'bg-yellow-500 text-white';
			case 'FAULTED':
				return 'bg-red-500 text-white';
			default:
				return 'bg-gray-500 text-white';
		}
	});

	let special = ['cache'];
</script>

{#snippet dtEl(device: Zpool['status']['devices'][0], showNote: boolean)}
	<div
		class="mr-3 h-2.5 w-2.5 rounded-full
        {device.state === 'ONLINE'
			? 'bg-green-500'
			: device.state === 'DEGRADED'
				? 'bg-yellow-500'
				: device.state === 'FAULTED'
					? 'bg-red-500'
					: 'bg-gray-500'}"
		title={device.state}
	></div>

	<div class="flex items-center gap-2 font-medium">
		<div>{device.name}</div>
		{#if device.note && showNote}
			<div
				class="rounded bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-800 dark:bg-blue-900 dark:text-blue-100"
			>
				{#if device.note === '(resilvering)'}
					<span>Resilvering</span>
				{:else}
					<span>{device.note}</span>
				{/if}
			</div>
		{/if}
	</div>

	{#if device.read > 0 || device.write > 0 || device.cksum > 0}
		<div class="ml-auto flex gap-2 text-xs">
			{#if device.read > 0}
				<span class="rounded bg-red-100 px-2 py-0.5 text-red-800 dark:bg-red-900 dark:text-red-100"
					>READ: {device.read}</span
				>
			{/if}
			{#if device.write > 0}
				<span class="rounded bg-red-100 px-2 py-0.5 text-red-800 dark:bg-red-900 dark:text-red-100"
					>WRITE: {device.write}</span
				>
			{/if}
			{#if device.cksum > 0}
				<span class="rounded bg-red-100 px-2 py-0.5 text-red-800 dark:bg-red-900 dark:text-red-100"
					>CKSUM: {device.cksum}</span
				>
			{/if}
		</div>
	{/if}
{/snippet}

{#snippet deviceTreeNode(device: Zpool['status']['devices'][0], showNote: boolean)}
	{#if !special.includes(device.name)}
		<div class="device-tree relative">
			<div class="bg-background relative flex items-center rounded-md border p-1.5">
				{#if showNote && !device.__isLast}
					<div
						class="bg-secondary absolute top-0 bottom-0 -left-6 w-0.5"
						style="height: calc(100% + 0.7rem);"
					></div>
				{:else}
					<div
						class="bg-secondary absolute top-0 bottom-0 -left-6 w-0.5"
						style="height: 18px;"
					></div>
				{/if}

				{@render dtEl(device, showNote)}
			</div>

			{#if device.children && device.children.length > 0 && !device.name.startsWith('replacing')}
				<div class=" mt-2 ml-5 space-y-2 pl-4">
					{#each device.children as child, index (child.name)}
						<div class="relative">
							<div
								class="bg-secondary h-0.5 w-6"
								style="position: absolute;left: -23px;top:18px"
							></div>
							{@render deviceTreeNode(
								{ ...child, __isLast: index === device.children.length - 1 },
								true
							)}
						</div>
					{/each}
				</div>
			{/if}

			{#if device.name.startsWith('replacing') && device.children && device.children.length > 0}
				<div class="border-border mt-2 ml-5 space-y-2 border-l-2 pl-4">
					{#each device.children as replaceDisk}
						{@render deviceTreeNode(replaceDisk, true)}
					{/each}
				</div>
			{/if}
		</div>
	{/if}
{/snippet}

{#if pool !== null && status !== undefined && spares !== undefined && cacheDevices !== undefined}
	<Dialog.Root bind:open>
		<Dialog.Content
			onInteractOutside={() => {
				open = false;
			}}
			class="fixed top-1/2 left-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-5 transition-all duration-300 ease-in-out lg:max-w-[45%]"
		>
			<!-- Header -->
			<div class="flex items-center justify-between pb-3">
				<Dialog.Header>
					<Dialog.Title class="flex items-center">
						<span class="text-primary font-semibold">Pool Status</span>
						<span class="text-muted-foreground mx-2">•</span>
						<span class="text-xl font-medium">{pool.name}</span>
						<Badge class="mt-0.5 ml-2 {statusColor} font-bold">{status.state}</Badge>
					</Dialog.Title>
				</Dialog.Header>

				<Dialog.Close
					class="flex h-5 w-5 cursor-pointer items-center justify-center rounded-sm opacity-70 transition-opacity hover:opacity-100"
					onclick={() => {
						open = false;
					}}
				>
					<span class="icon-[material-symbols--close-rounded] h-5 w-5"></span>
				</Dialog.Close>
			</div>

			<div class="space-y-5">
				<!-- Warning -->
				{#if status.status && status.status.length > 0}
					<div
						class="rounded-md border border-yellow-200 bg-yellow-50 p-4 text-yellow-800 dark:border-yellow-800 dark:bg-yellow-950 dark:text-yellow-200"
					>
						<div class="flex gap-3">
							<span class="icon-[mdi--alert-circle] mt-0.5 h-5 w-5 flex-shrink-0"></span>
							<div>
								<p class="font-medium">{status.status}</p>
								{#if status.action && status.action.length > 0}
									<p class="mt-2 text-sm">{status.action}</p>
								{/if}
							</div>
						</div>
					</div>
				{/if}

				<!-- Scan / Scrub activity-->
				<div class="space-y-4 overflow-hidden rounded-md">
					<div class="border">
						<div class="bg-muted flex items-center gap-2 px-4 py-2">
							<span class="icon-[mdi--magnify] text-primary h-5 w-5"></span>
							<span class="font-semibold">Scan Activity</span>
						</div>
						<div class="p-4">
							{#if status.scan && status.scan.length > 0}
								{#if status.scan.includes('in progress') || status.scan.includes('resilver in progress')}
									{@const progressMatch = status.scan.match(/(\d+\.\d+)%/)}
									{@const progress = progressMatch ? parseFloat(progressMatch[1]) : 0}
									{@const isResilver = status.scan.includes('resilver')}

									<div class="text-muted-foreground text-sm">
										{capitalizeFirstLetter(status.scan, true)}
									</div>
									<div class="bg-secondary mt-3 h-2.5 w-full overflow-hidden rounded-full">
										<div
											class="h-full rounded-full {isResilver ? 'bg-blue-500' : 'bg-primary'}"
											style="width: {progress}%"
										></div>
									</div>
								{:else}
									<div class="text-muted-foreground text-sm">
										{capitalizeFirstLetter(status.scan, true)}
									</div>
								{/if}
							{:else}
								<div class="text-muted-foreground flex items-center gap-2 py-1">
									<span class="icon-[material-symbols--info] h-4 w-4"></span>

									<span>No recent scan activity</span>
								</div>
							{/if}
						</div>
					</div>
				</div>

				<!-- Device Topology -->
				<div class="border">
					<div class="bg-muted flex items-center gap-2 px-4 py-2">
						<span class="icon-[tabler--topology-bus] text-primary h-5 w-5"></span>

						<span class="font-semibold">Device Topology</span>
					</div>
					<div class="h-full max-h-28 overflow-auto p-4 md:max-h-44 xl:max-h-72">
						{#if status.devices && status.devices.length > 0}
							<div class="space-y-3">
								{#each status.devices as device, index (device.name)}
									{@render deviceTreeNode(
										{ ...device, __isLast: index === device.children.length - 1 },
										false
									)}
								{/each}
							</div>

							{#if spares && spares.length > 0}
								<div class="device-tree relative mt-2">
									<div
										class="bg-background relative flex items-center gap-2 rounded-md border p-1.5"
									>
										<div
											class="bg-secondary absolute top-0 bottom-0 -left-6 w-0.5"
											style="height: calc(100% + 0.7rem);"
										></div>
										<span class="icon-[tabler--replace]"></span>

										<span class="font-medium">Spares</span>
									</div>

									<div class="mt-2 ml-5 space-y-2 pl-4">
										{#each spares as spare, index (spare.name)}
											<div class="relative">
												<div
													class="bg-secondary h-0.5 w-6"
													style="position: absolute; left: -23px; top: 18px"
												></div>
												<div class="device-tree relative">
													<div
														class="bg-background relative flex items-center gap-2 rounded-md border p-1.5"
													>
														<div
															class="bg-secondary absolute top-0 bottom-0 -left-6 w-0.5"
															style="height: 18px;"
														></div>
														<div
															class="h-2 w-2 rounded-full"
															class:bg-green-500={spare.health === 'AVAIL'}
															class:bg-yellow-400={spare.health !== 'AVAIL'}
														></div>

														<span>{spare.name}</span>
													</div>
												</div>
											</div>
										{/each}
									</div>
								</div>
							{/if}

							{#if cacheDevices && cacheDevices.length > 0}
								<div class="device-tree relative mt-2">
									<div
										class="bg-background relative flex items-center gap-2 rounded-md border p-1.5"
									>
										<div
											class="bg-secondary absolute top-0 bottom-0 -left-6 w-0.5"
											style="height: calc(100% + 0.7rem);"
										></div>
										<span class="icon-[octicon--cache-16] min-h-4 min-w-4"></span>

										<span class="font-medium">Cache</span>
									</div>

									<div class="mt-2 ml-5 space-y-2 pl-4">
										{#each cacheDevices as cache, index (cache.name)}
											<div class="relative">
												<div
													class="bg-secondary h-0.5 w-6"
													style="position: absolute; left: -23px; top: 18px"
												></div>
												<div class="device-tree relative">
													<div
														class="bg-background relative flex items-center gap-2 rounded-md border p-1.5"
													>
														<div
															class="bg-secondary absolute top-0 bottom-0 -left-6 w-0.5"
															style="height: 18px;"
														></div>
														<div
															class="h-2 w-2 rounded-full"
															class:bg-green-500={cache.health === 'ONLINE'}
															class:bg-yellow-400={cache.health !== 'ONLINE'}
														></div>

														<span>{cache.name}</span>
													</div>
												</div>
											</div>
										{/each}
									</div>
								</div>
							{/if}
						{:else}
							<div class="text-muted-foreground flex items-center gap-2 py-2">
								<span class="icon-[material-symbols--info] h-4 w-4"></span>

								<span>No devices found</span>
							</div>
						{/if}
					</div>
				</div>

				<div class="border">
					<div class="bg-muted flex items-center gap-2 px-4 py-2">
						<span class="icon-[mdi--alert] text-primary h-5 w-5"></span>
						<span class="font-semibold">Error Status</span>
					</div>
					<div class="p-4">
						<div
							class="flex items-center gap-2 rounded-md border p-2 {status.errors.includes(
								'No known data errors'
							)
								? 'border-green-200 bg-green-50 text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-200'
								: 'border-red-200 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-950 dark:text-red-200'}"
						>
							<span
								class={`icon-[${
									status.errors.includes('No known data errors')
										? 'mdi--check-circle'
										: 'mdi--alert-circle'
								}] h-5 w-5`}
							></span>
							<span>{status.errors}</span>
						</div>
					</div>
				</div>
			</div></Dialog.Content
		>
	</Dialog.Root>
{/if}
