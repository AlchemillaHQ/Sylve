<script lang="ts">
  import ViewInstaller from './ViewInstaller.svelte';

  let open = $state(false);
</script>

<ViewInstaller bind:open />

<button
  onclick={() => (open = true)}
  title="View installer"
  class="flex items-center rounded-md px-1.5 py-1 text-sm hover:bg-muted cursor-pointer"
>
  <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"></path>
    <circle cx="12" cy="12" r="3"></circle>
  </svg>
</button>
