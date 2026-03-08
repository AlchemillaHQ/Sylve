<script lang="ts">
  import { onMount } from "svelte";
  import { register } from "swiper/element/bundle";

  type Props = {
    screenshotsLight?: string[];
    screenshotsDark?: string[];
  };

  const { screenshotsLight = [], screenshotsDark = [] }: Props = $props();

  let frameEl = $state<HTMLDivElement | null>(null);
  let inView = $state(false);
  let activeIndex = $state(0);
  let swiperEl = $state<HTMLElement | null>(null);
  let activeTheme = $state<"light" | "dark">("light");

  const getScreenshotsForTheme = (theme: "light" | "dark") => {
    const preferred = theme === "dark" ? screenshotsDark : screenshotsLight;
    if (preferred.length > 0) {
      return preferred;
    }

    return theme === "dark" ? screenshotsLight : screenshotsDark;
  };

  const visibleScreenshots = $derived(getScreenshotsForTheme(activeTheme));

  function prevSlide() {
    (swiperEl as any)?.swiper?.slidePrev();
  }

  function nextSlide() {
    (swiperEl as any)?.swiper?.slideNext();
  }

  function goToSlide(index: number) {
    (swiperEl as any)?.swiper?.slideToLoop?.(index);
  }

  onMount(() => {
    register();

    const root = document.documentElement;
    const syncTheme = () => {
      activeTheme = root.classList.contains("dark") ? "dark" : "light";
      activeIndex = 0;
      (swiperEl as any)?.swiper?.slideToLoop?.(0, 0);
    };

    syncTheme();

    const themeObserver = new MutationObserver(() => {
      syncTheme();
    });

    themeObserver.observe(root, {
      attributes: true,
      attributeFilter: ["class"],
    });

    if (!frameEl) {
      return () => {
        themeObserver.disconnect();
      };
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          inView = true;
          observer.disconnect();
        }
      },
      { threshold: 0.7 },
    );

    observer.observe(frameEl);

    const currentSwiperEl = swiperEl as any;
    const onSlideChange = () => {
      activeIndex = currentSwiperEl?.swiper?.realIndex ?? 0;
    };

    currentSwiperEl?.addEventListener?.("swiperslidechange", onSlideChange);

    return () => {
      themeObserver.disconnect();
      observer.disconnect();
      currentSwiperEl?.removeEventListener?.("swiperslidechange", onSlideChange);
    };
  });
</script>

