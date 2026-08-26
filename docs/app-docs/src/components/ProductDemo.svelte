<script lang="ts">
  import { onMount } from "svelte";
  import Throbber from "../../../../web/src/lib/components/custom/Throbber.svelte";

  const demoUrl =
    import.meta.env.PUBLIC_SYLVE_DEMO_URL ||
    (import.meta.env.DEV
      ? "http://localhost:5173/datacenter/summary"
      : "https://demo.sylve.io/datacenter/summary");

  const designWidth = 1440;
  const designHeight = 860;
  const minimumLoaderDuration = 650;

  let container: HTMLDivElement;
  let demoFrame: HTMLIFrameElement;
  let loaded = $state(false);
  let scale = $state(1);
  let loaderStartedAt = 0;
  let revealTimer: number | undefined;

  const currentTheme = (): "light" | "dark" =>
    document.documentElement.classList.contains("dark") ? "dark" : "light";

  const syncDemoTheme = () => {
    demoFrame?.contentWindow?.postMessage(
      { type: "sylve-demo-theme", theme: currentTheme() },
      new URL(demoUrl, window.location.href).origin,
    );
  };

  const revealDemo = () => {
    if (loaded) return;

    const remaining =
      loaderStartedAt === 0
        ? 0
        : Math.max(
            0,
            minimumLoaderDuration - (performance.now() - loaderStartedAt),
          );

    window.clearTimeout(revealTimer);
    revealTimer = window.setTimeout(() => {
      loaded = true;
    }, remaining);
  };

  const handleDemoReady = (event: MessageEvent) => {
    if (
      event.source === demoFrame?.contentWindow &&
      event.data?.type === "sylve-demo-ready"
    ) {
      syncDemoTheme();
      revealDemo();
    }
  };

  onMount(() => {
    loaderStartedAt = performance.now();

    const observer = new ResizeObserver(([entry]) => {
      scale = Math.min(1, entry.contentRect.width / designWidth);
    });

    const themeObserver = new MutationObserver(syncDemoTheme);

    observer.observe(container);
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "data-theme"],
    });
    window.addEventListener("message", handleDemoReady);

    return () => {
      window.clearTimeout(revealTimer);
      observer.disconnect();
      themeObserver.disconnect();
      window.removeEventListener("message", handleDemoReady);
    };
  });
</script>

