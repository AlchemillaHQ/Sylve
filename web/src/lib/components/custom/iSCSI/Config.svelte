<!-- SPDX-License-Identifier: BSD-2-Clause -->

<script lang="ts">
	import { getISCSIConfig, updateISCSIConfig } from '$lib/api/iscsi/config';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import {
		ISCSIConfigSchema,
		ISCSIConfigValidationSchema,
		type ISCSIConfig,
		type ISCSIConfigValidation
	} from '$lib/types/iscsi/config';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { isAPIResponse } from '$lib/utils/http';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { node, config }: { node: string; config: ISCSIConfig } = $props();
	const maxBytes = 256 * 1024;
	let current = $state(untrack(() => config));
	let editor = $state(untrack(() => config.extraTargetConfig));
	let busy = $state(false);
	let validation = $state<ISCSIConfigValidation | null>(null);
	let byteCount = $derived(new TextEncoder().encode(editor).length);

	function reasonMessage(code: string | null): string {
		switch (code) {
			case 'extra_config_too_large':
			case 'iscsi_request_too_large':
				return 'The extra configuration exceeds the size limit.';
			case 'extra_config_listener_overlap':
				return 'Listeners overlap. Use separate addresses or ports.';
			case 'extra_config_invalid_listener':
				return 'Use a literal IP address and a valid port. Link-local IPv6 needs an interface name.';
			case 'extra_config_reserved_group':
			case 'extra_config_reserved_option':
			case 'extra_config_target_name_conflict':
				return 'The name is reserved or conflicts with a managed object.';
			case 'extra_config_explicit_group_required':
			case 'extra_config_unknown_listener_group':
			case 'extra_config_unknown_auth_group':
			case 'extra_config_unknown_lun':
				return 'Define each referenced user group or LUN first. Each target or controller needs an explicit listener group.';
			case 'extra_config_invalid_backing':
				return 'Check the backing path, backend, size, and blocksize.';
			case 'extra_config_native_field_too_long':
				return 'Use at most 16 bytes for a serial number and 64 bytes for a device ID.';
			case 'extra_config_unobservable_value':
				return 'The name, path, or option cannot be checked in the native inventory.';
			case 'extra_config_native_validation_failed':
				return 'The native validator rejected the merged configuration. Check the supported syntax and values.';
			case 'iscsi_listener_normalization_requires_stop':
				return 'Stop the target service before changing the spelling of an existing listener.';
			case 'invalid_managed_target_configuration':
				return 'Repair the managed target configuration, then retry.';
			case 'target_runtime_checks_pending':
				return 'Check the target service and backing storage, then save again to retry.';
			default:
				return 'Check the supported syntax and configuration, then retry.';
		}
	}

	async function refresh() {
		if (busy) return;
		busy = true;
		try {
			const result = await getISCSIConfig({ hostname: node });
			if (isAPIResponse(result)) {
				toast.error('Cannot read the iSCSI configuration status', { position: 'bottom-center' });
				return;
			}
			current = result;
		} finally {
			busy = false;
		}
	}

	async function save() {
		if (busy || byteCount > maxBytes) return;
		busy = true;
		validation = null;
		try {
			const response = await updateISCSIConfig(editor, { hostname: node });
			if (response.status !== 'success') {
				const detail = ISCSIConfigValidationSchema.safeParse(response.data);
				validation = detail.success ? detail.data : { reasonCode: null, lineNumber: null };
				toast.error('Configuration was not saved', { position: 'bottom-center' });
				return;
			}
			const saved = ISCSIConfigSchema.safeParse(response.data);
			current = saved.success
				? saved.data
				: {
						extraTargetConfig: editor,
						applyStatus: 'pending',
						reasonCode: 'target_runtime_checks_pending',
						lineNumber: null
					};
			if (current.applyStatus === 'pending') {
				toast.warning('Saved; runtime checks are pending', { position: 'bottom-center' });
			} else {
				toast.success('Extra target configuration saved', { position: 'bottom-center' });
			}
		} finally {
			busy = false;
		}
	}
</script>

<div class="space-y-3">
	<CustomValueInput
		label="Extra Target Configuration"
		placeholder={'portal-group user { listen 127.0.0.1:3261 }'}
		bind:value={editor}
		type="textarea"
		textAreaClasses="min-h-72 font-mono text-xs"
		disabled={busy}
	/>
	{#if byteCount > maxBytes}
		<p class="text-sm text-destructive" role="alert">The extra configuration exceeds 256 KiB.</p>
	{/if}
	{#if validation}
		<div class="text-sm text-destructive" role="alert">
			<p>Not saved. {reasonMessage(validation.reasonCode)}</p>
			{#if validation.lineNumber}<p>Line {validation.lineNumber}</p>{/if}
		</div>
	{/if}
	<div class="flex flex-wrap items-start justify-between gap-3">
		<div class="flex flex-wrap items-baseline gap-3">
			<p class="whitespace-nowrap text-xs text-muted-foreground">
				{formatBytesBinary(byteCount)} / {formatBytesBinary(maxBytes)}
			</p>
			<div class="space-y-1 text-xs" role="status" aria-live="polite">
				<div class="flex items-center gap-2">
					<span class="text-muted-foreground">Saved Configuration</span>
					<Badge variant={current.applyStatus === 'invalid' ? 'destructive' : 'outline'}>
						{#if current.applyStatus === 'checked'}
							Checked
						{:else if current.applyStatus === 'disabled'}
							Disabled
						{:else if current.applyStatus === 'invalid'}
							Invalid
						{:else}
							Pending
						{/if}
					</Badge>
				</div>
				{#if current.reasonCode}
					<p class="text-muted-foreground">{reasonMessage(current.reasonCode)}</p>
				{/if}
				{#if current.lineNumber}<p>Line {current.lineNumber}</p>{/if}
			</div>
		</div>
		<div class="flex gap-2">
			<Button variant="outline" size="sm" disabled={busy} onclick={refresh}>
				<span class="icon-[mdi--refresh] mr-1 size-4" aria-hidden="true"></span>
				Refresh Status
			</Button>
			<Button size="sm" disabled={busy || byteCount > maxBytes} aria-busy={busy} onclick={save}>
				{#if busy}
					<span class="icon-[mdi--loading] mr-1 size-4 animate-spin" aria-hidden="true"></span>
				{:else}
					<span class="icon-[mdi--content-save-outline] mr-1 size-4" aria-hidden="true"></span>
				{/if}
				Save
			</Button>
		</div>
	</div>
</div>