{#if visibleScreenshots.length > 0}
  <div
    bind:this={frameEl}
    class="mx-auto mt-16 w-full max-w-5xl px-4 perspective-[2000px] opacity-0 animate-fade-in sm:px-6 lg:px-0"
    style="--animation-delay: 600ms"
  >
    <div
      class={`relative overflow-hidden rounded-lg bg-opacity-[0.01] bg-hero-gradient before:absolute before:inset-0 before:bg-hero-glow before:opacity-0 before:filter-[blur(120px)] before:pointer-events-none ${inView ? "animate-image-rotate before:animate-image-glow" : "transform-[rotateX(25deg)]"}`}
    >
      <div
        class="relative z-10 aspect-2260/1250 w-full overflow-hidden rounded-lg border bg-background"
        role="region"
        aria-label="Sylve screenshots"
      >
        <div
          class={`hero-privacy-screen ${inView ? "hero-privacy-screen-hidden" : ""}`}
          aria-hidden="true"
        ></div>

        <swiper-container
          bind:this={swiperEl}
          class="hero-swiper h-full w-full"
          slides-per-view="1"
          speed="650"
          loop={visibleScreenshots.length > 1}
          navigation={false}
          pagination={false}
          keyboard={true}
          a11y={true}
          zoom={true}
          zoom-max-ratio="4"
          zoom-min-ratio="1"
          zoom-toggle={true}
        >
          {#each visibleScreenshots as screenshot, slideIndex}
            <swiper-slide>
              <div class="swiper-zoom-container h-full w-full">
                <img
                  src={screenshot}
                  alt={`Sylve screenshot ${slideIndex + 1}`}
                  class="h-full w-full object-cover object-top md:object-cover"
                  draggable="false"
                  loading={slideIndex === 0 ? "eager" : "lazy"}
                />
              </div>
            </swiper-slide>
          {/each}
        </swiper-container>

        {#if visibleScreenshots.length > 1}
          <div class="pointer-events-none absolute inset-x-4 top-1/2 z-20 flex -translate-y-1/2 items-center justify-between">
            <button
              class="hero-nav-btn pointer-events-auto"
              onclick={prevSlide}
              aria-label="Previous screenshot"
            >
              <span class="icon-[lucide--chevron-left] size-4"></span>
            </button>
            <button
              class="hero-nav-btn pointer-events-auto"
              onclick={nextSlide}
              aria-label="Next screenshot"
            >
              <span class="icon-[lucide--chevron-right] size-4"></span>
            </button>
          </div>

          <div class="absolute inset-x-0 bottom-3 z-20 flex items-center justify-center gap-1.5 px-4">
            {#each visibleScreenshots as _, dotIndex}
              <button
                class={`hero-dot ${dotIndex === activeIndex ? "hero-dot-active" : ""}`}
                onclick={() => goToSlide(dotIndex)}
                aria-label={`Go to screenshot ${dotIndex + 1}`}
              ></button>
            {/each}
          </div>
        {/if}

        <div class="pointer-events-none absolute left-4 bottom-4 z-20 rounded-full border border-white/20 bg-black/45 px-3 py-1 text-xs text-white backdrop-blur">
          Pinch or double-click to zoom
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  .hero-privacy-screen {
    position: absolute;
    inset: 0;
    z-index: 15;
    pointer-events: none;
    border-radius: inherit;
    background: linear-gradient(180deg, rgb(0 0 0 / 62%), rgb(0 0 0 / 48%));
    opacity: 1;
    transition: opacity 640ms cubic-bezier(0.22, 1, 0.36, 1), visibility 640ms ease;
  }

  .hero-privacy-screen-hidden {
    opacity: 0;
    visibility: hidden;
  }

  .hero-swiper {
    transition: filter 500ms ease;
  }

  .hero-nav-btn {
    display: inline-flex;
    height: 2rem;
    width: 2rem;
    align-items: center;
    justify-content: center;
    border-radius: 9999px;
    border: 1px solid rgb(255 255 255 / 18%);
    background: linear-gradient(180deg, rgb(18 18 18 / 78%), rgb(6 6 6 / 58%));
    color: rgb(245 245 245 / 95%);
    backdrop-filter: blur(8px);
    box-shadow: 0 8px 24px rgb(0 0 0 / 28%), inset 0 1px 0 rgb(255 255 255 / 14%);
    transition: transform 180ms ease, background-color 180ms ease, border-color 180ms ease;
  }

  .hero-nav-btn:hover {
    transform: translateY(-1px);
    border-color: rgb(255 255 255 / 28%);
    background: linear-gradient(180deg, rgb(22 22 22 / 84%), rgb(9 9 9 / 64%));
  }

  .hero-dot {
    height: 0.45rem;
    width: 0.45rem;
    border-radius: 9999px;
    border: 1px solid rgb(255 255 255 / 24%);
    background: rgb(255 255 255 / 28%);
    backdrop-filter: blur(4px);
    transition: all 220ms ease;
  }

  .hero-dot-active {
    width: 1.5rem;
    background: linear-gradient(90deg, rgb(255 255 255 / 95%), rgb(219 234 254 / 92%));
    border-color: rgb(255 255 255 / 65%);
    box-shadow: 0 0 0 1px rgb(255 255 255 / 16%), 0 4px 14px rgb(59 130 246 / 22%);
  }
</style>
