<script lang="ts">
	import { createUser, editUser } from '$lib/api/auth/local';
	import Button from '$lib/components/ui/button/button.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { User } from '$lib/types/auth';
	import { handleAPIError } from '$lib/utils/http';
	import { generatePassword, isValidEmail, isValidUsername } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		users: User[];
		user?: User;
		edit?: boolean;
		reload?: boolean;
	}

	let { open = $bindable(), users, user, edit = false, reload = $bindable() }: Props = $props();
	let options = {
		username: edit && user ? user.username : '',
		email: edit && user ? user.email : '',
		password: '',
		admin: edit && user ? user.admin : false
	};

	let properties = $state(options);

	async function create() {
		let error = '';
		if (!properties.username || !properties.password) {
			error = 'Username and password are required';
		} else if (!isValidUsername(properties.username)) {
			error = 'Invalid username';
		} else if (users.some((user) => user.username === properties.username)) {
			error = 'Username already exists';
		} else if (properties.email && !isValidEmail(properties.email)) {
			error = 'Invalid email address';
		} else if (properties.password.length < 8) {
			error = 'Password must be at least 8 characters long';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});

			return;
		}

		const response = await createUser(
			properties.username,
			properties.email,
			properties.password,
			properties.admin
		);

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to create user', {
				position: 'bottom-center'
			});
		} else {
			toast.success('User created', {
				position: 'bottom-center'
			});
			open = false;
			properties = options;
		}
	}

	async function change() {
		if (!user) {
			toast.error('No user selected for editing', {
				position: 'bottom-center'
			});
			return;
		}

		let error = '';
		if (!properties.username || !isValidUsername(properties.username)) {
			error = 'Invalid username';
		} else if (users.some((u) => u.id !== user?.id && u.username === properties.username)) {
			error = 'Username already exists';
		} else if (properties.email && !isValidEmail(properties.email)) {
			error = 'Invalid email address';
		} else if (properties.password && properties.password.length < 8) {
			error = 'Password must be at least 8 characters long';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		const response = await editUser(
			user.id,
			properties.username,
			properties.email,
			properties.password,
			properties.admin
		);

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to edit user', {
				position: 'bottom-center'
			});
		} else {
			toast.success('User edited', {
				position: 'bottom-center'
			});
			open = false;
			properties = options;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={() => {
			properties = options;
			open = false;
		}}
		class="lg:max-w-2x w-[90%] gap-4 p-5"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between  text-left">
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
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => {
							properties = options;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>

					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="icon-[mdi--user-edit] h-5 w-5"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<CustomValueInput
				label={'Username'}
				placeholder="hayzam"
				bind:value={properties.username}
				classes="flex-1 space-y-1.5"
			/>

			<CustomValueInput
				label={'E-Mail'}
				placeholder="hayzam@alchemilla.io"
				bind:value={properties.email}
				classes="flex-1 space-y-1.5"
			/>
		</div>

		<div class="grid grid-cols-1">
			<div class="space-y-1">
				<Label class="w-24 text-sm whitespace-nowrap">Password</Label>
				<div class="flex w-full items-center space-x-2">
					<Input
						type="password"
						id="vnc-password"
						placeholder="Enter or generate password"
						class="w-full"
						autocomplete="off"
						bind:value={properties.password}
						showPasswordOnFocus={true}
					/>

					<Button
						onclick={() => {
							properties.password = generatePassword();
						}}
					>
						<span class="icon-[fad--random-2dice] h-6 w-6"></span>
					</Button>
				</div>
			</div>
		</div>

		<div class="flex items-center space-x-2">
			<Checkbox id="admin" bind:checked={properties.admin} />
			<Label for="admin" class="text-sm font-medium">Admin</Label>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if !edit}
					<Button onclick={create} type="submit" size="sm">Create</Button>
				{:else}
					<Button onclick={change} type="submit" size="sm">Save</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
