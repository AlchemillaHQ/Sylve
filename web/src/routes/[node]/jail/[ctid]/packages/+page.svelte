<script lang="ts">
	import { page } from '$app/state';
	import { getJailPackages, startPackageOperation } from '$lib/api/jail/packages';
	import Details from '$lib/components/custom/Jail/Packages/Details.svelte';
	import Monitoring from '$lib/components/custom/Jail/Packages/Monitoring.svelte';
	import Operation from '$lib/components/custom/Jail/Packages/Operation.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import Combobox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type {
		JailPackage,
		PackageAction,
		PackageRequest,
		PackageView
	} from '$lib/types/jail/packages';
	import { packageKindLabel, packageReason } from '$lib/types/jail/packages';
	import { isAPIResponse } from '$lib/utils/http';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import { getContext, onMount, untrack } from 'svelte';
	import type { PageData } from './$types';
	import type { CellComponent } from 'tabulator-tables';
	import { toast } from 'svelte-sonner';

	let { data }: { data: PageData } = $props();
	const packageOperation = getContext<{ open?: () => boolean }>('jailPackageOperation');
	let requestError = $state('');
	const baseIdentity = $derived(`${data.node}:${data.ctId}:${data.jail.id}`);
	const inventory = resource(
		[() => data.node, () => data.ctId, () => data.jail.id],
		async (
			[node, ctId, jailId],
			_,
			{ signal, refetching, data: previous }
		): Promise<{ identity: string; view: PackageView | null }> => {
			const target = `${node}:${ctId}:${jailId}`;
			if (!refetching) {
				requestError = data.packagesError ? packageReason(data.packagesError.message) : '';
				return { identity: target, view: data.packages };
			}
			const result = await getJailPackages(ctId, { hostname: node, signal });
			signal.throwIfAborted();
			if (isAPIResponse(result)) {
				requestError = packageReason(result.message, 'Package inventory is unavailable.');
				return {
					identity: target,
					view: previous?.identity === target ? previous.view : data.packages
				};
			}
			requestError = '';
			return { identity: target, view: result };
		},
		{ initialValue: untrack(() => ({ identity: baseIdentity, view: data.packages })) }
	);
	const view = $derived(
		inventory.current.identity === baseIdentity ? inventory.current.view : data.packages
	);
	const identity = $derived(`${baseIdentity}:${view?.guid ?? ''}`);
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let submitting = $state(false);
	let updatesOpen = $state(false);
	let filterOpen = $state(false);
	let filters = $state<string[]>([]);
	const filterOptions = [
		{ value: 'vulnerable', label: 'Vulnerable only' },
		{ value: 'updates', label: 'Updates available' }
	];
	const vulnerableOnly = $derived(filters.includes('vulnerable'));
	const updatesOnly = $derived(filters.includes('updates'));
	watch(
		() => `${vulnerableOnly}:${updatesOnly}`,
		() => {
			activeRows = null;
		}
	);
	let details = $state<JailPackage | null>(null);
	let detailsOpen = $state(false);
	let monitoringOpen = $state(false);
	let operationOpen = $state(false);
	let taskId = $state(0);
	const busy = $derived(submitting || !!view?.activeJob);
	const enabled = $derived(!!view?.ready && !requestError && !busy);
	const selected = $derived(
		(activeRows ?? []).map((row) => view?.packages.find((pkg) => pkg.name === row.id))
	);
	const single = $derived(selected.length === 1 ? selected[0] : null);
	const selectionError = $derived(
		selected.some((pkg) => pkg?.kind === 'base')
			? 'Base packages must be updated separately under Updates → Jail system'
			: selected.some((pkg) => !pkg || pkg.locked || pkg.protected)
				? 'Locked/protected or missing packages cannot be upgraded'
				: selected.some(
							(pkg) =>
								(vulnerableOnly && !pkg?.advisories.length) ||
								(updatesOnly && (!pkg || !availableVersion(pkg)))
					  )
					? 'Selection includes filtered-out packages'
					: ''
	);
	const selectionEligible = $derived(selected.length > 0 && !selectionError);
	const timestamp = (value: string | null | undefined) => (value ? convertDbTime(value) : 'Never');
	const requestedTaskId = $derived(
		page.url.searchParams.get('job') === 'active'
			? (view?.activeJob?.taskId ?? 0)
			: Number(page.url.searchParams.get('job'))
	);

	watch(
		() => identity,
		() => {
			activeRows = null;
			details = null;
			detailsOpen = false;
			monitoringOpen = false;
			operationOpen = false;
			taskId = 0;
			filters = [];
			updatesOpen = false;
			filterOpen = false;
			submitting = false;
		}
	);
	watch(
		() => requestedTaskId,
		(id) => {
			if (Number.isSafeInteger(id) && id > 0) {
				taskId = id;
				operationOpen = true;
			}
		},
		{ lazy: false }
	);
	onMount(() => {
		packageOperation.open = () => {
			if (!view?.activeJob) return false;
			taskId = view.activeJob.taskId;
			operationOpen = true;
			return true;
		};
		if (data.packagesError)
			toast.error(packageReason(data.packagesError.message), { position: 'bottom-center' });
		return () => {
			packageOperation.open = undefined;
		};
	});
	useInterval(3000, {
		callback: () => {
			if (
				view?.activeJob &&
				!operationOpen &&
				!inventory.loading &&
				document.visibilityState === 'visible'
			)
				void inventory.refetch();
		}
	});

	async function refreshInventory(): Promise<PackageView | null> {
		const result = await inventory.refetch();
		return requestError ? null : (result?.view ?? null);
	}

	async function run(action: PackageAction, request: PackageRequest = {}) {
		if (!enabled) return;
		updatesOpen = false;
		const node = data.node,
			ctId = data.ctId,
			target = identity;
		submitting = true;
		try {
			const result = await startPackageOperation(ctId, action, request, { hostname: node });
			if (target !== identity) return;
			if (isAPIResponse(result)) {
				toast.error(packageReason(result.message), {
					position: 'bottom-center'
				});
				return;
			}
			taskId = result.taskId;
			operationOpen = true;
			await inventory.refetch();
		} finally {
			if (target === identity) submitting = false;
		}
	}
	function previewSelection() {
		if (selectionEligible)
			void run('upgrade/preview', { packages: selected.map((pkg) => pkg!.name) });
	}
	function availableVersion(pkg: JailPackage): string {
		const checked =
			pkg.kind === 'base'
				? view?.systemUpdatesChecked
				: pkg.kind === 'application' && view?.updatesChecked;
		return checked ? pkg.available : '';
	}
	function formatStatus(cell: CellComponent) {
		const pkg = cell.getRow().getData();
		const flags: string[] = [];
		if (pkg.advisoryCount > 0) flags.push(`${pkg.advisoryCount} advisories`);
		if (pkg.availableVersion) flags.push('Update available');
		if (pkg.locked) flags.push('Locked');
		if (pkg.kind !== 'application') flags.push(packageKindLabel(pkg.kind));
		else if (pkg.protected) flags.push('Protected package');
		if (flags.length === 0)
			flags.push(pkg.auditStale ? 'Not audited / stale' : 'No known vulnerabilities');
		return renderWithIcon(
			pkg.advisoryCount > 0
				? 'mdi:alert-outline'
				: pkg.kind === 'base' && !pkg.locked
					? 'mdi:layers-outline'
					: pkg.locked || pkg.protected
						? 'mdi:lock-outline'
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
				available:
					pkg.kind === 'base'
						? view?.systemUpdatesChecked
							? pkg.available || '-'
							: 'Not checked'
						: pkg.kind === 'application'
							? view?.updatesChecked
								? pkg.available || '-'
								: 'Not checked'
							: '—',
				advisoryCount: pkg.advisories.length,
				auditStale: view?.auditStale || !!requestError,
				status: ''
			}))
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<DropdownMenu.Root bind:open={updatesOpen}>
			<DropdownMenu.Trigger disabled={!enabled}>
				{#snippet child({ props })}
					<Button {...props} size="sm" class="h-6" title="Package update actions">
						<SpanWithIcon icon="icon-[mdi--update]" size="h-4 w-4" gap="gap-2" title="Updates" />
						<span class="icon-[mdi--chevron-down] h-4 w-4" aria-hidden="true"></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="start" class="w-64">
				<DropdownMenu.Group>
					<DropdownMenu.GroupHeading>Applications</DropdownMenu.GroupHeading>
					<DropdownMenu.Item
						onSelect={() => void run('check-updates')}
						disabled={!enabled}
						class="cursor-pointer"
						title={`Check availability without downloading package archives. Last successful check: ${timestamp(view?.checkedAt)}`}
					>
						<span class="icon-[mdi--update] h-4 w-4" aria-hidden="true"></span>
						Check for updates
					</DropdownMenu.Item>
					<DropdownMenu.Item
						onSelect={() => void run('upgrade/preview', { all: true })}
						disabled={!enabled}
						class="cursor-pointer"
						title="Preview all eligible application packages, including those hidden by filters. May download archives; installs nothing."
					>
						<span class="icon-[mdi--package-up] h-4 w-4" aria-hidden="true"></span>
						Preview all upgrades
					</DropdownMenu.Item>
					{#if selected.length > 0}
						<DropdownMenu.Item
							onSelect={previewSelection}
							disabled={!enabled || !selectionEligible}
							class="cursor-pointer"
							title={selectionError ||
								'Preview selected application packages. May download archives; installs nothing.'}
							aria-describedby={selectionError ? 'package-selection-reason' : undefined}
						>
							<span class="icon-[mdi--package-up] h-4 w-4" aria-hidden="true"></span>
							Preview selected ({selected.length})
						</DropdownMenu.Item>
						{#if selectionError}
							<p
								id="package-selection-reason"
								class="text-muted-foreground px-2 pb-1.5 text-xs leading-relaxed"
							>
								{selectionError}
							</p>
						{/if}
					{/if}
				</DropdownMenu.Group>
				{#if view?.pkgbase}
					<DropdownMenu.Separator />
					<DropdownMenu.Group>
						<DropdownMenu.GroupHeading>Jail system</DropdownMenu.GroupHeading>
						<DropdownMenu.Item
							onSelect={() => void run('upgrade/preview', { all: true, scope: 'system' })}
							disabled={!enabled}
							class="cursor-pointer"
							title={`Download and verify a same-release FreeBSD base preview without installing packages. Last check: ${timestamp(view.systemCheckedAt)}`}
						>
							<span class="icon-[mdi--layers-outline] h-4 w-4" aria-hidden="true"></span>
							Preview system upgrade
						</DropdownMenu.Item>
					</DropdownMenu.Group>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
		<Button
			onclick={() => run('audit')}
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={!enabled}
			title={`Last successful audit: ${timestamp(view?.auditAt)}`}
			><SpanWithIcon
				icon="icon-[mdi--shield-search]"
				size="h-4 w-4"
				gap="gap-2"
				title="Audit"
			/></Button
		>
		{#if single}<Button
				onclick={() => {
					details = single ? { ...single, available: availableVersion(single) } : null;
					detailsOpen = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
				><SpanWithIcon icon="icon-[mdi--eye]" size="h-4 w-4" gap="gap-2" title="Details" /></Button
			>{/if}
		<div class="ml-auto flex items-center gap-2">
			<Combobox
				bind:open={filterOpen}
				bind:value={filters}
				data={filterOptions}
				placeholder="Filter"
				multiple={true}
				showCount={true}
				showCountLabel="Filtered"
				triggerWidth="w-36"
				buttonClass="h-6.5"
				width="w-52"
				classes=""
			/>
			<Button
				onclick={() => (monitoringOpen = true)}
				size="sm"
				variant="outline"
				class="h-6"
				title="Package monitoring"
				aria-label="Package monitoring"
				disabled={!view}><span class="icon-[mdi--cog] h-4 w-4"></span></Button
			>
			<Button
				onclick={() => inventory.refetch()}
				size="sm"
				variant="outline"
				class="h-6"
				title="Refresh inventory"
				aria-label="Refresh inventory"
				disabled={inventory.loading}
				><span
					class={inventory.loading
						? 'icon-[mdi--loading] h-4 w-4 animate-spin'
						: 'icon-[mdi--refresh] h-4 w-4'}
				></span></Button
			>
		</div>
	</div>
	<div class="flex h-full flex-col overflow-hidden">
		{#key identity}
			<TreeTable
				data={tableData}
				name="tt-jail-packages"
				bind:parentActiveRow={activeRows}
				bind:query
				dataTree={false}
				initialSort={[{ column: 'name', dir: 'asc' }]}
				customPlaceholder={requestError ||
					(view?.reason ? packageReason(view.reason) : 'No installed packages')}
			/>
		{/key}
	</div>
</div>

{#key identity}
	{#if detailsOpen && details}<Details
			bind:open={detailsOpen}
			node={data.node}
			ctId={data.ctId}
			pkg={details}
			eligible={enabled && !details.locked && !details.protected}
			auditStale={view?.auditStale ?? true}
			onUpgrade={() => run('upgrade/preview', { packages: [details!.name] })}
		/>{/if}
	{#if monitoringOpen && view}<Monitoring
			bind:open={monitoringOpen}
			node={data.node}
			ctId={data.ctId}
			monitoring={view.monitoring}
			afterChange={() => inventory.refetch()}
		/>{/if}
	{#key taskId}{#if operationOpen && taskId > 0}<Operation
				bind:open={operationOpen}
				node={data.node}
				ctId={data.ctId}
				{taskId}
				afterChange={refreshInventory}
			/>{/if}{/key}
{/key}
