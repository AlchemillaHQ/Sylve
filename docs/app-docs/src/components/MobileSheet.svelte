<script lang="ts">
  import { onDestroy } from "svelte";
  import { fade, fly } from "svelte/transition";

  let open = $state(false);

  $effect(() => {
    if (typeof document === "undefined") return;
    document.body.style.overflow = open ? "hidden" : "";
    return () => (document.body.style.overflow = "");
  });

  onDestroy(() => {
    if (typeof document !== "undefined") document.body.style.overflow = "";
  });

  const close = () => (open = false);
</script>

<div class="mobile-menu">
  <button class="mobile-menu-trigger" onclick={() => (open = true)} aria-label="Open menu">
    <span class="icon-[lucide--menu] size-5"></span>
  </button>

  {#if open}
    <button class="mobile-menu-backdrop" transition:fade={{ duration: 140 }} onclick={close} aria-label="Close menu"></button>
    <aside class="mobile-menu-panel" transition:fly={{ x: 320, duration: 200 }}>
      <div class="mobile-menu-heading">
        <span>Navigation</span>
        <button onclick={close} aria-label="Close menu"><span class="icon-[lucide--x] size-5"></span></button>
      </div>
      <nav>
        <a href="/#capabilities" onclick={close}><small>01</small>Product</a>
        <a href="/docs/" data-astro-reload onclick={close}><small>02</small>Documentation</a>
        <a href="/blog" onclick={close}><small>03</small>Blog</a>
        <a href="https://github.com/AlchemillaHQ/Sylve" target="_blank" rel="noopener noreferrer" onclick={close}><small>04</small>GitHub</a>
      </nav>
      <div class="mobile-menu-footer">
        <button type="button" data-theme-toggle>
          <span class="icon-[lucide--sun] size-4 dark:hidden"></span>
          <span class="icon-[lucide--moon] hidden size-4 dark:block"></span>
          Change appearance
        </button>
        <button type="button" class="mobile-sponsor" data-sponsor-open onclick={close}>
          Sponsor Sylve
          <span class="icon-[lucide--heart-handshake] size-4" aria-hidden="true"></span>
        </button>
        <a href="/getting-started/" data-astro-reload onclick={close}>Get started <span>↗</span></a>
      </div>
    </aside>
  {/if}
</div>

<style>
  .mobile-menu { display: none; }
  .mobile-menu-trigger, .mobile-menu-heading button { display: grid; place-items: center; width: 2.4rem; height: 2.4rem; border: 1px solid color-mix(in oklab, var(--foreground) 14%, transparent); border-radius: 7px; background: transparent; color: var(--foreground); }
  .mobile-menu-backdrop { position: fixed; inset: 0; z-index: 70; border: 0; background: rgb(0 0 0 / 55%); }
  .mobile-menu-panel { position: fixed; top: 0; right: 0; z-index: 80; display: flex; flex-direction: column; width: min(90vw, 25rem); height: 100dvh; padding: 1.25rem; overflow-y: auto; background: var(--background); color: var(--foreground); border-left: 1px solid color-mix(in oklab, var(--foreground) 14%, transparent); }
  .mobile-menu-heading { display: flex; align-items: center; justify-content: space-between; padding-bottom: 1.25rem; border-bottom: 1px solid color-mix(in oklab, var(--foreground) 12%, transparent); font: .7rem "IBM Plex Mono", monospace; letter-spacing: .1em; text-transform: uppercase; }
  nav { display: grid; margin-top: 2rem; }
  nav a { display: grid; grid-template-columns: 2.2rem 1fr; align-items: center; padding: 1rem 0; border-bottom: 1px solid color-mix(in oklab, var(--foreground) 10%, transparent); color: var(--foreground); font-size: 1.45rem; letter-spacing: -.03em; text-decoration: none; }
  nav small { color: var(--muted-foreground); font: .6rem "IBM Plex Mono", monospace; }
  .mobile-menu-footer { display: grid; gap: .8rem; margin-top: auto; }
  .mobile-menu-footer button, .mobile-menu-footer a { display: flex; align-items: center; justify-content: space-between; gap: .6rem; min-height: 2.8rem; padding: 0 .85rem; border: 1px solid color-mix(in oklab, var(--foreground) 14%, transparent); border-radius: 7px; background: transparent; color: var(--foreground); font-size: .78rem; text-decoration: none; }
  .mobile-menu-footer button:not(.mobile-sponsor) { justify-content: flex-start; }
  .mobile-menu-footer .mobile-sponsor { background: color-mix(in oklab, var(--foreground) 5%, transparent); }
  .mobile-menu-footer a { background: var(--foreground); color: var(--background); }
  @media (max-width: 900px) { .mobile-menu { display: block; } }
</style>
