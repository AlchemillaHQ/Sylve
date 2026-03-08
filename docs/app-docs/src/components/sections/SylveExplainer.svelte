<script lang="ts">
  import { onMount } from "svelte";
  import { register } from "swiper/element/bundle";

  const features = [
    {
      title: "Virtual Machines & Jails",
      description:
        "Manage the full lifecycle of Bhyve VMs and FreeBSD Jails from a single streamlined control panel.",
      darkImage: "/screenshots/VM-CreateB.png",
      lightImage: "/screenshots/VM-Create.png",
    },
    {
      title: "ZFS Storage",
      description:
        "Manage pools, datasets, snapshots, and disk health with powerful built-in ZFS tooling, including streamlined workflows for replication.",
      darkImage: "/screenshots/ZFS-DatasetsB.png",
      lightImage: "/screenshots/ZFS-Datasets.png",
    },
    {
      title: "Networking",
      description:
        "Configure bridges, DHCP, and reusable network objects from a single intuitive interface, from quick setup to advanced management.",
      darkImage: "/screenshots/NetworkingB.png",
      lightImage: "/screenshots/Networking.png",
    },
    {
      title: "Clustering",
      description:
        "Using the same RAFT consensus model as Kubernetes, Sylve forms resilient clusters with distributed state and ZFS replication.",
      darkImage: "/screenshots/ClusteringB.png",
      lightImage: "/screenshots/Clustering.png",
    },
  ];

  const platformPartners = [
    {
      name: "Svelte",
      href: "https://svelte.dev",
      iconClass: "icon-[ri--svelte-fill]",
      iconColorClass: "text-[#ff3e00] dark:text-[#ff6a3d]",
    },
    {
      name: "Go",
      href: "https://go.dev",
      iconClass: "icon-[lineicons--go]",
      iconColorClass: "text-[#00add8] dark:text-[#64d8f7]",
    },
    {
      name: "libvirt",
      href: "https://libvirt.org",
      lightLogo: "/partners/libvirt.png",
      darkLogo: "/partners/libvirt.png",
      imgClass: "max-h-8",
    },
    {
      name: "OpenZFS",
      href: "https://openzfs.org",
      lightLogo: "/partners/openzfs-blue.png",
      darkLogo: "/partners/openzfs-white.png",
      imgClass: "max-h-7",
    },
    {
      name: "Zelta",
      href: "https://zelta.space",
      lightLogo: "/partners/zelta-yellow.svg",
      darkLogo: "/partners/zelta-white.svg",
      imgClass: "max-h-8",
    },
  ];

  let selectedFeature = $state(0);
  let featureSwiperEl = $state<HTMLElement | null>(null);
  let imageSwiperEl = $state<HTMLElement | null>(null);
  let isSyncing = false;

  function goToFeature(index: number) {
    selectedFeature = index;
    (featureSwiperEl as any)?.swiper?.slideTo?.(index);
    (imageSwiperEl as any)?.swiper?.slideTo?.(index);
  }

  function prevSlide() {
    (imageSwiperEl as any)?.swiper?.slidePrev?.();
  }

  function nextSlide() {
    (imageSwiperEl as any)?.swiper?.slideNext?.();
  }

  onMount(() => {
    register();

    const currentFeatureSwiperEl = featureSwiperEl as any;
    const currentImageSwiperEl = imageSwiperEl as any;

    const onFeatureSlideChange = () => {
      if (isSyncing) {
        return;
      }

      const idx = currentFeatureSwiperEl?.swiper?.realIndex ?? 0;
      selectedFeature = idx;
      const imageIndex = currentImageSwiperEl?.swiper?.activeIndex ?? 0;
      if (imageIndex !== idx) {
        isSyncing = true;
        currentImageSwiperEl?.swiper?.slideTo?.(idx);
        queueMicrotask(() => {
          isSyncing = false;
        });
      }
    };

    const onImageSlideChange = () => {
      if (isSyncing) {
        return;
      }

      const idx = currentImageSwiperEl?.swiper?.realIndex ?? 0;
      selectedFeature = idx;
      const featureIndex = currentFeatureSwiperEl?.swiper?.activeIndex ?? 0;
      if (featureIndex !== idx) {
        isSyncing = true;
        currentFeatureSwiperEl?.swiper?.slideTo?.(idx);
        queueMicrotask(() => {
          isSyncing = false;
        });
      }
    };

    currentFeatureSwiperEl?.addEventListener?.("swiperslidechange", onFeatureSlideChange);
    currentImageSwiperEl?.addEventListener?.("swiperslidechange", onImageSlideChange);

    return () => {
      currentFeatureSwiperEl?.removeEventListener?.("swiperslidechange", onFeatureSlideChange);
      currentImageSwiperEl?.removeEventListener?.("swiperslidechange", onImageSlideChange);
    };
  });
</script>

