<script lang="ts">
	import { addFileOrFolder, getFiles } from '$lib/api/system/file-explorer';
	import {
		editUser,
		createPamUser,
		getUserCapabilities,
		getNextUID,
		type UserPayload
	} from '$lib/api/auth/local';
	import Button from '$lib/components/ui/button/button.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { Group, SambaAction, User } from '$lib/types/auth';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { isValidEmail, isValidUsername } from '$lib/utils/string';
	import { watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		users: User[];
		groups: Group[];
		user?: User;
		edit?: boolean;
		reload?: boolean;
		hostname: string;
	}

	let {
		open = $bindable(),
		users,
		groups,
		user,
		edit = true,
		reload = $bindable(),
		hostname
	}: Props = $props();

	const shellOptions = [
		{ value: '/bin/sh', label: '/bin/sh' },
		{ value: '/bin/csh', label: '/bin/csh' },
		{ value: '/bin/tcsh', label: '/bin/tcsh' },
		{ value: '/usr/local/bin/bash', label: '/usr/local/bin/bash' },
		{ value: '/usr/local/bin/zsh', label: '/usr/local/bin/zsh' },
		{ value: '/usr/sbin/nologin', label: '/usr/sbin/nologin' }
	];
	const sambaActionOptions = [
		{ value: 'keep', label: 'Keep current Samba state' },
		{ value: 'upsert', label: 'Create or update Samba user' },
		{ value: 'remove', label: 'Remove Samba user' }
	];

	let groupOptions = $derived(groups.map((g) => ({ value: String(g.id), label: g.name })));

	function decomposePerms(perms: number) {
		const u = Math.floor(perms / 64);
		const g = Math.floor((perms % 64) / 8);
		const o = perms % 8;
		return {
			user: { read: !!(u & 4), write: !!(u & 2), exec: !!(u & 1) },
			group: { read: !!(g & 4), write: !!(g & 2), exec: !!(g & 1) },
			other: { read: !!(o & 4), write: !!(o & 2), exec: !!(o & 1) }
		};
	}

	function getInitialPrimaryGroup(): string {
		if (user?.primaryGroupId) {
			return String(user.primaryGroupId);
		}
		return '';
	}

	function getInitialAuxGroups(): string[] {
		if (user?.groups) {
			return user.groups.filter((g) => g.id !== user?.primaryGroupId).map((g) => String(g.id));
		}
		return [];
	}

	function makeDefaults() {
		return {
			fullName: user?.fullName ?? '',
			username: user?.username ?? '',
			email: user?.email ?? '',
			password: '',
			confirmPassword: '',
			admin: user?.admin ?? false,
			uid: user?.uid ?? 0,
			newPrimaryGroup: false,
			primaryGroup: { open: false, value: getInitialPrimaryGroup() },
			auxGroups: { open: false, value: getInitialAuxGroups() },
			homeDirectory: user?.homeDirectory ?? '/nonexistent',
			perms: decomposePerms(user?.homeDirPerms ?? 493),
			sshPublicKey: user?.sshPublicKey ?? '',
			shell: { open: false, value: user?.shell ?? '/bin/sh' },
			disablePassword: user?.disablePassword ?? false,
			locked: user?.locked ?? false,
			doasEnabled: user?.doasEnabled ?? false,
			createSamba: false,
			sambaAction: { open: false, value: 'keep' as SambaAction }
		};
	}

	let properties = $state(makeDefaults());
	let activeTab = $state('identity');
	let doasAvailable = $state(false);
	let loading = $state(false);
	let capabilitiesLoading = $state(false);
	let uidLoading = $state(false);
	let capabilitiesController: AbortController | null = null;
	let uidController: AbortController | null = null;
	let directoryController: AbortController | null = null;

	function cancelMetadataLoads() {
		capabilitiesController?.abort();
		uidController?.abort();
		directoryController?.abort();
		capabilitiesController = null;
		uidController = null;
		directoryController = null;
		capabilitiesLoading = false;
		uidLoading = false;
	}

	async function loadCapabilities() {
		capabilitiesController?.abort();
		const controller = new AbortController();
		capabilitiesController = controller;
		capabilitiesLoading = true;
		try {
			const response = await getUserCapabilities({ hostname, signal: controller.signal });
			if (controller.signal.aborted) return;
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to load user-management capabilities', {
					position: 'bottom-center'
				});
				return;
			}
			doasAvailable = response.doasAvailable;
		} catch (error) {
			if (isRequestCancellation(error)) return;
			toast.error('Failed to load user-management capabilities', {
				position: 'bottom-center'
			});
		} finally {
			if (capabilitiesController === controller) {
				capabilitiesController = null;
				capabilitiesLoading = false;
			}
		}
	}

	async function loadNextUID() {
		uidController?.abort();
		const controller = new AbortController();
		uidController = controller;
		uidLoading = true;
		try {
			const response = await getNextUID({ hostname, signal: controller.signal });
			if (controller.signal.aborted) return;
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to load the next available UID', { position: 'bottom-center' });
				return;
			}
			properties.uid = response.nextUID;
		} catch (error) {
			if (isRequestCancellation(error)) return;
			toast.error('Failed to load the next available UID', { position: 'bottom-center' });
		} finally {
			if (uidController === controller) {
				uidController = null;
				uidLoading = false;
			}
		}
	}

	watch([() => open, () => hostname, () => edit, () => user?.id], ([isOpen]) => {
		cancelMetadataLoads();
		if (!isOpen) return;
		doasAvailable = false;
		properties = makeDefaults();
		loadCapabilities();
		if (!edit) loadNextUID();
	});

	onDestroy(cancelMetadataLoads);

	const tabs = [
		{ value: 'identity', label: 'Identity' },
		{ value: 'groups', label: 'Groups' },
		{ value: 'filesystem', label: 'Filesystem' },
		{ value: 'security', label: 'Security' }
	];

	let computedPerms = $derived.by(() => {
		const p = properties.perms;
		const u = (p.user.read ? 4 : 0) + (p.user.write ? 2 : 0) + (p.user.exec ? 1 : 0);
		const g = (p.group.read ? 4 : 0) + (p.group.write ? 2 : 0) + (p.group.exec ? 1 : 0);
		const o = (p.other.read ? 4 : 0) + (p.other.write ? 2 : 0) + (p.other.exec ? 1 : 0);
		return u * 64 + g * 8 + o;
	});

	const hiddenRootDirs = new Set([
		'/bin',
		'/boot',
		'/dev',
		'/entropy',
		'/lib',
		'/libexec',
		'/net',
		'/proc',
		'/rescue',
		'/root',
		'/sbin',
		'/sys',
		'/usr',
		'/var',
		'/etc',
		'/compat'
	]);

	let dirPicker = $state({
		open: false,
		currentPath: '/',
		items: [] as FileNode[],
		loading: false
	});

	let newFolderInput = $state({ active: false, parentPath: '', name: '' });
	let ctxMenu = $state({ show: false, x: 0, y: 0, target: null as string | null });

	function hideCtxMenu() {
		ctxMenu.show = false;
	}

	function breadcrumbParts(path: string) {
		const parts = path.split('/').filter(Boolean);
		const result: { name: string; path: string; isLast: boolean }[] = [
			{ name: '/', path: '/', isLast: parts.length === 0 }
		];
		let built = '';
		for (let i = 0; i < parts.length; i++) {
			built += '/' + parts[i];
			result.push({ name: parts[i], path: built, isLast: i === parts.length - 1 });
		}
		return result;
	}

	async function loadDir(path: string) {
		directoryController?.abort();
		const controller = new AbortController();
		directoryController = controller;
		dirPicker.loading = true;
		dirPicker.currentPath = path;
		try {
			const all = await getFiles(path === '/' ? undefined : path, hostname, controller.signal);
			if (controller.signal.aborted) return;
			if (isAPIResponse(all)) {
				handleAPIError(all);
				toast.error('Failed to load directories', { position: 'bottom-center' });
				dirPicker.items = [];
				return;
			}
			dirPicker.items = all.filter(
				(f) => f.type === 'folder' && (path !== '/' || !hiddenRootDirs.has(f.id))
			);
		} catch (error) {
			if (isRequestCancellation(error)) return;
			toast.error('Failed to load directories', { position: 'bottom-center' });
			dirPicker.items = [];
		} finally {
			if (directoryController === controller || directoryController === null) {
				directoryController = null;
				dirPicker.loading = false;
			}
		}
	}

	function openDirPicker() {
		dirPicker.open = true;
		loadDir(properties.homeDirectory !== '/nonexistent' ? properties.homeDirectory : '/');
	}

	function selectDir() {
		properties.homeDirectory = dirPicker.currentPath;
		dirPicker.open = false;
	}

	function goUp() {
		const parts = dirPicker.currentPath.split('/').filter(Boolean);
		parts.pop();
		loadDir(parts.length === 0 ? '/' : '/' + parts.join('/'));
	}

	function startNewFolder(parentPath: string) {
		newFolderInput = { active: true, parentPath, name: '' };
	}

	async function createFolder() {
		const name = newFolderInput.name.trim();
		if (!name) {
			newFolderInput.active = false;
			return;
		}
		const res = await addFileOrFolder(newFolderInput.parentPath, name, true, hostname);
		newFolderInput = { active: false, parentPath: '', name: '' };
		if (res.error) {
			handleAPIError(res);
			toast.error('Failed to create folder', { position: 'bottom-center' });
		} else {
			await loadDir(dirPicker.currentPath);
		}
	}

	function reset() {
		properties = makeDefaults();
		activeTab = 'identity';
	}

	function validate(): string {
		if (!properties.username) return 'Username is required';
		if (!isValidUsername(properties.username)) return 'Invalid username format';
		if (edit && user && users.some((u) => u.id !== user!.id && u.username === properties.username))
			return 'Username already exists';
		if (!edit && users.some((u) => u.username === properties.username))
			return 'Username already exists';
		if (properties.email && !isValidEmail(properties.email)) return 'Invalid email address';
		if (!edit) {
			if (!properties.password) return 'Password is required';
			if (properties.password.length < 8) return 'Password must be at least 8 characters';
		}
		if (edit && properties.password && properties.password.length < 8)
			return 'Password must be at least 8 characters';
		if (properties.password.length > 128) return 'Password must be 128 characters or fewer';
		if (properties.password && properties.confirmPassword !== properties.password)
			return 'Passwords do not match';
		if (properties.uid < 1000) return 'UID must be 1000 or higher';
		if (properties.uid > 65533) return 'UID must be 65533 or lower';
		if (edit && user?.disablePassword && !properties.disablePassword && !properties.password)
			return 'Set a password to enable password authentication';
		if (edit && properties.sambaAction.value === 'upsert' && !properties.password)
			return 'Set a password to create or update the Samba user';
		if (
			properties.homeDirectory !== '/nonexistent' &&
			edit &&
			user &&
			users.some((u) => u.id !== user.id && u.homeDirectory === properties.homeDirectory)
		)
			return 'Home directory is already in use by another user';
		if (
			properties.homeDirectory !== '/nonexistent' &&
			!edit &&
			users.some((u) => u.homeDirectory === properties.homeDirectory)
		)
			return 'Home directory is already in use by another user';

		return '';
	}

	async function submit() {
		if (loading || (!edit && uidLoading)) return;

		const error = validate();
		if (error) {
			toast.error(error, { position: 'bottom-center' });
			return;
		}

		let primaryGroupId: number | undefined;
		if (!properties.newPrimaryGroup && properties.primaryGroup.value) {
			primaryGroupId = parseInt(properties.primaryGroup.value);
		}

		const auxGroupIds = properties.auxGroups.value.map((v) => parseInt(v));

		const payload: UserPayload = {
			fullName: properties.fullName,
			username: properties.username,
			email: properties.email,
			password: properties.password,
			admin: properties.admin,
			uid: properties.uid,
			newPrimaryGroup: properties.newPrimaryGroup,
			primaryGroupId,
			auxGroupIds,
			homeDirectory: properties.homeDirectory,
			homeDirPerms: computedPerms,
			sshPublicKey: properties.sshPublicKey,
			shell: properties.shell.value,
			disablePassword: properties.disablePassword,
			locked: properties.locked,
			doasEnabled: properties.doasEnabled,
			...(edit
				? { sambaAction: properties.sambaAction.value }
				: { createSamba: properties.createSamba })
		};

		loading = true;
		try {
			const response = edit
				? await editUser(user!.id, payload, { hostname })
				: await createPamUser(payload, { hostname });

			if (isAPIResponse(response) && response.status === 'error') {
				handleAPIError(response);
				toast.error(edit ? 'Failed to edit user' : 'Failed to create user', {
					position: 'bottom-center'
				});
				return;
			}

			reload = true;
			toast.success(edit ? 'User edited' : 'User created', { position: 'bottom-center' });
			open = false;
			reset();
		} catch (error) {
			console.error('PAM user mutation failed:', error);
			toast.error(edit ? 'Failed to edit user' : 'Failed to create user', {
				position: 'bottom-center'
			});
		} finally {
			loading = false;
		}
	}
