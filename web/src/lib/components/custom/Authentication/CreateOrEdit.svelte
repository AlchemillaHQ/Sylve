<script lang="ts">
	import { addFileOrFolder, getFiles } from '$lib/api/system/file-explorer';
	import { createUser, editUser, getNextUID, getUserCapabilities } from '$lib/api/auth/local';
	import Button from '$lib/components/ui/button/button.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb';
	import type { Group, User } from '$lib/types/auth';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidEmail, isValidUsername } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		users: User[];
		groups: Group[];
		user?: User;
		edit?: boolean;
		reload?: boolean;
	}

	let {
		open = $bindable(),
		users,
		groups,
		user,
		edit = false,
		reload = $bindable()
	}: Props = $props();

	const shellOptions = [
		{ value: '/bin/sh', label: '/bin/sh' },
		{ value: '/bin/csh', label: '/bin/csh' },
		{ value: '/bin/tcsh', label: '/bin/tcsh' },
		{ value: '/usr/local/bin/bash', label: '/usr/local/bin/bash' },
		{ value: '/usr/local/bin/zsh', label: '/usr/local/bin/zsh' },
		{ value: '/usr/sbin/nologin', label: '/usr/sbin/nologin' }
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
		if (edit && user?.primaryGroupId) {
			return String(user.primaryGroupId);
		}
		return '';
	}

	function getInitialAuxGroups(): string[] {
		if (edit && user?.groups) {
			return user.groups.filter((g) => g.id !== user?.primaryGroupId).map((g) => String(g.id));
		}
		return [];
	}

	function makeDefaults() {
		return {
			// Tab 1
			fullName: edit && user ? (user.fullName ?? '') : '',
			username: edit && user ? user.username : '',
			email: edit && user ? (user.email ?? '') : '',
			password: '',
			confirmPassword: '',
			// Tab 2
			uid: edit && user ? (user.uid ?? 0) : 0,
			newPrimaryGroup: false,
			primaryGroup: { open: false, value: getInitialPrimaryGroup() },
			auxGroups: { open: false, value: getInitialAuxGroups() },
			// Tab 3
			homeDirectory: edit && user ? (user.homeDirectory ?? '/nonexistent') : '/nonexistent',
			perms: decomposePerms(edit && user ? (user.homeDirPerms ?? 493) : 493),
			// Tab 4
			sshPublicKey: edit && user ? (user.sshPublicKey ?? '') : '',
			shell: { open: false, value: edit && user ? (user.shell ?? '/bin/sh') : '/bin/sh' },
			disablePassword: edit && user ? (user.disablePassword ?? false) : false,
			locked: edit && user ? (user.locked ?? false) : false,
			doasEnabled: edit && user ? (user.doasEnabled ?? false) : false,
			admin: edit && user ? user.admin : false
		};
	}

	let properties = $state(makeDefaults());
	let activeTab = $state('identification');
	let doasAvailable = $state(false);

	// Directories that should be hidden in the directory picker (system/non-user directories)
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
		'/sbin',
		'/sys',
		'/usr',
		'/var',
		'/etc',
		'/compat'
	]);

	// --- Mini directory picker state ---
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
		dirPicker.loading = true;
		dirPicker.currentPath = path;
		try {
			const all = await getFiles(path === '/' ? undefined : path);
			dirPicker.items = all.filter(
				(f) => f.type === 'folder' && (path !== '/' || !hiddenRootDirs.has(f.id))
			);
		} catch {
			dirPicker.items = [];
		} finally {
			dirPicker.loading = false;
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
		const res = await addFileOrFolder(newFolderInput.parentPath, name, true);
		newFolderInput = { active: false, parentPath: '', name: '' };
		if (res.error) {
			toast.error('Failed to create folder', { position: 'bottom-center' });
		} else {
			await loadDir(dirPicker.currentPath);
		}
	}

	// --- end dir picker ---

	let computedPerms = $derived.by(() => {
		const p = properties.perms;
		const u = (p.user.read ? 4 : 0) + (p.user.write ? 2 : 0) + (p.user.exec ? 1 : 0);
		const g = (p.group.read ? 4 : 0) + (p.group.write ? 2 : 0) + (p.group.exec ? 1 : 0);
		const o = (p.other.read ? 4 : 0) + (p.other.write ? 2 : 0) + (p.other.exec ? 1 : 0);
		return u * 64 + g * 8 + o;
	});

	$effect(() => {
		if (open && !edit) {
			getNextUID().then((res) => {
				if (res && !res.error && res.data) {
					properties.uid = res.data.nextUID;
				}
			});
		}
	});

	$effect(() => {
		if (open) {
			getUserCapabilities().then((res) => {
				if (res && !res.error && res.data) {
					doasAvailable = res.data.doasAvailable;
				}
			});
		}
	});

	function reset() {
		properties = makeDefaults();
		activeTab = 'identification';
	}

	function validate(): string {
		if (!properties.username) return 'Username is required';
		if (!isValidUsername(properties.username)) return 'Invalid username format';
		if (!edit && users.some((u) => u.username === properties.username))
			return 'Username already exists';
		if (edit && user && users.some((u) => u.id !== user!.id && u.username === properties.username))
			return 'Username already exists';
		if (properties.email && !isValidEmail(properties.email)) return 'Invalid email address';
		if (!edit && !properties.disablePassword) {
			if (!properties.password) return 'Password is required';
			if (properties.password.length < 8) return 'Password must be at least 8 characters';
		}
		if (properties.password && properties.confirmPassword !== properties.password)
			return 'Passwords do not match';
		if (!properties.homeDirectory) return 'Home directory is required';

		// UID collision
		if (properties.uid > 0) {
			const collision = users.find((u) => u.uid === properties.uid && (!edit || u.id !== user?.id));
			if (collision) return `UID ${properties.uid} is already used by "${collision.username}"`;
		}

		// Primary group also selected as auxiliary
		if (
			!properties.newPrimaryGroup &&
			properties.primaryGroup.value &&
			properties.auxGroups.value.includes(properties.primaryGroup.value)
		) {
			return 'Primary group cannot also be an auxiliary group';
		}

		return '';
	}

	async function submit() {
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

		const payload = {
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
			doasEnabled: properties.doasEnabled
		};

		let response;
		if (edit && user) {
			response = await editUser(user.id, payload);
		} else {
			response = await createUser(payload);
		}

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error(edit ? 'Failed to edit user' : 'Failed to create user', {
				position: 'bottom-center'
			});
		} else {
			toast.success(edit ? 'User edited' : 'User created', { position: 'bottom-center' });
			open = false;
			reset();
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
		class="lg:max-w-2xl w-[92%] gap-4 p-5"
	>
		{#if dirPicker.open}
			<!-- ── Dir picker view (inline — no second dialog) ── -->
			<div class="relative" data-picker-container>
				<Dialog.Header class="p-0">
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

				<!-- Breadcrumb nav -->
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

				<!-- Folder list — plain div, custom right-click menu avoids bits-ui portal interference -->
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

				<!-- Current path + actions -->
				<div class="flex items-center justify-between gap-3 pt-1">
					<span class="text-muted-foreground truncate text-xs">{dirPicker.currentPath}</span>
					<div class="flex shrink-0 gap-2">
						<Button variant="outline" size="sm" onclick={() => (dirPicker.open = false)}>
							Cancel
						</Button>
						<Button size="sm" onclick={selectDir}>Select</Button>
					</div>
				</div>

				<!-- Context menu, absolute inside picker container so coords are correct -->
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
			<!-- end picker container -->
		{:else}
			<!-- ── Normal create/edit view ── -->
			<Dialog.Header class="p-0">
				<Dialog.Title class="flex justify-between text-left">
					<div class="flex items-center gap-2">
						{#if !edit}
							<span class="icon-[mdi--user-plus] h-5 w-5"></span>
							<span>Create User</span>
						{:else}
							<span class="icon-[mdi--user-edit] h-5 w-5"></span>
							<span>Edit User - {user?.username}</span>
						{/if}
					</div>
					<div class="flex items-center gap-0.5">
						<Button size="sm" variant="link" class="h-4" title="Reset" onclick={reset}>
							<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
							<span class="sr-only">Reset</span>
						</Button>
						<Button
							size="sm"
							variant="link"
							class="h-4"
							title="Close"
							onclick={() => {
								reset();
								open = false;
							}}
						>
							<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"
							></span>
							<span class="sr-only">Close</span>
						</Button>
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<Tabs.Root bind:value={activeTab}>
				<Tabs.List class="w-full">
					<Tabs.Trigger value="identification" class="flex-1 text-xs">Identification</Tabs.Trigger>
					<Tabs.Trigger value="id-groups" class="flex-1 text-xs">ID & Groups</Tabs.Trigger>
					<Tabs.Trigger value="directories" class="flex-1 text-xs">Directory</Tabs.Trigger>
					<Tabs.Trigger value="authentication" class="flex-1 text-xs">Authentication</Tabs.Trigger>
				</Tabs.List>

				<!-- Tab 1: Identification -->
				<Tabs.Content value="identification" class="space-y-3 pt-3">
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
						/>
					</div>
					<CustomValueInput
						label="E-Mail"
						placeholder="john@example.com"
						bind:value={properties.email}
					/>
					<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
						<CustomValueInput
							label="Password"
							placeholder="••••••••"
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
				</Tabs.Content>

				<!-- Tab 2: User ID & Groups -->
				<Tabs.Content value="id-groups" class="space-y-3 pt-3">
					<CustomValueInput
						label="User ID (UID)"
						placeholder="1000"
						type="number"
						bind:value={properties.uid}
					/>
					<div class="flex items-center gap-2">
						<Checkbox id="new-primary-group" bind:checked={properties.newPrimaryGroup} />
						<Label for="new-primary-group" class="cursor-pointer text-sm"
							>Create User Primary Group</Label
						>
					</div>
					<CustomComboBox
						label="Primary Group"
						placeholder="Select primary group..."
						bind:open={properties.primaryGroup.open}
						bind:value={properties.primaryGroup.value}
						data={groupOptions}
						disabled={properties.newPrimaryGroup}
					/>
					<CustomComboBox
						label="Auxiliary Groups"
						placeholder="Select auxiliary groups..."
						bind:open={properties.auxGroups.open}
						bind:value={properties.auxGroups.value}
						data={groupOptions}
						multiple={true}
					/>
				</Tabs.Content>

				<!-- Tab 3: Directories & Permissions -->
				<Tabs.Content value="directories" class="space-y-3 pt-3">
					<div class="space-y-1.5">
						<CustomValueInput
							label="Home Directory"
							placeholder="/home/username"
							bind:value={properties.homeDirectory}
							topRightButton={{
								icon: 'icon-[mdi--home]',
								tooltip: 'Use /home/<username>',
								function: async () => {
									if (properties.username) {
										properties.homeDirectory = `/home/${properties.username}`;
									}
									return properties.homeDirectory;
								}
							}}
						/>
						<Button variant="outline" size="sm" class="shrink-0" onclick={openDirPicker}>
							<span class="icon-[mdi--folder-open-outline] mr-1 h-4 w-4"></span>
							Browse
						</Button>
					</div>
					<div class="space-y-2">
						<Label class="text-sm">Home Directory Permissions</Label>
						<div
							class="rounded-md border text-sm"
							class:opacity-50={properties.homeDirectory === '/nonexistent'}
						>
							<!-- header -->
							<div class="grid grid-cols-4 border-b">
								<div class="p-2"></div>
								<div class="p-2 text-center font-medium">Read</div>
								<div class="p-2 text-center font-medium">Write</div>
								<div class="p-2 text-center font-medium">Execute</div>
							</div>
							<!-- User row -->
							<div class="grid grid-cols-4 border-b">
								<div class="p-2 font-medium">User</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.user.read}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.user.write}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.user.exec}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
							</div>
							<!-- Group row -->
							<div class="grid grid-cols-4 border-b">
								<div class="p-2 font-medium">Group</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.group.read}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.group.write}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.group.exec}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
							</div>
							<!-- Other row -->
							<div class="grid grid-cols-4">
								<div class="p-2 font-medium">Other</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.other.read}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.other.write}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
								<div class="flex items-center justify-center p-2">
									<Checkbox
										bind:checked={properties.perms.other.exec}
										disabled={properties.homeDirectory === '/nonexistent'}
									/>
								</div>
							</div>
						</div>
						<p class="text-muted-foreground text-xs">
							Octal: 0{computedPerms.toString(8).padStart(3, '0')}
						</p>
					</div>
				</Tabs.Content>

				<!-- Tab 4: Authentication -->
				<Tabs.Content value="authentication" class="space-y-3 pt-3">
					<CustomValueInput
						label="SSH Public Key"
						placeholder="ssh-rsa AAAAB3NzaC1yc2E..."
						type="textarea"
						bind:value={properties.sshPublicKey}
					/>
					<CustomComboBox
						label="Shell"
						placeholder="Select shell..."
						bind:open={properties.shell.open}
						bind:value={properties.shell.value}
						data={shellOptions}
					/>
					<div class="grid grid-cols-2 gap-3">
						<div class="flex items-center gap-2">
							<Checkbox id="disable-password" bind:checked={properties.disablePassword} />
							<Label for="disable-password" class="cursor-pointer text-sm">Disable Password</Label>
						</div>
						<div class="flex items-center gap-2">
							<Checkbox id="lock-user" bind:checked={properties.locked} />
							<Label for="lock-user" class="cursor-pointer text-sm">Lock User</Label>
						</div>
						{#if doasAvailable}
							<div class="flex items-center gap-2">
								<Checkbox id="doas-enabled" bind:checked={properties.doasEnabled} />
								<Label for="doas-enabled" class="cursor-pointer text-sm">Permit Doas</Label>
							</div>
						{/if}
						<div class="flex items-center gap-2">
							<Checkbox id="admin" bind:checked={properties.admin} />
							<Label for="admin" class="cursor-pointer text-sm">Admin</Label>
						</div>
					</div>
				</Tabs.Content>
			</Tabs.Root>

			<div class="flex justify-end gap-2 pt-1">
				<Button
					variant="outline"
					onclick={() => {
						reset();
						open = false;
					}}>Cancel</Button
				>
				<Button onclick={submit}>{edit ? 'Save' : 'Create'}</Button>
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
