<script lang="ts">
	import { page } from '$app/state';
	import GuestGrants from '$lib/components/custom/Permissions/GuestGrants.svelte';
</script>

<GuestGrants path={`/jail/${page.params.ctid}`} />
