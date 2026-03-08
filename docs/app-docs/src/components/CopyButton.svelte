<script lang="ts">
  type Props = {
    text: string;
  };

  const { text }: Props = $props();

  type Status = "idle" | "loading" | "success";
  let status = $state<Status>("idle");

  async function handleCopy() {
    try {
      status = "loading";

      if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const textarea = document.createElement("textarea");
        textarea.value = text;
        document.body.appendChild(textarea);
        textarea.select();
        document.execCommand("copy");
        document.body.removeChild(textarea);
      }

      setTimeout(() => {
        status = "success";
      }, 500);

      setTimeout(() => {
        status = "idle";
      }, 3000);
    } catch (error) {
      console.error("Failed to copy:", error);
      status = "idle";
    }
  }
</script>

<button
  onclick={handleCopy}
  title="Copy to clipboard"
  class="flex items-center rounded-md px-1.5 py-1 text-sm hover:bg-muted disabled:opacity-50 cursor-pointer"
  disabled={status === "loading"}
>
  {#if status === "idle"}
    <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
    </svg>
  {:else if status === "loading"}
    <svg class="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M21 12a9 9 0 1 1-6.219-8.56"></path>
    </svg>
  {:else}
    <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M20 6 9 17l-5-5"></path>
    </svg>
  {/if}
</button>