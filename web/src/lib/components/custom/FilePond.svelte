<script lang="ts">
	import { onDestroy } from 'svelte';
	import * as FilePond from 'filepond';

	export const registerPlugin = FilePond.registerPlugin;
	export const isSupported = FilePond.supported();

	let root: HTMLInputElement | null = null;
	let instance: FilePond.FilePond | undefined = undefined;

	let {
		class: klass = undefined,
		id = undefined,
		name = undefined,
		allowMultiple = undefined,
		required = undefined,
		captureMethod = undefined,
		acceptedFileTypes = undefined,
		addFile = $bindable(() => {}),
		addFiles = $bindable(() => {}),
		browse = $bindable(() => {}),
		getFile = $bindable(() => {}),
		getFiles = $bindable(() => {}),
		moveFile = $bindable(() => {}),
		prepareFile = $bindable(() => {}),
		prepareFiles = $bindable(() => {}),
		processFile = $bindable(() => {}),
		processFiles = $bindable(() => {}),
		removeFile = $bindable(() => {}),
		removeFiles = $bindable(() => {}),
		sort = $bindable(() => {}),
		...options
	} = $props();

	$effect(() => {
		if (!isSupported || !root) return;

		if (!instance) {
			instance = FilePond.create(root, { ...options });
			addFile = instance.addFile;
			addFiles = instance.addFiles;
			removeFile = instance.removeFile;
			removeFiles = instance.removeFiles;
			browse = instance.browse;
			getFile = instance.getFile;
			getFiles = instance.getFiles;
			moveFile = instance.moveFile;
			prepareFile = instance.prepareFile;
			prepareFiles = instance.prepareFiles;
			processFile = instance.processFile;
			processFiles = instance.processFiles;
			sort = instance.sort;
		} else {
			instance.setOptions(options);
		}
	});

	onDestroy(() => {
		if (!instance) return;
		instance.destroy();
		instance = undefined;
	});
</script>

<div class="filepond--wrapper">
	<input
		type="file"
		bind:this={root}
		{id}
		{name}
		class={klass}
		accept={acceptedFileTypes}
		multiple={allowMultiple}
		{required}
		capture={captureMethod}
	/>
</div>
