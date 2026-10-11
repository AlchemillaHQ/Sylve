<script lang="ts">
	import { getKerberosSettings } from '$lib/api/directory-services';
	import Editor from '$lib/components/custom/DirectoryServices/KerberosSettings.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { KerberosSettings } from '$lib/types/directory-services';
	import { watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';

	let { data }: { data: { node: string; settings: KerberosSettings | null; loadFailed: boolean } } =
		$props();
	const initial = untrack(() => data);
	let settings = $state(initial.settings);
	let loadFailed = $state(initial.loadFailed);
	let query = $state('');
	let editorOpen = $state(false);
	let reload = $state(false);
	let refreshing = $state(false);
	let generation = 0;
	onDestroy(() => {
		generation++;
	});
	watch.pre(
		() => data,
		(next) => {
			generation++;
			refreshing = false;
			reload = false;
			settings = next.settings;
			loadFailed = next.loadFailed;
			editorOpen = false;
			query = '';
		}
	);
	async function refresh() {
		if (refreshing) return;
		refreshing = true;
		const node = data.node;
		const requestGeneration = generation;
		try {
			const result = await getKerberosSettings({ hostname: node });
			if (generation !== requestGeneration) return;
			settings = result;
			loadFailed = false;
		} catch {
			if (generation === requestGeneration) loadFailed = true;
		} finally {
			if (generation === requestGeneration) refreshing = false;
		}
	}
	watch(
		() => reload,
		(value) => {
			if (value) {
				reload = false;
				void refresh();
			}
		}
	);
	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{ title: 'Value', field: 'value' }
		],
		rows: settings
			? [
					{
						id: 'app',
						property: 'Application Defaults',
						value: settings.appDefaults ? 'Configured' : 'Generated Defaults'
					},
					{
						id: 'lib',
						property: 'Library Defaults',
						value: settings.libDefaults ? 'Configured' : 'Generated Defaults'
					}
				]
			: []
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query /><Button
			size="sm"
			class="h-6"
			disabled={!settings || loadFailed || refreshing}
			onclick={() => (editorOpen = true)}
			><SpanWithIcon
				icon="icon-[hugeicons--system-update-01]"
				size="h-4 w-4"
				gap="gap-2"
				title="Update"
			/></Button
		>{#if loadFailed}<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={refreshing}
				onclick={refresh}>Retry</Button
			>{/if}
	</div>
	{#if loadFailed}<p class="text-destructive px-3 py-2 text-xs" role="alert">
			Unable to load current Kerberos settings. Changes are disabled until refresh succeeds.
		</p>{/if}
	<TreeTable
		data={table}
		name="kerberos-settings-tt"
		multipleSelect={false}
		borderTop={loadFailed}
		bind:query
	/>
</div>
{#if settings && editorOpen}<Editor
		bind:open={editorOpen}
		node={data.node}
		{settings}
		bind:reload
	/>{/if}
