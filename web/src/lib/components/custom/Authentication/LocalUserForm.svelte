<script lang="ts">
	import { createUser, editUser } from '$lib/api/auth/local';
	import Button from '$lib/components/ui/button/button.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { User } from '$lib/types/auth';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidEmail, isValidUsername } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		users: User[];
		user?: User;
		edit?: boolean;
		reload?: boolean;
	}

	let {
		open = $bindable(),
		users,
		user,
		edit = false,
		reload = $bindable()
	}: Props = $props();

	function makeDefaults() {
		return {
			fullName: edit && user ? (user.fullName ?? '') : '',
			username: edit && user ? user.username : '',
			email: edit && user ? (user.email ?? '') : '',
			password: '',
			confirmPassword: '',
			admin: edit && user ? user.admin : false
		};
	}

	let properties = $state(makeDefaults());
	let loading = $state(false);

	function reset() {
		properties = makeDefaults();
	}

	function validate(): string {
		if (!properties.username) return 'Username is required';
		if (!(edit && user && properties.username === user.username) && !isValidUsername(properties.username))
			return 'Invalid username format';
		if (!edit && users.some((u) => u.username === properties.username))
			return 'Username already exists';
		if (edit && user && users.some((u) => u.id !== user!.id && u.username === properties.username))
			return 'Username already exists';
		if (properties.email && !isValidEmail(properties.email)) return 'Invalid email address';
		if (!edit) {
			if (!properties.password) return 'Password is required';
			if (properties.password.length < 8) return 'Password must be at least 8 characters';
		}
		if (properties.password && properties.confirmPassword !== properties.password)
			return 'Passwords do not match';

		return '';
	}

	async function submit() {
		loading = true;

		const error = validate();
		if (error) {
			toast.error(error, { position: 'bottom-center' });
			loading = false;
			return;
		}

		const payload = {
			fullName: properties.fullName,
			username: properties.username,
			email: properties.email,
			password: properties.password,
			admin: properties.admin
		};

		let response;
		if (edit && user) {
			response = await editUser(user.id, payload);
		} else {
			response = await createUser(payload);
		}

		reload = true;
		loading = false;

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
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon={!edit ? 'icon-[mdi--user-plus]' : 'icon-[mdi--user-edit]'}
					size="h-5 w-5"
					gap="gap-2"
					title={!edit ? 'Create User' : `Edit User - ${user?.username}`}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-3 overflow-y-auto pt-3">
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
			<div class="flex items-center gap-2 pt-2">
				<Checkbox id="admin" bind:checked={properties.admin} />
				<Label for="admin" class="cursor-pointer text-sm">Admin</Label>
			</div>
		</div>

		<div class="flex justify-end pt-1">
			<Button onclick={submit} disabled={loading}
				>{loading ? (edit ? 'Saving…' : 'Creating…') : edit ? 'Save' : 'Create'}</Button
			>
		</div>
	</Dialog.Content>
</Dialog.Root>
