<script lang="ts">
	import { untrack } from 'svelte';
	import DiskKindIcon from '$lib/components/DiskKindIcon.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import Combobox from '$lib/components/Combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Select from '$lib/components/ui/select';
	import * as Tabs from '$lib/components/ui/tabs';
	import {
		DISK_KINDS,
		MIN_TARGET_BYTES,
		RAID_TYPES,
		configDisks,
		diskLabel,
		formatBytes,
		isEligiblePartition,
		isPartitionMember,
		memberBytes,
		newVdev,
		partitionParent,
		poolBootProblem,
		poolNameProblem,
		poolReady,
		raidLabel,
		raidReady,
		raidRequirement,
		shortDevice,
		usableBytes,
		vdevUsableBytes,
		type PoolConfig,
		type PoolDisk
	} from '$lib/pool';
	import { cn } from '$lib/utils';
	import Icon from '@iconify/svelte';
	import partitionOutlined from '@iconify-icons/ant-design/partition-outlined';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import HardDrive from '@lucide/svelte/icons/hard-drive';
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';

	let {
		disks,
		config,
		onApply,
		onClose
	}: {
		disks: PoolDisk[];
		config: PoolConfig;
		onApply: (config: PoolConfig) => void;
		onClose: () => void;
	} = $props();

	let tab = $state('devices');

	const COMPRESSION_TYPES = [
		{ value: 'on', label: 'ON' },
		{ value: 'off', label: 'OFF' },
		{ value: 'gzip', label: 'GZIP' },
		{ value: 'lz4', label: 'LZ4' },
		{ value: 'lzjb', label: 'LZJB' },
		{ value: 'zle', label: 'ZLE' },
		{ value: 'zstd', label: 'ZSTD' },
		{ value: 'zstd-fast', label: 'ZSTD-FAST' }
	];

	function clone(config: PoolConfig): PoolConfig {
		return {
			...config,
			vdevs: config.vdevs.map((vdev) => ({ ...vdev, disks: [...vdev.disks] })),
			properties: config.properties.map((prop) => ({ ...prop }))
		};
	}

	let draft = $state<PoolConfig>(untrack(() => clone(config)));
	let openSections = $state<string[]>([]);

	const usable = $derived(usableBytes(disks, draft));
	const valid = $derived(poolReady(draft) && usable >= MIN_TARGET_BYTES);
	const nameProblem = $derived(poolNameProblem(draft.name));
	const bootProblem = $derived(poolBootProblem(draft));
	const sizeProblem = $derived(
		usable > 0 && usable < MIN_TARGET_BYTES
			? `The pool needs at least 8 GiB of usable space (currently ${formatBytes(usable)}).`
			: ''
	);
	const used = $derived(configDisks(draft).map(shortDevice));

	const sections = $derived(
		DISK_KINDS.map((kind) => {
			const kindDisks = disks.filter((disk) => {
				if (disk.kind !== kind || disk.boot) return false;
				const dev = shortDevice(disk.device);
				if (used.includes(dev)) return false;
				return !(disk.partitions ?? []).some(
					(part) => part.name && used.includes(shortDevice(part.name))
				);
			});
			const parts = kindDisks.flatMap((disk) =>
				(disk.partitions ?? [])
					.filter((part) => isEligiblePartition(part) && part.name)
					.filter(
						(part) =>
							!used.includes(shortDevice(part.name ?? '')) &&
							!used.includes(shortDevice(disk.device))
					)
					.map((part) => ({ disk, part }))
			);
			return { kind, disks: kindDisks, parts, count: kindDisks.length + parts.length };
		}).filter((section) => section.count > 0)
	);

	function selectable(member: string): boolean {
		const name = shortDevice(member);
		return disks.some(
			(disk) =>
				shortDevice(disk.device) === name ||
				(disk.partitions ?? []).some((part) => part.name === name && isEligiblePartition(part))
		);
	}

	function kindOfMember(member: string): string {
		const name = shortDevice(member);
		if (isPartitionMember(name)) {
			return (
				disks.find((disk) => shortDevice(disk.device) === partitionParent(name))?.kind ?? 'other'
			);
		}
		return disks.find((disk) => shortDevice(disk.device) === name)?.kind ?? 'other';
	}

	function addMember(member: string, vdevId?: string) {
		const name = shortDevice(member);
		if (used.includes(name)) return;
		const target = vdevId
			? draft.vdevs.find((vdev) => vdev.id === vdevId)
			: draft.vdevs[draft.vdevs.length - 1];
		if (!target) return;
		draft.vdevs = draft.vdevs.map((vdev) =>
			vdev.id === target.id ? { ...vdev, disks: [...vdev.disks, name] } : vdev
		);
	}

	function removeMember(member: string, vdevId: string) {
		draft.vdevs = draft.vdevs.map((vdev) =>
			vdev.id === vdevId ? { ...vdev, disks: vdev.disks.filter((m) => m !== member) } : vdev
		);
		const kind = kindOfMember(member);
		if (!openSections.includes(kind)) openSections = [...openSections, kind];
	}

	function addVdev() {
		draft.vdevs = [...draft.vdevs, newVdev()];
	}

	function removeVdev(vdevId: string) {
		if (draft.vdevs.length <= 1) return;
		const removed = draft.vdevs.find((vdev) => vdev.id === vdevId);
		draft.vdevs = draft.vdevs.filter((vdev) => vdev.id !== vdevId);
		for (const member of removed?.disks ?? []) {
			const kind = kindOfMember(member);
			if (!openSections.includes(kind)) openSections = [...openSections, kind];
		}
	}

	function isOpen(kind: string) {
		return openSections.includes(kind);
	}

	function toggleSection(kind: string) {
		openSections = isOpen(kind)
			? openSections.filter((k) => k !== kind)
			: [...openSections, kind];
	}

	function handleDragOver(event: DragEvent) {
		event.preventDefault();
		if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
	}

	function handleDrop(event: DragEvent, vdevId: string) {
		event.preventDefault();
		const member = event.dataTransfer?.getData('text/plain');
		if (!member) return;
		if (used.includes(shortDevice(member))) {
			moveMember(shortDevice(member), vdevId);
			return;
		}
		if (selectable(member)) addMember(member, vdevId);
	}

	function moveMember(member: string, vdevId: string) {
		const name = shortDevice(member);
		const target = draft.vdevs.find((vdev) => vdev.id === vdevId);
		if (!target || target.disks.includes(name)) return;
		draft.vdevs = draft.vdevs.map((vdev) => ({
			...vdev,
			disks:
				vdev.id === vdevId
					? [...vdev.disks.filter((d) => d !== name), name]
					: vdev.disks.filter((d) => d !== name)
		}));
	}

	function apply() {
		if (!valid) return;
		const out = clone(draft);
		out.name = out.name.trim();
		onApply(out);
	}
