<script lang="ts">
	import Controls from '$lib/components/custom/System/Updates/Controls.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { safeAdvisoryURL } from '$lib/types/jail/packages';
	import type { HostUpdateContext } from '$lib/types/system/updates';
	import { watch } from 'runed';
	import { getContext } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';

	const updates = getContext<HostUpdateContext>('hostUpdates');
	const view = $derived(updates.view);
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	watch(
		() => updates.node,
		() => {
			activeRows = null;
			query = '';
		}
	);
	watch(
		() => query,
		() => {
			activeRows = null;
		}
	);
	function formatCVEs(cell: CellComponent): HTMLElement {
		const value = cell.getValue() as string;
		const count = cell.getRow().getData().cveCount;
		const element = document.createElement('span');
		element.className = 'block whitespace-normal';
		element.textContent = value || '—';
		if (count > 3) {
			element.className =
				'inline-flex cursor-help items-center rounded border border-muted-foreground/50 px-1.5 leading-tight';
			element.textContent = `${count} CVEs`;
			element.title = value;
		}
		return element;
	}
	const columns: Column[] = [
		{ field: 'name', title: 'Package', formatter: 'plaintext', minWidth: 110, widthGrow: 2 },
		{ field: 'version', title: 'Version', formatter: 'plaintext', minWidth: 90, widthGrow: 1 },
		{ field: 'description', title: 'Advisory', formatter: 'textarea', minWidth: 200, widthGrow: 4 },
		{ field: 'cves', title: 'CVE', formatter: formatCVEs, minWidth: 160, widthGrow: 3 },
		{ field: 'advisoryId', title: 'Advisory ID', formatter: 'plaintext', visible: false }
	];
	const tableData = $derived({
		columns,
		rows: (view?.audit.packages ?? []).flatMap((pkg) =>
			pkg.advisories.map((advisory) => ({
				...advisory,
				id: JSON.stringify([pkg.name, pkg.version, advisory.id]),
				advisoryId: advisory.id,
				description: advisory.description || advisory.id,
				name: pkg.name,
				version: pkg.version,
				cveCount: advisory.cves.length,
				cves: advisory.cves.join(', ')
			}))
		)
	});
	const selected = $derived(
		activeRows?.length === 1
			? tableData.rows.find((row) => row.id === activeRows?.[0]?.id)
			: undefined
	);
	const advisoryURL = $derived(selected ? safeAdvisoryURL(selected.url) : null);
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => updates.run('audit')}
			size="sm"
			class="h-6"
			disabled={!updates.enabled || !view?.ready}
		>
			<SpanWithIcon icon="icon-[mdi--shield-search]" size="h-4 w-4" gap="gap-2" title="Audit" />
		</Button>
		{#if selected}
			<Button
				href={advisoryURL ?? undefined}
				target="_blank"
				rel="noopener noreferrer"
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!advisoryURL}
				title="Open selected advisory in a new tab"
			>
				<SpanWithIcon
					icon="icon-[mdi--open-in-new]"
					size="h-4 w-4"
					gap="gap-2"
					title="Open Advisory"
				/>
			</Button>
		{/if}
		<div class="ml-auto flex items-center gap-2"><Controls /></div>
	</div>
	<div class="flex h-full flex-col overflow-hidden">
		{#key updates.node}<TreeTable
				data={tableData}
				name="tt-host-package-audit"
				bind:parentActiveRow={activeRows}
				bind:query
				multipleSelect={false}
				layout="fitColumns"
				dataTree={false}
				initialSort={[{ column: 'name', dir: 'asc' }]}
				customPlaceholder={!view || view.auditStale || updates.error
					? 'No current audit results'
					: 'No known vulnerabilities found'}
			/>{/key}
	</div>
</div>

<style>
	:global(#tt-host-package-audit .tabulator-cell[tabulator-field='description']),
	:global(#tt-host-package-audit .tabulator-cell[tabulator-field='cves']) {
		overflow-wrap: anywhere;
	}
</style>
