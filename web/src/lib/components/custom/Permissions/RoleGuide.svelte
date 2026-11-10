<script lang="ts">
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Role } from '$lib/types/auth';

	interface Props {
		open: boolean;
		roles: Role[];
	}

	let { open = $bindable(), roles }: Props = $props();

	const guide = [
		{
			name: 'Administrator',
			icon: 'icon-[mdi--shield-crown-outline]',
			text: 'Everything: guests, storage, networks, the hosts, users and permissions. On a node it includes host settings that amount to root on that node. A few actions stay with root@pam and admin@sylve alone: forming and changing the cluster, resetting a node, the File Explorer, and editing those two accounts.'
		},
		{
			name: 'SysAdmin',
			icon: 'icon-[mdi--server]',
			text: "Runs the hosts: view and change node settings, read logs, open the host console, and reboot or shut down. Changing node settings is as good as root on that node, so give it only to people you'd trust with root. It doesn't cover guests, users or permissions."
		},
		{
			name: 'GuestAdmin',
			icon: 'icon-[mdi--cube-outline]',
			text: 'Full control of virtual machines, jails and containers: create and delete them, change their hardware and options, clone, migrate, snapshot and roll back, power and console. It can use storage and networks for them. Settings that reach the host itself (hooks, passthrough, raw options and the like) still need host admin rights.'
		},
		{
			name: 'GuestUser',
			icon: 'icon-[mdi--play-circle-outline]',
			text: "Day-to-day use of guests: see them, start, stop and restart them, open their console, and edit cloud-init. It can't change their hardware or create and delete them."
		},
		{
			name: 'DatastoreUser',
			icon: 'icon-[mdi--database-outline]',
			text: 'Use storage: see pools, take space from them for guest disks and datasets, and add templates and images such as jail bootstraps and container images.'
		},
		{
			name: 'Auditor',
			icon: 'icon-[mdi--eye-outline]',
			text: 'Read-only: see guests, storage, networks and node status, and read the logs, without changing anything.'
		},
		{
			name: 'NoAccess',
			icon: 'icon-[mdi--block-helper]',
			text: 'Takes everything away on the path it is granted on, and below it if propagated, whatever else applies there. Use it to carve an exception out of a broader grant.'
		}
	];

	let privilegesOf = $derived(
		Object.fromEntries(roles.map((role) => [role.name, role.privileges]))
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="flex max-h-[85vh] w-full max-w-2xl! flex-col gap-0 overflow-hidden p-6">
		<Dialog.Header class="shrink-0 pr-10">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--help-circle-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Built-in Roles"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 min-h-0 space-y-4 overflow-y-auto pr-1 text-justify hyphens-auto">
			<div class="bg-muted/50 space-y-1.5 rounded-md p-3 text-sm">
				<p>
					A grant gives a user or a permission group a role on a path: the whole datacenter (/), a
					node, a guest, a storage pool or a switch. With Propagate it also applies to everything
					below that path.
				</p>
				<p class="text-muted-foreground text-xs">
					A grant on a deeper path replaces what comes from above, a user's own grants replace their
					groups' on the same path, and NoAccess removes everything. root@pam and admin@sylve skip
					all of this.
				</p>
			</div>

			{#each guide as role (role.name)}
				<div class="space-y-1.5 rounded-md border p-3">
					<div class="flex items-center gap-2">
						<span class="{role.icon} text-muted-foreground h-4 w-4 shrink-0"></span>
						<span class="text-sm font-medium">{role.name}</span>
						{#if role.name === 'Administrator'}
							<span
								class="inline-flex items-center rounded border border-amber-400/50 px-1 font-mono text-xs leading-tight text-amber-400"
							>
								ALL PRIVILEGES
							</span>
						{/if}
					</div>
					<p class="text-sm">{role.text}</p>
					{#if role.name !== 'Administrator' && (privilegesOf[role.name] ?? []).length > 0}
						<div class="flex flex-wrap gap-1 pt-1.5">
							{#each privilegesOf[role.name] as privilege (privilege)}
								<span class="bg-secondary rounded px-1.5 py-0.5 font-mono text-xs">{privilege}</span
								>
							{/each}
						</div>
					{/if}
				</div>
			{/each}

			<p class="text-muted-foreground text-xs">
				Need something in between? Create a custom role from any set of privileges.
			</p>
		</div>
	</Dialog.Content>
</Dialog.Root>
