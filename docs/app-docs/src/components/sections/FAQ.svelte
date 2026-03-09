<script lang="ts">
  const faq = [
    {
      question: "What workloads can I run with Sylve?",
      answer:
        "Sylve supports both Bhyve virtual machines and Jail-based workloads from a single UI. It also includes Samba share management and ZFS snapshot automation with periodic schedules and retention policies, so teams can run workloads and protect data from one control plane.",
      value: "workloads",
    },
    {
      question: "Does Sylve support backups and replication?",
      answer:
        "Yes. Sylve uses ZFS replication primarily to replicate VM and Jail datasets across multiple Sylve instances, and it also doubles as a strong backup workflow. It supports incremental replication to SSH-accessible hosts with ZFS, so you can keep workloads synchronized between nodes while maintaining efficient off-host backups.",
      value: "replication-backup",
    },
    {
      question: "Is Sylve actually lightweight enough for small systems?",
      answer:
        "Absolutely. Sylve is built to stay lean, with low overhead and fast setup. Basic deployments can run with less than 384 MB RAM and 1 vCPU, making it suitable for homelabs, edge nodes, and resource-constrained environments.",
      value: "lightweight",
    },
    {
      question: "What advanced features are included today?",
      answer:
        "Sylve includes native Cloud-Init provisioning, built-in networking tools (DNS, DHCP, and bridges), ZFS file-sharing workflows for clients like Windows, PCI passthrough, and CPU pinning with multi-socket awareness. It is designed to be simple up front, while still giving power users the controls they need.",
      value: "advanced-features",
    },
    {
      question: "Who should use Sylve?",
      answer:
        "Sylve is a strong fit for anyone who wants a very lightweight virtualization stack, including IT teams that want a modern UI for VM and Jail management, operators running lean infrastructure, and enthusiasts who want high performance without large enterprise bloat.",
      value: "who-should-use",
    },
    {
      question: "How is Sylve different from Proxmox?",
      answer:
        "Sylve delivers a Proxmox-like experience with a much lighter footprint and a strong focus on simplicity. ARM64 is supported out of the box as a first-class platform today (with ARM64 VM support coming soon), and the core stack is built in-house, including the RAFT layer, DB layer, and replication layer, without requiring external core dependencies like corosync. Live migration is not available yet, but Sylve already supports extremely frequent and fast ZFS replication and backup workflows.",
      value: "proxmox-comparison",
    },
  ];

  let openItem = $state<string | null>(faq[0]?.value ?? null);

  function toggle(item: string) {
    openItem = openItem === item ? null : item;
  }
</script>

<section
  class="pt-10 pb-24 relative -translate-y-4 animate-fade-in opacity-0"
  style="--animation-delay: 120ms"
>
  <div class="container mx-auto px-4 max-w-4xl">
    <div class="text-center mb-16">
      <h2 class="text-3xl md:text-5xl font-bold mb-4 text-gradient">
        Frequently Asked Questions
      </h2>
      <p class="text-muted-foreground text-lg">
        Everything you need to know about Sylve.
      </p>
    </div>

    <div class="w-full border rounded-xl divide-y">
      {#each faq as item}
        <div class="px-4">
          <button
            class="w-full py-4 text-left flex items-center justify-between gap-3"
            onclick={() => toggle(item.value)}
            aria-expanded={openItem === item.value}
          >
            <span class="text-xl font-bold">{item.question}</span>
            <span
              class={`icon-[lucide--chevron-down] size-4 text-muted-foreground transition-transform ${openItem === item.value ? "rotate-180" : ""}`}
            ></span>
          </button>
          {#if openItem === item.value}
            <p class="text-base text-muted-foreground pb-4">{item.answer}</p>
          {/if}
        </div>
      {/each}
    </div>

    <div class="mt-12 text-center">
      <p class="text-muted-foreground">
        Still have questions?
        <a
          href="https://discord.gg/bJB826JvXK"
          class="text-primary hover:underline"
        >
          Join our Discord
        </a>
        or
        <a href="/docs/" class="text-primary hover:underline">
          read the documentation
        </a>.
      </p>
    </div>
  </div>
</section>