</script>

<Dialog.Root
	open
	onOpenChange={(value) => {
		if (!value) onClose();
	}}
>
	<Dialog.Content
		class="flex h-[72vh] w-[94%] max-w-3xl flex-col gap-3 overflow-hidden p-4 sm:max-w-3xl"
		showCloseButton={false}
	>
		<div class="flex items-start justify-between gap-4">
			<div class="space-y-1">
				<Dialog.Title class="flex items-center gap-2 text-left text-base font-medium">
					<HardDrive class="size-5" />
					Create ZFS Pool
				</Dialog.Title>
				<Dialog.Description class="text-muted-foreground text-xs">
					Selected whole disks are repartitioned; existing FreeBSD ZFS partitions are used
					as-is.
				</Dialog.Description>
			</div>
			<div class="flex items-center gap-2">
				<button
					class="text-muted-foreground transition-colors hover:text-foreground focus:outline-none"
					onclick={onClose}
					title="Close"
					aria-label="Close"
				>
					<X class="h-5 w-5" />
				</button>
			</div>
		</div>

		<Tabs.Root bind:value={tab} class="flex min-h-0 flex-1 flex-col gap-1">
			<Tabs.List class="grid w-full grid-cols-2 p-0">
				<Tabs.Trigger value="devices" class="border-b">Devices</Tabs.Trigger>
				<Tabs.Trigger value="options" class="border-b">Options</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="devices" class="min-h-0 space-y-3 overflow-y-auto pt-2 pr-1">
				<div class="space-y-1.5">
					<Label for="pool-name">Name</Label>
					<Input
						id="pool-name"
						bind:value={draft.name}
						placeholder="zroot"
						autocomplete="off"
						spellcheck="false"
						aria-invalid={!!nameProblem}
					/>
					{#if nameProblem}
						<p class="text-destructive text-xs">{nameProblem}</p>
					{/if}
				</div>

				<div>
					<div class="flex items-center justify-between">
						<Label>
							<span>
								VDEVs
								{#if usable}
									<span class="font-semibold text-green-600"
										>({formatBytes(usable)} Usable)</span
									>
								{/if}
							</span>
						</Label>
						<Button variant="outline" size="sm" class="h-7 px-2 text-xs" onclick={addVdev}>
							<Plus class="size-3.5" /> Add VDEV
						</Button>
					</div>

					<div class="mt-1 space-y-2">
						{#each draft.vdevs as vdev (vdev.id)}
							<div class="bg-accent/30 space-y-2 rounded-lg p-3">
								<div class="flex items-center gap-2">
									<Select.Root type="single" bind:value={vdev.raid}>
										<Select.Trigger class="h-7 w-28 text-xs" aria-label="RAID layout">
											{raidLabel(vdev.raid)}
										</Select.Trigger>
										<Select.Content>
											{#each RAID_TYPES as raid (raid.value)}
												<Select.Item
													value={raid.value}
													label={raid.label}
													disabled={!raidReady(raid.value, vdev.disks.length)}
												/>
											{/each}
										</Select.Content>
									</Select.Root>
									{#if vdev.disks.length > 0 && !raidReady(vdev.raid, vdev.disks.length)}
										<span class="text-destructive text-xs">
											{raidLabel(vdev.raid)} {raidRequirement(vdev.raid)}
										</span>
									{/if}
									<span class="text-muted-foreground ml-auto text-xs">
										{vdevUsableBytes(disks, vdev)
											? `${formatBytes(vdevUsableBytes(disks, vdev))} Usable`
											: ''}
									</span>
									{#if draft.vdevs.length > 1}
										<button
											class="text-muted-foreground opacity-60 transition-opacity hover:opacity-100 focus:outline-none"
											onclick={() => removeVdev(vdev.id)}
											title="Remove vdev"
											aria-label="Remove vdev"
										>
											<X class="size-3.5" />
										</button>
									{/if}
								</div>

								<div
									class={cn(
										'bg-primary/5 relative flex min-h-16 w-full items-center rounded-lg border border-dashed p-3 transition-colors',
										vdev.disks.length === 0
											? 'border-muted-foreground/40'
											: 'border-primary/30'
									)}
									role="group"
									aria-label="VDEV drop zone"
									ondragover={handleDragOver}
									ondrop={(event) => handleDrop(event, vdev.id)}
								>
									{#if vdev.disks.length === 0}
										<div
											class="text-muted-foreground/70 mx-auto flex h-[52px] items-center text-sm"
										>
											Drag disks or partitions here, or press + below
										</div>
									{:else}
										<div class="flex w-full flex-wrap items-center gap-5" role="list">
											{#each vdev.disks as member (member)}
												<div
													class="relative cursor-move text-center"
													role="listitem"
													draggable="true"
													ondragstart={(event) => {
														event.dataTransfer?.setData('text/plain', shortDevice(member));
														if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
													}}
												>
													{#if isPartitionMember(member)}
														<Icon
															icon={partitionOutlined}
															class="mx-auto size-9 rotate-90 text-blue-500"
														/>
														<div class="max-w-20 truncate text-xs">
															{shortDevice(member)}
														</div>
														<div class="text-muted-foreground text-[10px]">
															{formatBytes(memberBytes(disks, member))}
														</div>
													{:else}
														<DiskKindIcon
															kind={
																disks.find((d) => shortDevice(d.device) === member)?.kind ?? 'other'
															}
															class="mx-auto size-9"
														/>
														<div class="max-w-16 truncate text-xs">{shortDevice(member)}</div>
													{/if}
													<button
														class="absolute -top-1 -right-1 flex size-5 items-center justify-center rounded-full bg-red-500 text-white hover:bg-red-600"
														onclick={() => removeMember(member, vdev.id)}
														title={`Remove ${shortDevice(member)}`}
														aria-label={`Remove ${shortDevice(member)}`}
													>
														<X class="size-3" />
													</button>
												</div>
											{/each}
										</div>
									{/if}
								</div>
							</div>
						{/each}
					</div>

					{#if bootProblem}
						<p class="text-destructive mt-1 text-xs">{bootProblem}</p>
					{/if}
					{#if sizeProblem}
						<p class="text-destructive mt-1 text-xs">{sizeProblem}</p>
					{/if}
				</div>

				<div>
					<Label>Available disks</Label>
					{#if sections.length === 0}
						<div
							class="bg-muted/30 mt-1 flex min-h-24 flex-col items-center justify-center gap-2 rounded-lg border border-dashed p-6 text-center"
						>
							<HardDrive class="text-muted-foreground/60 size-8" />
							<div class="space-y-0.5">
								<p class="text-sm">No disks or partitions available</p>
								<p class="text-muted-foreground text-xs">
									Everything is already part of a vdev, or reserved as installer media.
								</p>
							</div>
						</div>
					{:else}
						<div class="mt-1 grid items-start gap-2 sm:grid-cols-2">
							{#each sections as section (section.kind)}
								<div
									class={cn(
										'overflow-hidden rounded-lg border',
										sections.length === 1 && 'sm:col-span-2'
									)}
								>
								<button
									class="hover:bg-muted/40 flex w-full items-center justify-between gap-2 px-3 py-2 text-left transition-colors"
									onclick={() => toggleSection(section.kind)}
									aria-expanded={isOpen(section.kind)}
								>
									<span class="flex items-center gap-2 text-sm font-medium">
										<DiskKindIcon kind={section.kind} class="size-4" />
										{diskLabel(section.kind)}
										<span class="text-muted-foreground text-xs font-normal">
											({section.count} available)
										</span>
									</span>
									<ChevronDown
										class={cn(
											'text-muted-foreground size-4 shrink-0 transition-transform',
											isOpen(section.kind) && 'rotate-180'
										)}
									/>
								</button>
								{#if isOpen(section.kind)}
									<div
										class="flex max-h-40 min-h-16 flex-wrap items-center gap-5 overflow-y-auto px-3 pt-1 pb-3"
									>
										{#each section.disks as disk (disk.device)}
											<div class="relative text-center">
												<button
													class="cursor-move"
													draggable="true"
													ondragstart={(event) => {
														event.dataTransfer?.setData('text/plain', shortDevice(disk.device));
														if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
													}}
													onclick={() => addMember(disk.device)}
													title={`Add ${shortDevice(disk.device)}`}
												>
													<DiskKindIcon kind={disk.kind} class="size-9" />
												</button>
												<div class="max-w-16 truncate text-xs">
													{shortDevice(disk.device)}
												</div>
												<div class="text-muted-foreground text-xs">
													{formatBytes(disk.sizeBytes)}
												</div>
												<button
													class="absolute -top-1 -right-1 flex size-5 items-center justify-center rounded-full bg-green-500 text-white hover:bg-green-600"
													onclick={() => addMember(disk.device)}
													title={`Add ${shortDevice(disk.device)}`}
													aria-label={`Add ${shortDevice(disk.device)}`}
												>
													<Plus class="size-3" />
												</button>
											</div>
										{/each}
										{#each section.parts as { disk, part } (part.name)}
											<div class="relative text-center">
												<button
													class="cursor-move"
													draggable="true"
													ondragstart={(event) => {
														event.dataTransfer?.setData('text/plain', shortDevice(part.name ?? ''));
														if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
													}}
													onclick={() => addMember(part.name ?? '')}
													title={`Add ${shortDevice(part.name ?? '')} from ${shortDevice(disk.device)}`}
												>
													<Icon
														icon={partitionOutlined}
														class="mx-auto size-9 rotate-90 text-blue-500"
													/>
												</button>
												<div class="max-w-20 truncate text-xs">
													{shortDevice(part.name ?? '')}
												</div>
												<div class="text-muted-foreground text-xs">
													{formatBytes(part.sizeBytes)}
												</div>
												<button
													class="absolute -top-1 -right-1 flex size-5 items-center justify-center rounded-full bg-green-500 text-white hover:bg-green-600"
													onclick={() => addMember(part.name ?? '')}
													title={`Add ${shortDevice(part.name ?? '')}`}
													aria-label={`Add ${shortDevice(part.name ?? '')}`}
												>
													<Plus class="size-3" />
												</button>
											</div>
										{/each}
									</div>
								{/if}
							</div>
						{/each}
					</div>
					{/if}
				</div>
			</Tabs.Content>

			<Tabs.Content value="options" class="min-h-0 space-y-4 overflow-y-auto pt-2 pr-1">
				<div class="space-y-1.5">
					<Label for="pool-comment">Comment</Label>
					<Input
						id="pool-comment"
						bind:value={draft.comment}
						placeholder="Optional comment about the pool"
					/>
				</div>

				<div class="grid gap-4 sm:grid-cols-2">
					<div class="space-y-1.5">
						<Label for="pool-compression">Compression</Label>
						<Combobox
							id="pool-compression"
							bind:value={draft.compression}
							options={COMPRESSION_TYPES}
							placeholder="Select or type compression"
							searchPlaceholder="Select or type compression"
							allowCustom
						/>
					</div>
					<div class="space-y-1.5">
						<Label for="pool-ashift">ASHIFT</Label>
						<Select.Root type="single" bind:value={draft.ashift}>
							<Select.Trigger id="pool-ashift" class="h-9 w-full text-sm">
								{draft.ashift}
							</Select.Trigger>
							<Select.Content>
								{#each ['9', '10', '11', '12', '13', '14', '15', '16'] as value (value)}
									<Select.Item {value} label={value} />
								{/each}
							</Select.Content>
						</Select.Root>
					</div>
				</div>

				<div class="space-y-2">
					<Label>Custom properties</Label>
					{#each draft.properties as prop, index (index)}
						<div class="flex items-center gap-2">
							<Input
								bind:value={prop.key}
								placeholder="Key"
								class="h-8 flex-1 font-mono text-xs"
								autocomplete="off"
								spellcheck="false"
							/>
							<Input
								bind:value={prop.value}
								placeholder="Value"
								class="h-8 flex-1 font-mono text-xs"
								autocomplete="off"
								spellcheck="false"
							/>
							<Button
								variant="outline"
								size="icon"
								class="size-8 shrink-0"
								onclick={() =>
									(draft.properties = draft.properties.filter((_, i) => i !== index))}
								title="Remove property"
								aria-label="Remove property"
							>
								<X class="size-3.5" />
							</Button>
						</div>
					{/each}
					<Button
						variant="outline"
						size="sm"
						class="h-7 px-2 text-xs"
						onclick={() => (draft.properties = [...draft.properties, { key: '', value: '' }])}
					>
						<Plus class="size-3.5" /> Add property
					</Button>
				</div>

				<div class="flex items-center gap-3">
					<Checkbox id="pool-trim" bind:checked={draft.autotrim} />
					<Label for="pool-trim">Auto Trim</Label>
				</div>
			</Tabs.Content>
		</Tabs.Root>

		<Dialog.Footer class="flex justify-end border-t pt-3">
			<Button size="sm" onclick={apply} disabled={!valid}>Apply</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
