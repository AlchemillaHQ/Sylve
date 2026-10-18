<script lang="ts">
	import { untrack } from 'svelte';
	import DiskKindIcon from '$lib/components/DiskKindIcon.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Select from '$lib/components/ui/select';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import {
		diskLabel,
		formatBytes,
		parseSize,
		partitionTypeLabel,
		shortDevice,
		type ApiDisk,
		type PoolDisk
	} from '$lib/pool';
	import { cn } from '$lib/utils';
	import HardDrive from '@lucide/svelte/icons/hard-drive';
	import Plus from '@lucide/svelte/icons/plus';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
	import X from '@lucide/svelte/icons/x';

	let {
		disks,
		onClose,
		onDisks
	}: {
		disks: PoolDisk[];
		onClose: () => void;
		onDisks: (disks: ApiDisk[]) => void;
	} = $props();

	let selected = $state(untrack(() => disks.find((d) => !d.boot)?.device ?? disks[0]?.device ?? ''));
	let busy = $state(false);
	let error = $state('');
	let status = $state('');
	let confirmWipe = $state(false);
	let confirmDelete = $state('');

	let partSize = $state('16G');
	let partLabel = $state('');
	let partType = $state('freebsd-zfs');

	const PARTITION_TYPES: { value: string; label: string; size?: string; hint?: string }[] = [
		{ value: 'freebsd-zfs', label: 'ZFS' },
		{ value: 'freebsd-ufs', label: 'UFS', hint: 'A UFS filesystem.' },
		{ value: 'freebsd-swap', label: 'Swap', hint: 'A swap partition.' },
		{
			value: 'freebsd-boot',
			label: 'Boot (BIOS)',
			size: '512k',
			hint: 'Holds the BIOS boot loader — 512 KiB is the usual size.'
		},
		{
			value: 'efi',
			label: 'EFI',
			size: '260M',
			hint: 'EFI system partition for UEFI boot — 260 MiB is the usual size.'
		}
	];
	const partTypeInfo = $derived(
		PARTITION_TYPES.find((item) => item.value === partType) ?? PARTITION_TYPES[0]
	);

	function selectPartType(value: string) {
		partType = value;
		const info = PARTITION_TYPES.find((item) => item.value === value);
		if (info?.size) partSize = info.size;
	}

	const orderedDisks = $derived(
		[...disks].sort((a, b) => {
			if (!!a.boot !== !!b.boot) return a.boot ? 1 : -1;
			return a.device.localeCompare(b.device);
		})
	);
	const disk = $derived(disks.find((d) => d.device === selected) ?? null);
	const partCount = $derived(disk?.partitions?.length ?? 0);
	const usedBytes = $derived((disk?.partitions ?? []).reduce((sum, p) => sum + p.sizeBytes, 0));
	const freeBytes = $derived(disk ? Math.max(0, disk.sizeBytes - usedBytes) : 0);

	async function mutate(path: string, body: unknown, success: string) {
		error = '';
		status = '';
		busy = true;
		try {
			const res = await fetch(path, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			});
			const json = await res.json().catch(() => null);
			if (!res.ok) {
				error = json?.error ?? `The action failed (HTTP ${res.status}).`;
				return;
			}
			if (Array.isArray(json?.disks)) onDisks(json.disks);
			status = success;
		} catch (e) {
			error = String(e);
		} finally {
			busy = false;
		}
	}

	function wipe() {
		if (!disk) return;
		confirmWipe = false;
		confirmDelete = '';
		mutate('/api/disks/wipe', { name: shortDevice(disk.device) }, `Wiped ${shortDevice(disk.device)}.`);
	}

	function initGpt() {
		if (disk)
			mutate(
				'/api/disks/gpt',
				{ name: shortDevice(disk.device) },
				`GPT initialized on ${shortDevice(disk.device)}.`
			);
	}

	function addPartition() {
		if (!disk) return;
		const raw = partSize.trim();
		const size = raw === '' || raw.toLowerCase() === 'max' ? 0 : parseSize(raw);
		if (size === null || (size === 0 && !['', 'max'].includes(raw.toLowerCase()))) {
			error = 'Enter a size such as 16G or 512M, or leave it empty for the rest.';
			return;
		}
		const label = partLabel.trim();
		if (label !== '' && !/^[A-Za-z0-9._-]{1,32}$/.test(label)) {
			error = 'Labels may use up to 32 letters, digits, dots, dashes or underscores.';
			return;
		}
		mutate(
			'/api/disks/partitions',
			{
				name: shortDevice(disk.device),
				sizeBytes: size,
				type: partType,
				label
			},
			`Added ${label || partTypeInfo.label} on ${shortDevice(disk.device)}.`
		);
		partLabel = '';
	}

	function requestDelete(index: number) {
		if (!disk) return;
		confirmWipe = false;
		confirmDelete = `${shortDevice(disk.device)}p${index}`;
	}

	function deletePartition() {
		if (!confirmDelete) return;
		const partition = confirmDelete;
		confirmDelete = '';
		mutate('/api/disks/partitions/delete', { partition }, `Deleted ${partition}.`);
	}
