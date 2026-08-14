<script lang="ts">
  const faq = [
    {
      question: "What workloads does Sylve support?",
      answer:
        "Sylve runs Bhyve virtual machines, FreeBSD Jails, and Linux Jails. It provides guest lifecycle controls, VNC and serial consoles, snapshots, Cloud-Init, VM templates, PCI passthrough, CPU pinning, and migration between Sylve hosts.",
      value: "workloads",
    },
    {
      question: "What does Sylve require from the host?",
      answer:
        "Sylve is designed for FreeBSD 15.0 or later. A basic deployment uses less than 384 MB of memory and one vCPU, leaving most host resources available for workloads. Actual capacity depends on your guests and storage demands.",
      value: "host-requirements",
    },
    {
      question: "How does Sylve use ZFS?",
      answer:
        "ZFS is the storage foundation. Sylve manages pools, filesystems, zvols, periodic snapshots, and incremental replication. It can also publish storage through Samba and iSCSI.",
      value: "zfs-storage",
    },
    {
      question: "Which networking features are built in?",
      answer:
        "Sylve manages physical and virtual interfaces, bridges, routes, reusable network objects, DHCP, mDNS, WireGuard, traffic filtering, and SNAT, DNAT, or BINAT rules.",
      value: "networking",
    },
    {
      question: "Can I cluster hosts, migrate guests, and fail over?",
      answer:
        "Yes. Sylve can join hosts into a built-in cluster, migrate VMs and Jails between nodes, schedule replicated copies, and fail over a workload to another host. Guest migration is currently not live.",
      value: "cluster-migration",
    },
    {
      question: "How do backups and restores work?",
      answer:
        "Backup jobs send VM and Jail data to configured remote targets using ZFS snapshots and incremental streams. Sylve tracks job progress and history, exposes retained snapshots, and can restore a guest from a selected backup.",
      value: "backup-restore",
    },
    {
      question: "How is Sylve different from Proxmox?",
      answer:
        "Proxmox is built around Linux, KVM, and LXC. Sylve is built for FreeBSD around Bhyve, Jails, and ZFS, with its own clustering, replication, backup, networking, and storage workflows. Sylve does not currently provide live migration.",
      value: "proxmox-comparison",
    },
  ];

  let openItem = $state<string | null>(faq[0]?.value ?? null);

  function toggle(item: string) {
    openItem = openItem === item ? null : item;
  }
</script>

<section class="faq-section site-shell" aria-labelledby="faq-title">
  <div class="faq-intro">
    <p class="section-kicker">Common questions</p>
    <h2 id="faq-title">Know what fits before you install.</h2>
    <p>
      Still deciding if Sylve fits your environment? Read the
      <a href="/docs/">documentation</a> or talk with us on
      <a href="https://discord.gg/bJB826JvXK">Discord</a>.
    </p>
  </div>

  <div class="faq-list">
    {#each faq as item, index (item.value)}
      <div class="faq-item" class:open={openItem === item.value}>
        <button onclick={() => toggle(item.value)} aria-expanded={openItem === item.value}>
          <small>{String(index + 1).padStart(2, "0")}</small>
          <span>{item.question}</span>
          <i class:open={openItem === item.value} aria-hidden="true"></i>
        </button>
        {#if openItem === item.value}
          <p>{item.answer}</p>
        {/if}
      </div>
    {/each}
  </div>
</section>

<style>
  .faq-list { overflow: hidden; border: 1px solid color-mix(in oklab, var(--foreground) 12%, transparent); border-radius: 12px; background: color-mix(in oklab, var(--foreground) 2%, transparent); }
  .faq-item { border-bottom: 1px solid color-mix(in oklab, var(--foreground) 13%, transparent); }
  .faq-item:last-child { border-bottom: 0; }
  .faq-item.open { background: color-mix(in oklab, var(--foreground) 2.5%, transparent); }
  .faq-item button { display: grid; grid-template-columns: 2.2rem 1fr auto; align-items: center; gap: .8rem; width: 100%; padding: 1.25rem 1.3rem; border: 0; background: transparent; color: var(--foreground); text-align: left; cursor: pointer; }
  .faq-item small { color: var(--muted-foreground); font: .6rem "IBM Plex Mono", monospace; }
  .faq-item span { font-size: 1rem; font-weight: 520; letter-spacing: -.015em; }
  .faq-item i { position: relative; width: 1.75rem; height: 1.75rem; border: 1px solid color-mix(in oklab, var(--foreground) 13%, transparent); border-radius: 50%; }
  .faq-item i::before, .faq-item i::after { content: ""; position: absolute; left: 25%; top: 50%; width: 50%; height: 1px; background: currentColor; transition: transform 180ms ease; }
  .faq-item i::after { transform: rotate(90deg); }
  .faq-item i.open::after { transform: rotate(0); }
  .faq-item p { max-width: 44rem; margin: -.2rem 4.4rem 1.35rem 4.3rem; color: var(--muted-foreground); font-size: .88rem; line-height: 1.7; }
  @media (max-width: 560px) {
    .faq-item button { grid-template-columns: 1.6rem 1fr auto; padding-inline: 1rem; }
    .faq-item p { margin: -.1rem 1rem 1.25rem; }
  }
</style>
