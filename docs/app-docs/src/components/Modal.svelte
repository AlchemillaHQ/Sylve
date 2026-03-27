<script lang="ts">
  import { fade, scale } from 'svelte/transition';
  import { onDestroy } from 'svelte';

  let { open = $bindable(), header, footer, children } = $props();

  // Portal action — moves the node to document.body so it escapes any
  // transformed/stacking-context ancestor in the component tree.
  function portal(node: HTMLElement) {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }

  $effect(() => {
    if (typeof document === 'undefined') return;
    document.body.style.overflow = open ? 'hidden' : '';
    return () => { document.body.style.overflow = ''; };
  });

  onDestroy(() => {
    if (typeof document !== 'undefined') document.body.style.overflow = '';
  });
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_noninteractive_element_interactions -->
  <div use:portal>
    <div
      class="fixed inset-0 z-[9998] bg-black/50 backdrop-blur-sm"
      transition:fade={{ duration: 180 }}
      onclick={() => (open = false)}
    ></div>

    <div
      class="fixed inset-0 z-[9999] flex items-center justify-center p-4 pointer-events-none"
      transition:fade={{ duration: 100 }}
    >
      <div
        class="pointer-events-auto w-full max-w-2xl rounded-xl border border-border bg-background text-foreground shadow-2xl flex flex-col max-h-[90vh]"
        in:scale={{ start: 0.96, duration: 200 }}
        out:scale={{ start: 0.96, duration: 150 }}
      >
        {#if header}
          <div class="flex items-center justify-between border-b border-border px-5 py-4 shrink-0">
            {@render header()}
            <button
              onclick={() => (open = false)}
              class="ml-4 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              aria-label="Close"
            >
              <span class="icon-[lucide--x] size-4"></span>
            </button>
          </div>
        {/if}
        <div class="px-5 py-4 overflow-y-auto">
          {@render children?.()}
        </div>
        {#if footer}
          <div class="flex items-center justify-between gap-3 border-t border-border px-5 py-3 shrink-0">
            {@render footer()}
          </div>
        {/if}
      </div>
    </div>
  </div>
{/if}