</script>

<Dialog.Root
	open={true}
	onOpenChange={(value) => {
		if (!value) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[72vh] w-[94%] max-w-4xl flex-col gap-3 overflow-hidden p-4 sm:max-w-4xl"
		showCloseButton={false}
	>
		<div class="flex items-start justify-between gap-4">
			<div class="space-y-1">
				<Dialog.Title class="flex items-center gap-2 text-left text-base font-medium">
					<HardDrive class="size-5" />
					Disks &amp; Partitions
				</Dialog.Title>
				<Dialog.Description class="text-muted-foreground text-xs">
					Inspect disks, wipe them, or edit their partition table. The installer media cannot
					be modified.
				</Dialog.Description>
			</div>
			<button
				class="text-muted-foreground transition-colors hover:text-foreground focus:outline-none"
				onclick={onClose}
				title="Close"
				aria-label="Close"
			>
				<X class="h-5 w-5" />
			</button>
		</div>

		<div class="grid min-h-0 flex-1 gap-4 sm:grid-cols-[230px_1fr]">
			<div class="min-h-0 space-y-1.5 overflow-y-auto pr-1 pb-2">
				{#each orderedDisks as item (item.device)}
					<button
						class={cn(
							'flex w-full items-center gap-3 rounded-lg border px-3 py-2 text-left transition-colors',
							item.device === selected ? 'border-primary bg-primary/5' : 'hover:bg-muted/40'
						)}
						onclick={() => {
							selected = item.device;
							error = '';
							status = '';
							confirmWipe = false;
							confirmDelete = '';
						}}
					>
						<DiskKindIcon kind={item.kind} class="size-5 shrink-0" />
						<span class="min-w-0 flex-1">
							<span class="block truncate text-sm">{shortDevice(item.device)}</span>
							<span class="text-muted-foreground block truncate text-xs">
								{item.model || diskLabel(item.kind)} · {formatBytes(item.sizeBytes)}
							</span>
						</span>
						{#if item.boot}
							<span
								class="text-muted-foreground shrink-0 text-[10px] uppercase"
								title="Installer media — cannot be modified"
							>
								media
							</span>
						{/if}
					</button>
				{:else}
					<p class="text-muted-foreground text-sm">No disks detected.</p>
				{/each}
			</div>

			<div class="min-h-0 overflow-y-auto pr-1">
				{#if !disk}
					<p class="text-muted-foreground text-sm">Select a disk.</p>
				{:else}
					<div class="space-y-4">
						<div class="flex items-center gap-3 rounded-lg border bg-muted/30 p-3">
							<DiskKindIcon kind={disk.kind} class="size-6 shrink-0" />
							<div class="min-w-0 flex-1">
								<p class="truncate text-sm font-medium">
									{shortDevice(disk.device)} · {diskLabel(disk.kind)}
								</p>
								<p class="text-muted-foreground truncate text-xs">
									{disk.model || 'unknown model'}{disk.serial ? ` · ${disk.serial}` : ''}
								</p>
							</div>
							<div class="shrink-0 text-right text-xs">
								<p>{formatBytes(disk.sizeBytes)}</p>
								<p class="text-muted-foreground">
									{disk.scheme
										? `${disk.scheme} · ${partCount} partition${partCount === 1 ? '' : 's'}`
										: 'No partition table'}
								</p>
							</div>
						</div>

						{#if disk.boot}
							<p class="text-muted-foreground text-xs">
								This is the live installer media — it cannot be wiped or repartitioned.
							</p>
						{:else}
							<div class="space-y-2">
								<div class="flex items-center justify-between">
									<Label>Partitions</Label>
									<span class="text-muted-foreground text-xs">
										Free ≈ {formatBytes(freeBytes)}
									</span>
								</div>
								<div class="overflow-hidden rounded-lg border">
									{#if (disk.partitions?.length ?? 0) === 0}
										<p class="text-muted-foreground p-3 text-sm">
											No partitions{disk.scheme ? '' : ' — initialize GPT first'}.
										</p>
									{:else}
										{#each disk.partitions ?? [] as part (part.index)}
											<div class="flex items-center gap-3 border-b px-3 py-2 last:border-b-0">
												<span class="text-muted-foreground w-7 text-xs">p{part.index}</span>
												<span class="min-w-0 flex-1 truncate text-sm">
													{partitionTypeLabel(part.type)}{part.label ? ` · ${part.label}` : ''}
												</span>
												<span class="text-muted-foreground shrink-0 text-xs">
													{formatBytes(part.sizeBytes)}
												</span>
												<button
													class="text-muted-foreground hover:text-destructive shrink-0 transition-colors"
													onclick={() => requestDelete(part.index)}
													title={`Delete partition ${part.index}`}
													aria-label={`Delete partition ${part.index}`}
												>
													<Trash2 class="size-3.5" />
												</button>
											</div>
										{/each}
									{/if}
								</div>
							</div>

							<div class="space-y-1.5">
								<div class="grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto] sm:items-end">
									<div class="space-y-1.5">
										<Label for="part-type">Type</Label>
										<Select.Root type="single" value={partType} onValueChange={selectPartType}>
											<Select.Trigger id="part-type" size="sm" class="w-full text-xs">
												{partTypeInfo.label}
											</Select.Trigger>
											<Select.Content>
												{#each PARTITION_TYPES as item (item.value)}
													<Select.Item value={item.value} label={item.label} />
												{/each}
											</Select.Content>
										</Select.Root>
									</div>
									<div class="space-y-1.5">
										<Label for="part-size">Size</Label>
										<Input
											id="part-size"
											bind:value={partSize}
											placeholder="16G, empty = rest"
											class="h-8"
										/>
									</div>
									<div class="space-y-1.5">
										<Label for="part-label">Label</Label>
										<Input
											id="part-label"
											bind:value={partLabel}
											placeholder="Optional"
											class="h-8"
										/>
									</div>
									<Button size="sm" onclick={addPartition} disabled={busy || !disk.scheme}>
										<Plus class="size-3.5" /> Add partition
									</Button>
								</div>
								{#if partTypeInfo.hint}
									<p class="text-muted-foreground text-xs">
										{partTypeInfo.hint}
									</p>
								{/if}
							</div>

							{#if error}
								<p class="text-destructive text-xs">{error}</p>
							{:else if status}
								<p class="text-xs text-emerald-600 dark:text-emerald-400">{status}</p>
							{/if}

							<div class="space-y-3 border-t pt-3">
								{#if !confirmWipe && !confirmDelete}
									<div class="flex flex-wrap items-center gap-2">
										<Button
											variant="outline"
											size="sm"
											onclick={initGpt}
											disabled={busy || !!disk.scheme}
											title={disk.scheme
												? `${shortDevice(disk.device)} already has a ${disk.scheme} partition table`
												: `Create a GPT partition table on ${shortDevice(disk.device)}`}
										>
											Initialize GPT
										</Button>
										<Button
											variant="outline"
											size="sm"
											onclick={() => {
												confirmDelete = '';
												confirmWipe = true;
											}}
											disabled={busy || confirmWipe}
										>
											Wipe Disk
										</Button>
									</div>
								{/if}

								{#if confirmWipe}
									<div
										class="border-destructive/40 bg-destructive/5 flex flex-wrap items-center gap-2 rounded-lg border p-2"
									>
										<TriangleAlert class="text-destructive size-3.5 shrink-0" />
										<p class="min-w-0 flex-1 text-xs">
											Wipe {shortDevice(disk.device)}? Everything on it will be erased.
										</p>
										<Button variant="destructive" size="sm" onclick={wipe} disabled={busy}>
											Yes, wipe
										</Button>
										<Button
											variant="outline"
											size="sm"
											onclick={() => (confirmWipe = false)}
											disabled={busy}
										>
											Cancel
										</Button>
									</div>
								{/if}

								{#if confirmDelete}
									<div
										class="border-destructive/40 bg-destructive/5 flex flex-wrap items-center gap-2 rounded-lg border p-2"
									>
										<TriangleAlert class="text-destructive size-3.5 shrink-0" />
										<p class="min-w-0 flex-1 text-xs">
											Delete {confirmDelete}? This cannot be undone.
										</p>
										<Button variant="destructive" size="sm" onclick={deletePartition} disabled={busy}>
											Yes, delete
										</Button>
										<Button
											variant="outline"
											size="sm"
											onclick={() => (confirmDelete = '')}
											disabled={busy}
										>
											Cancel
										</Button>
									</div>
								{/if}
							</div>
						{/if}
					</div>
				{/if}
			</div>
		</div>
	</Dialog.Content>
</Dialog.Root>
