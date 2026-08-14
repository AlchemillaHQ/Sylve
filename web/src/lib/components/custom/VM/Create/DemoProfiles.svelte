<script lang="ts">
	import { demoVMProfiles, type DemoVMProfile } from '$lib/demo/vm-profiles';
	import { formatBytesBinary } from '$lib/utils/bytes';

	interface Props {
		value: string;
		onSelect: (profile: DemoVMProfile) => void;
	}

	let { value = $bindable(), onSelect }: Props = $props();
</script>

<div class="grid grid-cols-1 gap-3 md:grid-cols-3">
	{#each demoVMProfiles as profile (profile.id)}
		<button
			type="button"
			class="group rounded-lg border p-4 text-left transition-colors"
			class:border-foreground={value === profile.id}
			class:bg-muted={value === profile.id}
			class:border-border={value !== profile.id}
			onclick={() => {
				value = profile.id;
				onSelect(profile);
			}}
		>
			<div class="flex items-start justify-between gap-3">
				<div>
					<p class="text-sm font-medium">{profile.label}</p>
					<p class="text-muted-foreground mt-0.5 text-xs">{profile.release}</p>
				</div>
				<span
					class="mt-0.5 h-3 w-3 shrink-0 rounded-full border"
					class:border-foreground={value === profile.id}
					class:bg-foreground={value === profile.id}
				></span>
			</div>
			<p class="text-muted-foreground mt-4 min-h-12 text-xs leading-5">{profile.description}</p>
			<div class="text-muted-foreground mt-4 flex items-center gap-2 border-t pt-3 text-[11px]">
				<span>{profile.architecture}</span>
				<span aria-hidden="true">·</span>
				<span>{formatBytesBinary(profile.memoryBytes)} RAM</span>
				<span aria-hidden="true">·</span>
				<span>{formatBytesBinary(profile.diskBytes)}</span>
			</div>
		</button>
	{/each}
</div>
