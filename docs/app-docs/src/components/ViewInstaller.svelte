<script lang="ts">
  import { onMount } from "svelte";
  import Modal from "./Modal.svelte";
  import CopyButton from "./CopyButton.svelte";

  interface Props {
    open: boolean;
  }

  let { open = $bindable() }: Props = $props();

  const SCRIPT_URL =
    "https://raw.githubusercontent.com/AlchemillaHQ/Sylve/refs/heads/master/scripts/installer.sh";

  let script = $state("");
  let highlighted = $state("");
  let error = $state("");

  onMount(async () => {
    try {
      const res = await fetch(SCRIPT_URL);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      script = await res.text();

      const [
        { createHighlighterCore },
        { createOnigurumaEngine },
        { default: githubLight },
        { default: githubDark },
        { default: langBash },
      ] = await Promise.all([
        import("shiki/core"),
        import("shiki/engine/oniguruma"),
        import("shiki/themes/github-light.mjs"),
        import("shiki/themes/github-dark.mjs"),
        import("shiki/langs/bash.mjs"),
      ]);

      const highlighter = await createHighlighterCore({
        themes: [githubLight, githubDark],
        langs: [langBash],
        engine: createOnigurumaEngine(import("shiki/wasm")),
      });

      highlighted = highlighter.codeToHtml(script, {
        lang: "bash",
        themes: { light: "github-light", dark: "github-dark" },
        defaultColor: "light",
      });

      highlighter.dispose();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load script";
    }
  });
</script>

<Modal bind:open>
  {#snippet header()}
    <div>
      <h2 class="text-base font-semibold">Install Sylve</h2>
      <p class="text-sm text-muted-foreground mt-0.5">
        FreeBSD &bull; BSD-2 License
      </p>
    </div>
  {/snippet}

  <div class="flex flex-col gap-3">
    <p class="text-sm text-muted-foreground">
      Sylve runs on FreeBSD and the script below will help you get up and
      running!
    </p>

    <div
      class="relative rounded-lg border border-border overflow-hidden text-sm"
    >
      {#if error}
        <div class="p-4 text-sm text-destructive font-mono">
          Failed to load script: {error}
        </div>
      {:else if highlighted}
        <div class="absolute top-2 right-2 z-10">
          <CopyButton text={script} />
        </div>
        <div
          class="shiki-block [&>pre]:!m-0 [&>pre]:p-4 [&>pre]:overflow-x-auto [&>pre]:leading-relaxed"
        >
          {@html highlighted}
        </div>
      {:else}
        <div
          class="flex items-center gap-2 p-4 text-sm text-muted-foreground font-mono"
        >
          <span class="icon-[lucide--loader-circle] size-4 animate-spin"></span>
          Loading installer script…
        </div>
      {/if}
    </div>
  </div>

  {#snippet footer()}
    <p class="text-xs text-muted-foreground">
      After installation, open <span class="font-mono text-foreground"
        >https://&lt;host&gt;:8181</span
      > in your browser.
    </p>
    <CopyButton text="fetch -o- https://sh.sylve.io | sh" />
  {/snippet}
</Modal>

<style>
  /* With defaultColor:'light', Shiki bakes light colors as inline styles and
     stores dark colors as --shiki-dark / --shiki-dark-bg custom properties.
     Light mode: leave inline styles untouched.
     Dark mode: override with the dark-theme variables. */
  :global(.dark .shiki-block .shiki) {
    background-color: var(--shiki-dark-bg) !important;
    color: var(--shiki-dark) !important;
  }
  :global(.dark .shiki-block .shiki span) {
    color: var(--shiki-dark) !important;
  }
</style>
