<script lang="ts">
	import Controls from '$lib/components/custom/System/Updates/Controls.svelte';
	import Details from '$lib/components/custom/System/Updates/Details.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import Combobox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { packageKindLabel, type JailPackage } from '$lib/types/jail/packages';
	import type { HostUpdateContext } from '$lib/types/system/updates';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import { watch } from 'runed';
	import { getContext } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';

	const updates = getContext<HostUpdateContext>('hostUpdates');
	const view = $derived(updates.view);
	const enabled = $derived(updates.enabled && !!view?.ready);
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let updatesOpen = $state(false);
	let filterOpen = $state(false);
	let filters = $state<string[]>([]);
	let details = $state<JailPackage | null>(null);
	let detailsOpen = $state(false);
	const vulnerableOnly = $derived(filters.includes('vulnerable'));
	const updatesOnly = $derived(filters.includes('updates'));
	const selected = $derived(
		(activeRows ?? []).map((row) => view?.packages.find((pkg) => pkg.name === row.id))
	);
	const single = $derived(selected.length === 1 ? selected[0] : null);
	const selectionError = $derived.by(() => {
		if (selected.some((pkg) => pkg?.kind === 'base' || pkg?.kind === 'kernel')) {
			return 'Base and kernel packages require Preview all upgrades';
		}
		if (
			selected.some(
				(pkg) => !pkg || pkg.locked || !['application', 'package-manager'].includes(pkg.kind)
			)
		) {
			return 'Selection includes locked, protected or missing packages';
		}
		if (
			selected.some(
				(pkg) =>
					(vulnerableOnly && !pkg?.advisories.length) ||
					(updatesOnly && (!pkg || !availableVersion(pkg)))
			)
		) {
			return 'Selection includes filtered-out packages';
		}
		return '';
	});
	const selectionEligible = $derived(selected.length > 0 && !selectionError);
	watch(
		() => `${vulnerableOnly}:${updatesOnly}`,
		() => {
			activeRows = null;
		}
	);
	watch(
		() => updates.node,
		() => {
			activeRows = null;
			details = null;
			detailsOpen = false;
			filters = [];
			query = '';
			updatesOpen = false;
			filterOpen = false;
		}
	);
	function availableVersion(pkg: JailPackage) {
		return view?.updatesChecked ? pkg.available : '';
	}
	function formatStatus(cell: CellComponent) {
		const pkg = cell.getRow().getData();
		const flags: string[] = [];
		if (pkg.advisoryCount > 0) flags.push(`${pkg.advisoryCount} advisories`);
		if (pkg.availableVersion) flags.push('Update available');
		if (pkg.locked) flags.push('Locked');
		if (pkg.kind !== 'application') flags.push(packageKindLabel(pkg.kind));
		if (flags.length === 0)
			flags.push(pkg.auditStale ? 'Not audited / stale' : 'No known vulnerabilities');
		return renderWithIcon(
			pkg.advisoryCount > 0
				? 'mdi:alert-outline'
				: pkg.locked
					? 'mdi:lock-outline'
					: pkg.kind === 'base' || pkg.kind === 'kernel'
						? 'mdi:layers-outline'
						: 'mdi:information-outline',
			escapeHTML(flags.join(' · ')),
			pkg.advisoryCount > 0 ? 'text-yellow-600' : ''
		);
	}
	const columns: Column[] = [
		{ field: 'name', title: 'Name', formatter: 'plaintext' },
		{ field: 'version', title: 'Version', formatter: 'plaintext' },
		{ field: 'available', title: 'Available', formatter: 'plaintext' },
		{ field: 'repository', title: 'Repository', formatter: 'plaintext' },
		{ field: 'status', title: 'Status', formatter: formatStatus },
		{ field: 'origin', title: 'Origin', visible: false, formatter: 'plaintext' },
		{ field: 'kindLabel', title: 'Type', visible: false, formatter: 'plaintext' },
		{
			field: 'size',
			title: 'Size',
			visible: false,
			formatter: (cell: CellComponent) => formatBytesBinary(Number(cell.getValue()))
		},
		{
			field: 'automatic',
			title: 'Automatic',
			visible: false,
			formatter: (cell: CellComponent) => (cell.getValue() ? 'Yes' : 'No')
		},
		{
			field: 'locked',
			title: 'Locked',
			visible: false,
			formatter: (cell: CellComponent) => (cell.getValue() ? 'Yes' : 'No')
		},
		{ field: 'advisoryCount', title: 'Advisories', visible: false, sorter: 'number' }
	];
	const tableData = $derived({
		columns,
		rows: (view?.packages ?? [])
			.filter(
				(pkg) =>
					(!vulnerableOnly || pkg.advisories.length > 0) &&
					(!updatesOnly || !!availableVersion(pkg))
			)
			.map((pkg) => ({
				...pkg,
				id: pkg.name,
				availableVersion: availableVersion(pkg),
				available: view?.updatesChecked ? pkg.available || '—' : 'Not checked',
				kindLabel: packageKindLabel(pkg.kind),
				advisoryCount: pkg.advisories.length,
				auditStale: view?.auditStale || !!updates.error,
				status: ''
			}))
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<DropdownMenu.Root bind:open={updatesOpen}>
			<DropdownMenu.Trigger disabled={!enabled}>
				{#snippet child({ props })}<Button
						{...props}
						size="sm"
						class="h-6"
						title="Package update actions"
					>
						<SpanWithIcon icon="icon-[mdi--update]" size="h-4 w-4" gap="gap-2" title="Updates" />
						<span class="icon-[mdi--chevron-down] h-4 w-4" aria-hidden="true"></span>
					</Button>{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="start" class="w-64">
				<DropdownMenu.Item
					onSelect={() => void updates.run('check')}
					disabled={!enabled}
					class="cursor-pointer"
					title={`Last successful check: ${view?.checkedAt ? convertDbTime(view.checkedAt) : 'Never'}`}
				>
					<span class="icon-[mdi--update] h-4 w-4" aria-hidden="true"></span>Check for updates
				</DropdownMenu.Item>
				<DropdownMenu.Item
					onSelect={() => void updates.run('packages-preview')}
					disabled={!enabled}
					class="cursor-pointer"
					title="Preview all host packages, including base, kernel and kernel modules. Downloads and installs no packages."
				>
					<span class="icon-[mdi--package-up] h-4 w-4" aria-hidden="true"></span>Preview all
					upgrades
				</DropdownMenu.Item>
				{#if selected.length > 0}<DropdownMenu.Item
						onSelect={() =>
							void updates.run('packages-preview', { packages: selected.map((pkg) => pkg!.name) })}
						disabled={!enabled || !selectionEligible}
						class="cursor-pointer"
						title={selectionError ||
							'Preview selected packages. Downloads and installs no packages.'}
						aria-describedby={selectionError ? 'host-package-selection-reason' : undefined}
					>
						<span class="icon-[mdi--package-up] h-4 w-4" aria-hidden="true"></span>Preview selected
						({selected.length})
					</DropdownMenu.Item>
					{#if selectionError}<p
							id="host-package-selection-reason"
							class="text-muted-foreground px-2 pb-1.5 text-xs leading-relaxed"
						>
							{selectionError}
						</p>{/if}{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
		{#if single}<Button
				onclick={() => {
					details = { ...single, available: availableVersion(single) };
					detailsOpen = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
				><SpanWithIcon icon="icon-[mdi--eye]" size="h-4 w-4" gap="gap-2" title="Details" /></Button
			>{/if}
		<div class="ml-auto flex items-center gap-2">
			<Controls
				><Combobox
					bind:open={filterOpen}
					bind:value={filters}
					data={[
						{ value: 'vulnerable', label: 'Vulnerable only' },
						{ value: 'updates', label: 'Updates available' }
					]}
					placeholder="Filter"
					multiple={true}
					showCount={true}
					showCountLabel="Filtered"
					triggerWidth="w-36"
					buttonClass="h-6.5"
					width="w-52"
					classes=""
				/></Controls
			>
		</div>
	</div>
	<div class="flex h-full flex-col overflow-hidden">
		{#key updates.node}<TreeTable
				data={tableData}
				name="tt-host-packages"
				bind:parentActiveRow={activeRows}
				bind:query
				dataTree={false}
				initialSort={[{ column: 'name', dir: 'asc' }]}
				customPlaceholder={updates.error || 'No installed packages'}
			/>{/key}
	</div>
</div>

{#key updates.node}{#if detailsOpen && details}<Details
			bind:open={detailsOpen}
			pkg={details}
			eligible={enabled &&
				!details.locked &&
				['application', 'package-manager'].includes(details.kind)}
			auditStale={view?.auditStale || !!updates.error}
			onUpgrade={() => updates.run('packages-preview', { packages: [details!.name] })}
		/>{/if}{/key}