<section class="max-w-6xl mx-auto flex w-full flex-col items-center mt-12 lg:mt-24 -translate-y-4 animate-fade-in opacity-0" style="--animation-delay: 120ms">
  <h2 class="text-gradient mb-2 text-center text-3xl font-semibold tracking-tight md:text-5xl">A Modern Control Plane for FreeBSD</h2>
  <div class="mb-4 mt-2 flex w-full flex-wrap items-center justify-center gap-x-2 gap-y-2 text-xs text-muted-foreground">
    <span class="mr-1 uppercase tracking-[0.18em] text-muted-foreground/80">Powered by</span>
    {#each platformPartners as partner}
      <a
        href={partner.href}
        target="_blank"
        rel="noopener noreferrer"
        aria-label={`Visit ${partner.name}`}
        class="inline-flex items-center rounded-md border border-border/60 bg-background/55 px-2.5 py-1.5 transition-colors hover:border-primary/30"
      >
        {#if partner.iconClass}
          <span class={`${partner.iconClass} ${partner.iconColorClass ?? "text-foreground"} size-11 shrink-0`}></span>
        {:else}
          <img
            src={partner.lightLogo}
            alt={`${partner.name} logo`}
            class={`w-auto object-contain dark:hidden ${partner.imgClass}`}
            loading="lazy"
            decoding="async"
          />
          <img
            src={partner.darkLogo}
            alt={`${partner.name} logo`}
            class={`hidden w-auto object-contain dark:block ${partner.imgClass}`}
            loading="lazy"
            decoding="async"
          />
        {/if}
      </a>
    {/each}
  </div>

  <p class="block max-w-2xl text-center text-base leading-relaxed text-muted-foreground md:text-lg">
    Sylve brings virtualization, containers, storage, and networking together in one intuitive interface
    giving you complete control of your FreeBSD systems.
  </p>

  <section class="w-full py-12 md:pb-24 lg:pb-28">
    <div class="container mx-auto w-full px-0">
      <div class="mx-auto w-full max-w-5xl">
        <swiper-container
          bind:this={featureSwiperEl}
          class="features-swiper mx-auto w-full max-w-lg"
          slides-per-view="1"
          space-between="10"
          speed="420"
          loop={false}
          navigation={false}
          pagination={false}
          keyboard={true}
          a11y={true}
        >
          {#each features as feature, index}
            <swiper-slide>
              <button
                class={`h-full w-full rounded-xl border px-3.5 py-3.5 text-center transition-colors ${index === selectedFeature ? "border-primary/35 bg-muted/70" : "border-border/65 bg-background/70 hover:border-primary/25"}`}
                onclick={() => {
                  goToFeature(index);
                }}
                aria-label={`Show ${feature.title}`}
              >
                <h3 class="text-lg font-semibold">{feature.title}</h3>
                <p class="mt-1 text-sm text-muted-foreground">{feature.description}</p>
              </button>
            </swiper-slide>
          {/each}
        </swiper-container>

        <div class="mt-4 flex items-center justify-center gap-2">
          <button class="hero-nav-btn" onclick={prevSlide} aria-label="Previous feature image">
            <span class="icon-[lucide--chevron-left] size-4"></span>
          </button>
          <div class="flex items-center gap-1.5">
            {#each features as _, dotIndex}
              <button
                class={`hero-dot ${dotIndex === selectedFeature ? "hero-dot-active" : ""}`}
                onclick={() => goToFeature(dotIndex)}
                aria-label={`Go to feature ${dotIndex + 1}`}
              ></button>
            {/each}
          </div>
          <button class="hero-nav-btn" onclick={nextSlide} aria-label="Next feature image">
            <span class="icon-[lucide--chevron-right] size-4"></span>
          </button>
        </div>

        <div class="relative mt-5 overflow-hidden rounded-2xl">
          <div class="hidden lg:block absolute inset-0 bg-linear-to-r from-blue-500/45 to-orange-500/40 rounded-2xl blur-3xl opacity-20"></div>
          <div class="relative z-20 mx-auto w-full overflow-hidden rounded-2xl border border-border/70 bg-transparent shadow-xl lg:max-w-4xl">
            <swiper-container
              bind:this={imageSwiperEl}
              class="explainer-swiper h-full w-full"
              slides-per-view="1"
              speed="500"
              loop={false}
              navigation={false}
              pagination={false}
              keyboard={true}
              a11y={true}
            >
              {#each features as feature}
                <swiper-slide>
                  <img
                    src={feature.lightImage}
                    width="700"
                    height="420"
                    alt={feature.title}
                    class="block h-auto w-full dark:hidden"
                  />
                  <img
                    src={feature.darkImage}
                    width="700"
                    height="420"
                    alt={feature.title}
                    class="hidden h-auto w-full dark:block"
                  />
                </swiper-slide>
              {/each}
            </swiper-container>
          </div>
        </div>
      </div>
    </div>
  </section>
</section>

<style>
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
