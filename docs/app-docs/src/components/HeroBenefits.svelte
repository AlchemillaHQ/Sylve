<script lang="ts">
  import { onMount } from "svelte";

  const benefits = [
    {
      title: "Built on FreeBSD",
      text: "We make orchestration simple, and the incredibly robust base system does the rest.",
    },
    {
      title: "Minimal footprint",
      text: "A basic deployment runs with less than 384 MB of memory and one vCPU.",
    },
    {
      title: "Blazingly fast",
      text: "Our backend is written in Go, with frontend in Svelte, with a focus on speed.",
    },
    {
      title: "No strings attached",
      text: "BSD-2-Clause licensed, with no proprietary core or artificial restrictions.",
    },
  ];
  const cycleDurationMs = 8000;

  let activeIndex = $state(0);
  let cycle = $state(0);
  let paused = $state(false);
  const activeBenefit = $derived(benefits[activeIndex]);
  let timer: number | undefined;
  let timerStartedAt = 0;
  let remainingMs = cycleDurationMs;
  let mounted = false;
  let reducedMotion = false;
  let pointerPaused = false;
  let focusPaused = false;

  function scheduleNext(delay = cycleDurationMs) {
    window.clearTimeout(timer);
    remainingMs = delay;
    timerStartedAt = performance.now();
    timer = window.setTimeout(() => {
      timer = undefined;
      activeIndex = (activeIndex + 1) % benefits.length;
      cycle += 1;
      scheduleNext();
    }, delay);
  }

  function selectBenefit(index: number) {
    activeIndex = index;
    cycle += 1;
    remainingMs = cycleDurationMs;
    if (mounted && !reducedMotion && !paused) scheduleNext();
  }

  function setPauseReason(reason: "pointer" | "focus", active: boolean) {
    if (reason === "pointer") pointerPaused = active;
    else focusPaused = active;

    const nextPaused = pointerPaused || focusPaused;
    if (nextPaused === paused) return;
    paused = nextPaused;

    if (!mounted || reducedMotion) return;
    if (paused) {
      if (timer !== undefined) {
        remainingMs = Math.max(0, remainingMs - (performance.now() - timerStartedAt));
        window.clearTimeout(timer);
        timer = undefined;
      }
    } else {
      scheduleNext(remainingMs);
    }
  }

  onMount(() => {
    mounted = true;
    reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (!reducedMotion) scheduleNext();

    return () => {
      mounted = false;
      window.clearTimeout(timer);
    };
  });
</script>

<div
  class="benefit-rotator"
  role="group"
  aria-label="Sylve benefits"
  style:--cycle-duration={`${cycleDurationMs}ms`}
  onmouseenter={() => setPauseReason("pointer", true)}
  onmouseleave={() => setPauseReason("pointer", false)}
  onfocusin={() => setPauseReason("focus", true)}
  onfocusout={() => setPauseReason("focus", false)}
>
  <div class="benefit-track" aria-hidden="true">
    {#key cycle}
      <span class:paused></span>
    {/key}
  </div>

  <div class="benefit-copy" aria-live="polite">
    <p>{activeBenefit.title}</p>
    <span>{activeBenefit.text}</span>
  </div>

  <div class="benefit-nav" aria-label="Sylve benefits">
    <span>{String(activeIndex + 1).padStart(2, "0")} / {String(benefits.length).padStart(2, "0")}</span>
    <div>
      {#each benefits as benefit, index (benefit.title)}
        <button
          type="button"
          class:active={activeIndex === index}
          aria-label={`Show: ${benefit.title}`}
          aria-pressed={activeIndex === index}
          onclick={() => selectBenefit(index)}
        ></button>
      {/each}
    </div>
  </div>
</div>

<style>
  .benefit-rotator {
    align-self: end;
    width: 100%;
    max-width: 17rem;
  }

  .benefit-track {
    height: 2px;
    overflow: hidden;
    background: color-mix(in oklab, var(--foreground) 13%, transparent);
  }

  .benefit-track span {
    display: block;
    width: 100%;
    height: 100%;
    background: var(--foreground);
    transform-origin: left;
    animation: benefit-progress var(--cycle-duration) linear forwards;
  }

  .benefit-track span.paused { animation-play-state: paused; }
  .benefit-copy { min-height: 7.4rem; padding-top: 1.05rem; }
  .benefit-copy p { margin: 0; font-size: .82rem; font-weight: 650; letter-spacing: -.01em; }
  .benefit-copy span { display: block; margin-top: .65rem; color: var(--muted-foreground); font-size: .76rem; line-height: 1.6; }
  .benefit-nav { display: flex; align-items: center; justify-content: space-between; gap: 1rem; color: var(--muted-foreground); font: .58rem "IBM Plex Mono", ui-monospace, monospace; }
  .benefit-nav > div { display: flex; gap: .35rem; }
  .benefit-nav button { width: 1.15rem; height: 1.15rem; padding: 0; border: 0; background: transparent; cursor: pointer; }
  .benefit-nav button::before { content: ""; display: block; width: 100%; height: 1px; background: color-mix(in oklab, var(--foreground) 24%, transparent); transition: background 160ms ease; }
  .benefit-nav button:hover::before,
  .benefit-nav button.active::before { background: var(--foreground); }

  @keyframes benefit-progress {
    from { transform: scaleX(0); }
    92%,
    to { transform: scaleX(1); }
  }

  @media (max-width: 900px) {
    .benefit-rotator { max-width: 32rem; margin-top: .4rem; }
  }

  @media (prefers-reduced-motion: reduce) {
    .benefit-track span { animation: none; transform: scaleX(1); }
  }
</style>
