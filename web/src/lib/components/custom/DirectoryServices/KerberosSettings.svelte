<script lang="ts">
	import { updateKerberosSettings } from '$lib/api/directory-services';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { KerberosSettings } from '$lib/types/directory-services';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { onDestroy, untrack } from 'svelte';

	let {
		open = $bindable(),
		settings,
		node,
		reload = $bindable(false)
	}: { open: boolean; node: string; settings: KerberosSettings; reload?: boolean } = $props();
	const targetNode = untrack(() => node);
	let mounted = true;
	onDestroy(() => {
		mounted = false;
	});
	let form = $state(untrack(() => ({ ...settings })));
	let saving = $state(false);
	let content = $state<HTMLDivElement | null>(null);
	async function save() {
		if (saving) return;
		saving = true;
		try {
			const result = await updateKerberosSettings(form, { hostname: targetNode });
			if (!mounted) return;
			if (result.status === 'error') {
				handleAPIError(result);
				return;
			}
			toast.success('Kerberos settings updated', { position: 'bottom-center' });
			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		bind:ref={content}
		class="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onOpenAutoFocus={(event) => {
			const textarea = content?.querySelector('textarea');
			if (!textarea) return;
			event.preventDefault();
			textarea.focus({ preventScroll: true });
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		onReset={() => (form = { ...settings })}
		onClose={() => (open = false)}
	>
		<Dialog.Header
			><Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--cog-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Update Kerberos Settings"
				/></Dialog.Title
			></Dialog.Header
		>
		<CustomValueInput
			label="Application Defaults"
			placeholder={'pam = {\n  forwardable = true\n}'}
			type="textarea"
			bind:value={form.appDefaults}
			classes="space-y-1.5"
			textAreaClasses="h-36 font-mono text-xs"
			disabled={saving}
			hint="Defaults for Kerberos applications. Application and realm blocks are supported; omit the [appdefaults] header."
		/>
		<CustomValueInput
			label="Library Defaults"
			placeholder="ticket_lifetime = 24h&#10;renew_lifetime = 7d"
			type="textarea"
			bind:value={form.libDefaults}
			classes="space-y-1.5"
			textAreaClasses="h-36 font-mono text-xs"
			disabled={saving}
			hint="One supported key = value setting per line. Omit the [libdefaults] header."
		/>
		<p class="text-muted-foreground text-xs">
			Advanced defaults override generated values. Existing tickets are not reissued by saving these
			settings.
		</p>
		<Dialog.Footer class="flex justify-end"
			><Button size="sm" disabled={saving} onclick={save}
				>{#if saving}<span class="icon-[mdi--loading] h-4 w-4 animate-spin"
					></span>Saving...{:else}Save{/if}</Button
			></Dialog.Footer
		>
	</Dialog.Content>
</Dialog.Root>
