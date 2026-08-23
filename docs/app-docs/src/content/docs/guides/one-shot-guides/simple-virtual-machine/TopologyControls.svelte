<script>
  import { Panel, useSvelteFlow } from "@xyflow/svelte";

  let {
    hostPortCount,
    vmCount,
    maxHostPorts,
    maxVms,
    onAddHostPort,
    onAddVm,
    onReset,
    guestLabel = "VM",
  } = $props();

  const { fitView } = useSvelteFlow();

  const runAndRefit = (action) => {
    action();
    requestAnimationFrame(() => {
      fitView({ padding: 0.12, duration: 350, maxZoom: 0.9 });
    });
  };
</script>

<Panel position="top-right">
  <div class="controls" aria-label="Topology controls">
    <button
      type="button"
      onclick={() => runAndRefit(onAddHostPort)}
      disabled={hostPortCount >= maxHostPorts}
    >
      <span>+</span> Host port
    </button>
    <button
      type="button"
      onclick={() => runAndRefit(onAddVm)}
      disabled={vmCount >= maxVms}
    >
      <span>+</span> {guestLabel}
    </button>
    <button class="reset" type="button" onclick={() => runAndRefit(onReset)}>Reset</button>
  </div>
</Panel>

<style>
  .controls {
    display: flex;
    gap: 0.35rem;
    padding: 0.35rem;
    border: 1px solid var(--flow-line);
    border-radius: 0.25rem;
    background: color-mix(in oklab, var(--sl-color-bg) 94%, transparent);
    backdrop-filter: blur(8px);
  }
  button {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    min-height: 1.9rem;
    padding: 0 0.65rem;
    border: 1px solid var(--flow-line);
    border-radius: 0.15rem;
    background: color-mix(in oklab, var(--sl-color-white) 4%, transparent);
    color: var(--sl-color-white);
    font: 500 0.58rem var(--sl-font-mono);
    cursor: pointer;
  }
  button:hover:not(:disabled) {
    border-color: var(--flow-strong);
    background: color-mix(in oklab, var(--sl-color-white) 8%, transparent);
  }
  button:focus-visible { outline: 2px solid var(--sl-color-white); outline-offset: 2px; }
  button:disabled { cursor: not-allowed; opacity: 0.35; }
  button span { font-size: 0.85rem; line-height: 1; }
  .reset { color: var(--flow-muted); }
  @media (max-width: 34rem) {
    .controls { flex-wrap: wrap; justify-content: flex-end; max-width: 12rem; }
  }
</style>
