<script lang="ts">
	import { saveKerberosRealm, discoverKerberosRealm } from '$lib/api/directory-services';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { KerberosRealm } from '$lib/types/directory-services';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { onDestroy, untrack } from 'svelte';

	let {
		open = $bindable(),
		node,
		realm = null,
		reload = $bindable(false)
	}: { open: boolean; node: string; realm?: KerberosRealm | null; reload?: boolean } = $props();
	const targetNode = untrack(() => node);
	let mounted = true;
	onDestroy(() => {
		mounted = false;
	});
	function initialForm() {
		return {
			realm: realm?.realm ?? '',
			kdc: realm?.kdc.join('\n') ?? '',
			adminServer: realm?.adminServer.join('\n') ?? '',
			kpasswdServer: realm?.kpasswdServer.join('\n') ?? ''
		};
	}
	let form = $state(initialForm());
	let saving = $state(false);
	let discovering = $state(false);
	async function discover() {
		if (!form.realm.trim() || discovering) return;
		discovering = true;
		try {
			const result = await discoverKerberosRealm(form.realm.trim(), { hostname: targetNode });
			if (!mounted) return;
			form.kdc = result.kdc.join('\n');
			form.adminServer = result.adminServer.join('\n');
			form.kpasswdServer = result.kpasswdServer.join('\n');
		} catch {
			if (!mounted) return;
			toast.error('Unable to discover Kerberos servers. Check the realm and node DNS.', {
				position: 'bottom-center'
			});
		} finally {
			discovering = false;
		}
	}
	const lines = (text: string) =>
		text
			.split('\n')
			.map((line) => line.trim())
			.filter(Boolean);
	async function save() {
		if (saving || discovering) return;
		if (!form.realm.trim()) {
			toast.error('Realm name is required', { position: 'bottom-center' });
			return;
		}
		saving = true;
		try {
			const result = await saveKerberosRealm(
				realm?.id ?? null,
				{
					realm: form.realm.trim(),
					kdc: lines(form.kdc),
					adminServer: lines(form.adminServer),
					kpasswdServer: lines(form.kpasswdServer)
				},
				{ hostname: targetNode }
			);
			if (!mounted) return;
			if (result.status === 'error') {
				handleAPIError(result);
				return;
			}
			toast.success(`Kerberos realm ${realm ? 'updated' : 'created'}`, {
				position: 'bottom-center'
			});
			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[90vh] overflow-y-auto sm:max-w-3xl"
		showCloseButton={!saving}
		showResetButton={realm !== null && !saving && !discovering}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		onReset={() => (form = initialForm())}
		onClose={() => (open = false)}
	>
		<Dialog.Header class="-mb-2">
			<Dialog.Title
				><SpanWithIcon
					icon="icon-[mdi--earth]"
					size="h-5 w-5"
					gap="gap-2"
					title={realm ? `Edit Kerberos Realm - ${realm.realm}` : 'Create Kerberos Realm'}
				/></Dialog.Title
			>
			<Dialog.Description class="text-muted-foreground text-xs"
				>Leave servers empty to use DNS discovery. Enter one server per line.</Dialog.Description
			>
		</Dialog.Header>
		<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
			<CustomValueInput
				label="Realm"
				disabled={saving || discovering}
				placeholder="EXAMPLE.COM"
				bind:value={form.realm}
				classes="space-y-1.5 md:col-span-3"
			/>
			<CustomValueInput
				label="KDC Servers"
				disabled={saving || discovering}
				placeholder="dc1.example.com"
				type="textarea"
				bind:value={form.kdc}
				classes="space-y-1.5"
				textAreaClasses="h-24 font-mono text-xs"
			/>
			<CustomValueInput
				label="Administration Servers"
				disabled={saving || discovering}
				placeholder="Optional"
				type="textarea"
				bind:value={form.adminServer}
				classes="space-y-1.5"
				textAreaClasses="h-24 font-mono text-xs"
			/>
			<CustomValueInput
				label="Password Servers"
				disabled={saving || discovering}
				placeholder="Optional"
				type="textarea"
				bind:value={form.kpasswdServer}
				classes="space-y-1.5"
				textAreaClasses="h-24 font-mono text-xs"
			/>
		</div>
		<Button
			size="sm"
			variant="outline"
			disabled={saving || discovering || !form.realm.trim()}
			onclick={discover}
			><span class={`icon-[mdi--dns-outline] size-4 ${discovering ? 'animate-pulse' : ''}`}
			></span>{discovering ? 'Discovering...' : 'Discover Servers'}</Button
		>
		<Dialog.Footer class="flex justify-end"
			><Button size="sm" disabled={saving || discovering} onclick={save}
				>{#if saving}<span class="icon-[mdi--loading] h-4 w-4 animate-spin"
					></span>Saving...{:else}{realm ? 'Save Changes' : 'Create Realm'}{/if}</Button
			></Dialog.Footer
		>
	</Dialog.Content>
</Dialog.Root>