<section class="demo-section" aria-labelledby="demo-title">
  <div class="demo-intro">
    <div class="demo-copy">
      <p class="demo-kicker">
        <span class="demo-status" aria-hidden="true"></span>
        Interactive product demo
      </p>
      <h2 id="demo-title">Explore Sylve.</h2>
      <p>A working three-node cluster, right here in your browser.</p>
    </div>

  </div>

  <div class="demo-frame">
    <header class="demo-meta">
      <div>
        <span class="demo-meta-mark" aria-hidden="true"></span>
        <span>Interactive Sandbox</span>
      </div>
      <div class="demo-meta-context">
        <span>3 nodes</span>
        <span class="demo-note">Sample data</span>
        <a
          class="demo-launch"
          href={demoUrl}
          target="_blank"
          rel="noopener noreferrer"
        >
          Open full
          <span class="icon-[lucide--arrow-up-right]" aria-hidden="true"></span>
        </a>
      </div>
    </header>

    <div
      class="demo-container"
      bind:this={container}
      style={`--demo-scale:${scale}; --demo-height:${designHeight * scale}px`}
    >
      {#if !loaded}
        <div class="demo-loading" aria-live="polite" aria-label="Loading Sylve demo">
          <Throbber />
        </div>
      {/if}
      <iframe
        bind:this={demoFrame}
        src={demoUrl}
        title="Sylve web interface demo"
        width={designWidth}
        height={designHeight}
        class:loaded
        onload={() => {
          syncDemoTheme();
          revealDemo();
        }}
        allow="fullscreen"
        sandbox="allow-scripts allow-same-origin allow-forms allow-modals allow-downloads allow-pointer-lock"
        referrerpolicy="no-referrer"
      ></iframe>
    </div>
  </div>
</section>

<style>
  .demo-section {
    display: grid;
    gap: 1.4rem;
  }

  .demo-intro {
    padding-inline: 0.1rem;
  }

  .demo-copy {
    max-width: 47rem;
  }

  .demo-kicker {
    display: flex;
    align-items: center;
    gap: 0.55rem;
    margin: 0;
    color: var(--muted-foreground);
    font: 600 0.66rem/1.3 "IBM Plex Mono", "SFMono-Regular", monospace;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .demo-copy h2 {
    margin: 0.7rem 0 0;
    color: var(--foreground);
    font-size: clamp(2rem, 4vw, 3.7rem);
    font-weight: 480;
    line-height: 1;
    letter-spacing: -0.055em;
  }

  .demo-copy > p:last-child {
    max-width: 42rem;
    margin: 1rem 0 0;
    color: var(--muted-foreground);
    font-size: clamp(0.9rem, 1.4vw, 1.02rem);
    line-height: 1.65;
  }

  .demo-launch {
    display: inline-flex;
    flex: none;
    align-items: center;
    justify-content: center;
    gap: 0.4rem;
    min-height: 1.85rem;
    padding: 0 0.65rem;
    border-radius: 5px;
    background: var(--foreground);
    color: var(--background);
    font-size: 0.67rem;
    font-weight: 650;
    text-decoration: none;
    transition:
      background 160ms ease,
      transform 160ms ease;
  }

  .demo-launch:hover {
    background: color-mix(in oklab, var(--foreground) 86%, var(--background));
    transform: translateY(-1px);
  }

  .demo-launch span {
    width: 0.75rem;
    height: 0.75rem;
  }

  .demo-frame {
    overflow: hidden;
    border: 1px solid color-mix(in oklab, var(--sl-color-gray-5) 82%, transparent);
    border-radius: 12px;
    background: var(--sl-color-black);
    box-shadow:
      0 32px 90px rgb(0 0 0 / 20%),
      0 1px 0 rgb(255 255 255 / 5%) inset;
  }

  .demo-meta {
    display: flex;
    min-height: 2.8rem;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0 0.9rem;
    border-bottom: 1px solid color-mix(in oklab, var(--sl-color-gray-5) 78%, transparent);
    background: color-mix(in oklab, var(--sl-color-black) 96%, var(--sl-color-white) 4%);
    color: var(--sl-color-gray-2);
    font-size: 0.72rem;
    letter-spacing: 0.01em;
  }

  .demo-meta > div {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .demo-status {
    width: 0.42rem;
    height: 0.42rem;
    flex: none;
    border-radius: 999px;
    background: #4ade80;
    box-shadow: 0 0 0 3px rgb(74 222 128 / 10%);
  }

  .demo-meta-mark {
    width: 0.65rem;
    height: 0.65rem;
    border: 1px solid currentColor;
    border-radius: 2px;
    opacity: 0.65;
  }

  .demo-meta-context {
    color: var(--sl-color-gray-3);
  }

  .demo-note {
    color: var(--sl-color-gray-3);
  }

  .demo-note::before {
    content: "·";
    margin-right: 0.5rem;
  }

  .demo-container {
    position: relative;
    height: var(--demo-height);
    min-height: 0;
    overflow: hidden;
    background: #09090b;
  }

  iframe {
    position: absolute;
    inset: 0 auto auto 0;
    display: block;
    width: 1440px;
    height: 860px;
    border: 0;
    opacity: 0;
    transform: scale(var(--demo-scale));
    transform-origin: top left;
    transition: opacity 240ms ease;
  }

  iframe.loaded {
    opacity: 1;
  }

  .demo-loading {
    position: absolute;
    inset: 0;
    z-index: 1;
    --throbber-primary: #4c4847;
    background: #09090b;
  }

  :global(.demo-loading > div) {
    display: flex;
    width: 100%;
    height: 100%;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.55rem;
    overflow: hidden;
  }

  :global(.demo-loading #thrb) {
    width: clamp(3.5rem, 8vw, 6rem);
    height: auto;
  }

  :global(.demo-loading p) {
    width: auto;
    margin: 0 0 0 0.3rem;
    color: #d4d4d8;
    font-size: clamp(0.48rem, 0.8vw, 0.68rem);
    font-weight: 500;
    letter-spacing: 0.45em;
  }

  @media (max-width: 40rem) {
    .demo-section {
      gap: 1.1rem;
    }

    .demo-copy h2 {
      font-size: clamp(1.9rem, 10vw, 2.7rem);
    }

    .demo-meta {
      min-height: 2.6rem;
      padding-inline: 0.7rem;
      font-size: 0.66rem;
    }

    .demo-note {
      display: none;
    }

    .demo-frame {
      border-radius: 8px;
    }
  }

</style>
