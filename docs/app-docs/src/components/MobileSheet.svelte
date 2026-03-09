<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { fade, fly } from "svelte/transition";

  const labels = {
    menu: "Menu",
    blog: "Blog",
    docs: "Documentation",
    starGithub: "Star on Github",
    colorMode: "Theme",
    toggleTheme: "Toggle",
  };

  let open = $state(false);

  const setBodyScrollLock = (locked: boolean) => {
    if (typeof document === "undefined") return;
    document.body.style.overflow = locked ? "hidden" : "";
    document.body.style.touchAction = locked ? "none" : "";
  };

  onMount(() => {});

  $effect(() => {
    setBodyScrollLock(open);

    return () => {
      setBodyScrollLock(false);
    };
  });

  onDestroy(() => {
    setBodyScrollLock(false);
  });
</script>

<div class="md:hidden">
  <button
    class="inline-flex h-9 w-9 items-center justify-center rounded-md border bg-background text-foreground shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground"
    onclick={() => {
      open = true;
    }}
    aria-label="Toggle menu"
  >
    <span class="icon-[lucide--menu] size-6"></span>
  </button>

  {#if open}
    <button
      class="fixed inset-0 z-60 bg-black/70 backdrop-blur-[1px]"
      transition:fade={{ duration: 180 }}
      onclick={() => {
        open = false;
      }}
      aria-label="Close menu overlay"
    ></button>

    <aside
      class="fixed inset-y-0 right-0 z-70 h-full w-[84%] max-w-sm border-l border-border bg-background text-foreground shadow-2xl"
      transition:fly={{ x: 320, duration: 220, opacity: 0.2 }}
    >
      <div class="flex h-full flex-col">
        <div
          class="flex items-center justify-between border-b border-border px-4 py-4"
        >
          <h2 class="text-left text-base font-semibold tracking-tight">
            {labels.menu}
          </h2>
          <button
            class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-background text-foreground transition-colors hover:bg-accent"
            onclick={() => {
              open = false;
            }}
            aria-label="Close menu"
          >
            <span class="icon-[lucide--x] size-4"></span>
          </button>
        </div>

        <nav class="flex flex-col gap-1 px-2 py-3">
          <a
            href="/docs/"
            class="inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium text-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            onclick={() => {
              open = false;
            }}
          >
            {labels.docs}
          </a>
          <a
            href="/blog"
            class="inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium text-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            onclick={() => {
              open = false;
            }}
          >
            {labels.blog}
          </a>
          <a
            href="https://github.com/AlchemillaHQ/Sylve"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium text-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            onclick={() => {
              open = false;
            }}
          >
            <span class="icon-[mdi--github] size-4"></span>
            <span>{labels.starGithub}</span>
          </a>
        </nav>

        <div class="mt-auto border-t border-border p-4">
          <button
            type="button"
            data-theme-toggle
            class="inline-flex w-full items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-left text-sm font-medium text-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            <span class="icon-[lucide--sun] size-4 dark:hidden"></span>
            <span class="icon-[lucide--moon] hidden size-4 dark:block"></span>
            <span>{labels.colorMode} {labels.toggleTheme}</span>
          </button>
        </div>
      </div>
    </aside>
  {/if}
</div>
