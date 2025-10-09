<script lang="ts">
	import { doesPathHaveBase, getFiles } from '$lib/api/system/file-explorer';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import type { Jail } from '$lib/types/jail/jail';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Dataset } from '$lib/types/zfs/dataset';

	interface Props {
		filesystems: Dataset[];
		downloads: Download[];
		dataset: string;
		base: string;
		jails: Jail[];
	}

	let {
		filesystems,
		downloads,
		dataset = $bindable(),
		base = $bindable(),
		jails
	}: Props = $props();

	let datasetOptions = $derived.by(() => {
		const usable = [] as { label: string; value: string }[];
		const used = jails.map((jail) => jail.dataset);

		for (const filesystem of filesystems) {
			const mountpoint = filesystem.mountpoint || '';
			if (mountpoint === '/' || mountpoint === 'none') {
				continue;
			}

			if (used.includes(filesystem.guid)) {
				continue;
			}

			if (filesystem.name.includes('/') && filesystem.used < 1024 * 1024) {
				getFiles(mountpoint).then((files) => {
					if (files.length === 0) {
						usable.push({ label: filesystem.name, value: filesystem.guid || '' });
					}
				});
				continue;
			}

			if (mountpoint && filesystem.used > 1024 * 1024 * 256) {
				doesPathHaveBase(mountpoint)
					.then((hasBase) => {
						if (hasBase) {
							usable.push({ label: filesystem.name, value: filesystem.guid || '' });
						}
					})
					.catch(() => {
						console.error('Error checking path base for', mountpoint);
					});
			}
		}

		return usable;
	});

	let baseOptions = $derived.by(() => {
		return downloads
			.filter((download) => download.name.includes('txz'))
			.map((download) => ({
				label: download.name,
				value: download.uuid
			}));
	});

	let comboBoxes = $state({
		dataset: {
			open: false,
			options: [] as { label: string; value: string }[]
		},
		base: {
			open: false,
			options: [] as { label: string; value: string }[]
		}
	});

	let disableBaseSelection = $state(false);

	$effect(() => {
		if (dataset) {
			const mountpoint = filesystems.find((fs) => fs.guid === dataset)?.mountpoint || '';
			doesPathHaveBase(mountpoint).then((hasBase) => {
				if (hasBase) {
					disableBaseSelection = true;
					base = '';
				} else {
					disableBaseSelection = false;
				}
			});
		}
	});
</script>

<div class="flex flex-col gap-4 p-4">
	<CustomComboBox
		bind:open={comboBoxes.dataset.open}
		label="Filesystem"
		bind:value={dataset}
		data={datasetOptions}
		classes="flex-1 space-y-1"
		placeholder="Select filesystem"
		triggerWidth="w-full "
		width="w-full"
	></CustomComboBox>

	<CustomComboBox
		bind:open={comboBoxes.base.open}
		label="Base"
		bind:value={base}
		data={baseOptions}
		classes="flex-1 space-y-1"
		placeholder="Select base"
		triggerWidth="w-full"
		width="w-full"
		disabled={disableBaseSelection}
	></CustomComboBox>
</div>