</script>

<svelte:window onpointerdown={() => hideCtxMenu()} />

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={() => {
			reset();
			open = false;
		}}
		class="lg:max-w-2xl w-[92%] gap-4 p-6"
		showCloseButton={true}
		showResetButton={true}
		onClose={() => {
			reset();
			open = false;
		}}
		onReset={reset}
	>
		{#if dirPicker.open}
			<div class="relative" data-picker-container>
				<Dialog.Header>
					<Dialog.Title class="flex items-center gap-2 text-left text-sm font-medium">
						<Button
							size="icon"
							variant="ghost"
							class="h-7 w-7 shrink-0"
							onclick={() => (dirPicker.open = false)}
						>
							<span class="icon-[tabler--arrow-left] h-4 w-4"></span>
						</Button>
						Select Directory
					</Dialog.Title>
				</Dialog.Header>

				<div class="flex items-center gap-1 border-b pb-2">
					<Breadcrumb.Root>
						<Breadcrumb.List>
							{#each breadcrumbParts(dirPicker.currentPath) as item (item.path)}
								<Breadcrumb.Item>
									{#if item.isLast}
										<Breadcrumb.Page class="text-xs">{item.name}</Breadcrumb.Page>
									{:else}
										<Breadcrumb.Link
											href="#"
											class="text-xs"
											onclick={(e: MouseEvent) => {
												e.preventDefault();
												loadDir(item.path);
											}}>{item.name}</Breadcrumb.Link
										>
									{/if}
								</Breadcrumb.Item>
								{#if !item.isLast}
									<Breadcrumb.Separator />
								{/if}
							{/each}
						</Breadcrumb.List>
					</Breadcrumb.Root>
					<Button
						variant="ghost"
						size="icon"
						class="ml-auto h-7 w-7 shrink-0"
						disabled={dirPicker.currentPath === '/'}
						onclick={goUp}
						title="Go up"
					>
						<span class="icon-[mdi--arrow-up] h-4 w-4"></span>
					</Button>
				</div>

				<div
					role="tree"
					tabindex="-1"
					class="block h-64 w-full overflow-y-auto rounded-md border"
					oncontextmenu={(e: MouseEvent) => {
						e.preventDefault();
						const container = (e.currentTarget as Element).closest(
							'[data-picker-container]'
						) as HTMLElement;
						const rect = container.getBoundingClientRect();
						const btn = (e.target as Element)?.closest('[data-folder]');
						ctxMenu = {
							show: true,
							x: e.clientX - rect.left,
							y: e.clientY - rect.top,
							target: btn ? btn.getAttribute('data-folder') : null
						};
					}}
				>
					{#if dirPicker.loading}
						<div class="text-muted-foreground flex h-full items-center justify-center text-sm">
							Loading…
						</div>
					{:else}
						<ul class="p-1">
							{#if newFolderInput.active}
								<li class="flex items-center gap-2 rounded px-3 py-1.5">
									<span
										class="icon-[mdi--folder-plus-outline] text-muted-foreground h-4 w-4 shrink-0"
									></span>
									<!-- svelte-ignore a11y_autofocus -->
									<input
										type="text"
										class="text-foreground flex-1 border-b bg-transparent text-sm outline-none"
										placeholder="New folder name"
										bind:value={newFolderInput.name}
										autofocus
										onkeydown={(e: KeyboardEvent) => {
											if (e.key === 'Enter') createFolder();
											if (e.key === 'Escape') newFolderInput.active = false;
										}}
									/>
									<button
										aria-label="Confirm"
										class="text-muted-foreground hover:text-foreground"
										onclick={createFolder}
									>
										<span class="icon-[mdi--check] h-4 w-4"></span>
									</button>
									<button
										aria-label="Cancel"
										class="text-muted-foreground hover:text-foreground"
										onclick={() => (newFolderInput.active = false)}
									>
										<span class="icon-[mdi--close] h-4 w-4"></span>
									</button>
								</li>
							{/if}
							{#if dirPicker.items.length === 0 && !newFolderInput.active}
								<li
									class="text-muted-foreground flex h-56 items-center justify-center text-sm select-none"
								>
									No subdirectories — right-click to create one
								</li>
							{:else}
								{#each dirPicker.items as folder (folder.id)}
									{@const name = folder.id.split('/').pop() || folder.id}
									<li>
										<button
											data-folder={folder.id}
											class="hover:bg-accent flex w-full items-center gap-2 rounded px-3 py-1.5 text-left text-sm"
											onclick={() => loadDir(folder.id)}
										>
											<span class="icon-[mdi--folder] text-muted-foreground h-4 w-4 shrink-0"
											></span>
											{name}
										</button>
									</li>
								{/each}
							{/if}
						</ul>
					{/if}
				</div>

				<div class="flex items-center justify-between gap-3 pt-1">
					<span class="text-muted-foreground truncate text-xs">{dirPicker.currentPath}</span>
					<Button size="sm" onclick={selectDir}>Select</Button>
				</div>

				{#if ctxMenu.show}
					<div
						role="menu"
						tabindex="-1"
						class="bg-popover text-popover-foreground border-border absolute z-50 min-w-36 rounded-md border p-1 shadow-md"
						style="left: {ctxMenu.x}px; top: {ctxMenu.y}px"
						onpointerdown={(e: PointerEvent) => e.stopPropagation()}
					>
						{#if ctxMenu.target !== null}
							<button
								role="menuitem"
								class="hover:bg-accent flex w-full items-center rounded-sm px-2 py-1.5 text-sm"
								onclick={() => {
									startNewFolder(ctxMenu.target!);
									hideCtxMenu();
								}}
							>
								<span class="icon-[mdi--folder-plus-outline] mr-2 h-4 w-4"></span>
								New Folder inside
							</button>
							<button
								role="menuitem"
								class="hover:bg-accent flex w-full items-center rounded-sm px-2 py-1.5 text-sm"
								onclick={() => {
									startNewFolder(dirPicker.currentPath);
									hideCtxMenu();
								}}
							>
								<span class="icon-[mdi--folder-plus-outline] mr-2 h-4 w-4"></span>
								New Folder here
							</button>
						{:else}
							<button
								role="menuitem"
								class="hover:bg-accent flex w-full items-center rounded-sm px-2 py-1.5 text-sm"
								onclick={() => {
									startNewFolder(dirPicker.currentPath);
									hideCtxMenu();
								}}
							>
								<span class="icon-[mdi--folder-plus-outline] mr-2 h-4 w-4"></span>
								New Folder
							</button>
						{/if}
					</div>
				{/if}
			</div>
		{:else}
			<Dialog.Header>
				<Dialog.Title>
					<SpanWithIcon
						icon="icon-[mdi--user-edit]"
						size="h-5 w-5"
						gap="gap-2"
						title={edit ? `Edit PAM User - ${user?.username}` : 'Create PAM User'}
					/>
				</Dialog.Title>
			</Dialog.Header>

			<ScrollArea orientation="vertical" class="h-[40vh] pt-3">
				<Tabs.Root bind:value={activeTab} class="w-full overflow-hidden">
					<Tabs.List class="grid w-full grid-cols-4 p-0">
						{#each tabs as { value, label } (value)}
							<Tabs.Trigger class="border-b" {value}>{label}</Tabs.Trigger>
						{/each}
					</Tabs.List>

					<Tabs.Content value="identity">
						<div class="space-y-3 pt-2 pb-1">
							<input type="text" style="display:none" autocomplete="username" />
							<input type="password" style="display:none" autocomplete="new-password" />

							<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
								<CustomValueInput
									label="Full Name"
									placeholder="John Doe"
									bind:value={properties.fullName}
								/>
								<CustomValueInput
									label="Username"
									placeholder="johndoe"
									bind:value={properties.username}
									disabled={edit}
								/>
							</div>
							<CustomValueInput
								label="E-Mail"
								placeholder="john@example.com"
								bind:value={properties.email}
							/>
							<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
								<CustomValueInput
									label="Unix/PAM + Sylve Password"
									placeholder={edit ? 'Leave blank to keep current' : '••••••••'}
									type="password"
									revealOnFocus={true}
									bind:value={properties.password}
								/>
								<CustomValueInput
									label="Confirm Password"
									placeholder="••••••••"
									type="password"
									revealOnFocus={true}
									bind:value={properties.confirmPassword}
								/>
							</div>
							<div class="flex flex-wrap items-end gap-6">
								<div class="flex items-center gap-2">
									<Checkbox id="pam-admin" bind:checked={properties.admin} />
									<Label for="pam-admin" class="cursor-pointer text-sm">Admin</Label>
								</div>
								{#if edit}
									<div class="min-w-64 flex-1">
										<CustomComboBox
											label="Samba action"
											placeholder="Keep current Samba state"
											bind:open={properties.sambaAction.open}
											bind:value={properties.sambaAction.value}
											data={sambaActionOptions}
											width="w-full"
										/>
									</div>
								{:else}
									<div class="flex items-center gap-2">
										<Checkbox id="create-samba" bind:checked={properties.createSamba} />
										<Label for="create-samba" class="cursor-pointer text-sm">Samba User</Label>
									</div>
								{/if}
							</div>
						</div>
					</Tabs.Content>

					<Tabs.Content value="groups">
						<div class="space-y-3 pt-2 pb-1">
							{#if !properties.newPrimaryGroup}
								<CustomComboBox
									label="Primary Group"
									placeholder="Select primary group"
									bind:open={properties.primaryGroup.open}
									bind:value={properties.primaryGroup.value}
									data={groupOptions}
									width="w-full"
								/>
							{/if}

							<CustomComboBox
								label="Auxiliary Groups"
								placeholder="Select groups"
								bind:open={properties.auxGroups.open}
								bind:value={properties.auxGroups.value}
								onValueChange={(v) => {
									properties.auxGroups.value = v as string[];
								}}
								data={groupOptions}
								multiple={true}
								width="w-full"
							/>

							<div class="flex items-center gap-2">
								<Checkbox id="new-primary-group" bind:checked={properties.newPrimaryGroup} />
								<Label for="new-primary-group" class="cursor-pointer text-sm">
									Create new primary group
								</Label>
							</div>
						</div>
					</Tabs.Content>

					<Tabs.Content value="filesystem">
						<div class="space-y-3 pt-2 pb-1">
							<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
								<CustomValueInput
									label="UID"
									placeholder={uidLoading ? 'Loading…' : '1000'}
									disabled={uidLoading}
									value={properties.uid}
									onChange={(v) => {
										properties.uid = typeof v === 'string' ? parseInt(v) || 0 : v;
									}}
								/>
								<CustomValueInput
									label="Home Directory"
									placeholder="/home/john"
									bind:value={properties.homeDirectory}
									topRightButton={{
										icon: 'icon-[mdi--folder-open]',
										tooltip: 'Browse',
										function: async () => {
											openDirPicker();
											return '';
										}
									}}
								/>
							</div>

							<div class="space-y-1.5">
								<Label class="text-sm">Home Directory Permissions</Label>
								<div class="bg-muted rounded-md p-3 space-y-2">
									{#each ['user', 'group', 'other'] as entity (entity)}
										<div class="flex items-center gap-4">
											<span class="w-12 text-sm capitalize">{entity}</span>
											<div class="flex flex-1 items-center justify-evenly">
												<div class="flex items-center gap-2">
													<Checkbox
														id={`perm-${entity}-read`}
														bind:checked={
															properties.perms[entity as keyof typeof properties.perms].read
														}
													/>
													<Label for={`perm-${entity}-read`} class="cursor-pointer text-xs"
														>Read</Label
													>
												</div>
												<div class="flex items-center gap-2">
													<Checkbox
														id={`perm-${entity}-write`}
														bind:checked={
															properties.perms[entity as keyof typeof properties.perms].write
														}
													/>
													<Label for={`perm-${entity}-write`} class="cursor-pointer text-xs"
														>Write</Label
													>
												</div>
												<div class="flex items-center gap-2">
													<Checkbox
														id={`perm-${entity}-exec`}
														bind:checked={
															properties.perms[entity as keyof typeof properties.perms].exec
														}
													/>
													<Label for={`perm-${entity}-exec`} class="cursor-pointer text-xs"
														>Exec</Label
													>
												</div>
											</div>
										</div>
									{/each}
									<div class="text-muted-foreground text-xs pt-1">
										{computedPerms} (octal)
									</div>
								</div>
							</div>

							<CustomComboBox
								label="Shell"
								placeholder="Select shell"
								bind:open={properties.shell.open}
								bind:value={properties.shell.value}
								data={shellOptions}
								width="w-full"
							/>
						</div>
					</Tabs.Content>

					<Tabs.Content value="security">
						<div class="space-y-3 pt-2 pb-1">
							<CustomValueInput
								label="SSH Public Key"
								placeholder="ssh-rsa AAAAB3..."
								bind:value={properties.sshPublicKey}
							/>

							<div class="flex flex-wrap items-center gap-4 pt-2">
								<div class="flex items-center gap-2">
									<Checkbox id="disable-password" bind:checked={properties.disablePassword} />
									<Label for="disable-password" class="cursor-pointer text-sm"
										>Disable Password</Label
									>
								</div>
								<div class="flex items-center gap-2">
									<Checkbox id="locked" bind:checked={properties.locked} />
									<Label for="locked" class="cursor-pointer text-sm">Locked</Label>
								</div>
								{#if doasAvailable}
									<div class="flex items-center gap-2">
										<Checkbox id="doas" bind:checked={properties.doasEnabled} />
										<Label for="doas" class="cursor-pointer text-sm">Doas Enabled</Label>
									</div>
								{/if}
							</div>
						</div>
					</Tabs.Content>
				</Tabs.Root>
			</ScrollArea>
		{/if}

		<div class="flex justify-end pt-1">
			<Button onclick={submit} disabled={loading || uidLoading || capabilitiesLoading}
				>{loading ? (edit ? 'Saving…' : 'Creating…') : edit ? 'Save' : 'Create'}</Button
			>
		</div>
	</Dialog.Content>
</Dialog.Root>
