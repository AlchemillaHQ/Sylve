<script lang="ts">
  const capabilities = [
    {
      index: "01",
      label: "Compute",
      title: "Run VMs and Jails.",
      description:
        "Provision Bhyve VMs, FreeBSD Jails, and Linux Jails. Snapshot workloads, create templates, and migrate guests between hosts.",
      points: ["Cloud-Init and templates", "PCI passthrough and CPU pinning", "Snapshots and migration"],
      kind: "compute",
    },
    {
      index: "02",
      label: "Storage",
      title: "Build on ZFS.",
      description:
        "Create pools, filesystems, and zvols; schedule snapshots; replicate datasets; and serve storage over Samba or iSCSI.",
      points: ["Periodic snapshots", "Replication and failover", "Samba and iSCSI"],
      kind: "storage",
    },
    {
      index: "03",
      label: "Network",
      title: "Shape the network.",
      description:
        "Build bridges and virtual networks, enforce firewall and NAT policy, and run DHCP, mDNS, routing, or WireGuard on the host.",
      points: ["Bridges and interfaces", "Firewall and NAT", "DHCP, mDNS and WireGuard"],
      kind: "network",
    },
    {
      index: "04",
      label: "Multi-node",
      title: "Coordinate every host.",
      description:
        "Join FreeBSD hosts into a cluster, migrate guests, schedule replicated copies, fail over workloads, and restore from remote backups.",
      points: ["Guest migration", "Replication policies", "Backup and restore"],
      kind: "cluster",
    },
  ];
</script>

<section class="capabilities-section site-shell" id="capabilities">
  <div class="capabilities-header">
    <div>
      <p class="section-kicker">Sylve platform</p>
      <h2>Build and operate on FreeBSD.</h2>
    </div>
    <p>
      Run VMs and Jails, manage ZFS storage and host networking, then coordinate
      workloads across a cluster.
    </p>
  </div>

  <div class="fact-strip" aria-label="Sylve platform facts">
    <div><strong>&lt;384 MB</strong><span>baseline memory</span></div>
    <div><strong>Bhyve + Jails</strong><span>workload runtimes</span></div>
    <div><strong>ZFS native</strong><span>storage and replication</span></div>
    <div><strong>BSD-2-Clause</strong><span>open source</span></div>
  </div>

  <div class="capability-grid">
    {#each capabilities as capability (capability.index)}
      <article class="capability-card">
        <div class="capability-card-copy">
          <div class="capability-meta">
            <span>{capability.index}</span>
            <p class="section-kicker">{capability.label}</p>
          </div>
          <h3>{capability.title}</h3>
          <p>{capability.description}</p>
          <ul>
            {#each capability.points as point (point)}
              <li>{point}</li>
            {/each}
          </ul>
        </div>
        <div class={`system-figure ${capability.kind}`} aria-hidden="true">
          <span class="figure-label">Fig. {capability.index} / {capability.label}</span>
          {#if capability.kind === "compute"}
            <span class="figure-reading">06 active</span>
            <svg viewBox="0 0 600 260" role="presentation">
              <g class="wire faint">
                <circle cx="300" cy="137" r="103"></circle>
                <circle cx="300" cy="137" r="72"></circle>
                <line x1="300" y1="18" x2="300" y2="246"></line>
                <line x1="80" y1="137" x2="520" y2="137"></line>
              </g>
              <g class="wire">
                <path d="M300 65L410 86L458 177L350 216L186 190L142 91Z"></path>
                <line x1="300" y1="137" x2="300" y2="65"></line>
                <line x1="300" y1="137" x2="410" y2="86"></line>
                <line x1="300" y1="137" x2="458" y2="177"></line>
                <line x1="300" y1="137" x2="350" y2="216"></line>
                <line x1="300" y1="137" x2="186" y2="190"></line>
                <line x1="300" y1="137" x2="142" y2="91"></line>
              </g>
              <g class="nodes">
                <circle cx="300" cy="65" r="7"></circle>
                <circle cx="410" cy="86" r="7"></circle>
                <circle cx="458" cy="177" r="7"></circle>
                <circle cx="350" cy="216" r="7"></circle>
                <circle cx="186" cy="190" r="7"></circle>
                <circle cx="142" cy="91" r="7"></circle>
              </g>
              <g class="core-mark">
                <rect x="273" y="110" width="54" height="54" rx="3"></rect>
                <path d="M286 124H314M286 137H314M286 150H306"></path>
              </g>
              <g class="diagram-labels">
                <text x="315" y="51">VM-01</text>
                <text x="424" y="77">JAIL-02</text>
                <text x="472" y="181">VM-03</text>
                <text x="365" y="231">JAIL-04</text>
                <text x="120" y="209">VM-05</text>
                <text x="76" y="82">VM-06</text>
              </g>
            </svg>
          {:else if capability.kind === "storage"}
            <span class="figure-reading">pool / atlas</span>
            <svg viewBox="0 0 600 260" role="presentation">
              <g class="wire faint">
                <line x1="48" y1="48" x2="552" y2="48"></line>
                <line x1="48" y1="212" x2="552" y2="212"></line>
                <circle cx="250" cy="132" r="96"></circle>
                <circle cx="250" cy="132" r="68"></circle>
                <circle cx="250" cy="132" r="40"></circle>
              </g>
              <g class="storage-arcs">
                <path d="M250 36A96 96 0 0 1 346 132"></path>
                <path d="M318 132A68 68 0 0 1 215 191"></path>
                <path d="M250 172A40 40 0 0 1 222 103"></path>
              </g>
              <g class="storage-scale">
                <line x1="398" y1="72" x2="536" y2="72"></line>
                <line x1="398" y1="104" x2="506" y2="104"></line>
                <line x1="398" y1="136" x2="524" y2="136"></line>
                <line x1="398" y1="168" x2="474" y2="168"></line>
                <circle cx="398" cy="72" r="3"></circle>
                <circle cx="398" cy="104" r="3"></circle>
                <circle cx="398" cy="136" r="3"></circle>
                <circle cx="398" cy="168" r="3"></circle>
              </g>
              <text class="figure-number" x="250" y="127" text-anchor="middle">4.2</text>
              <text class="figure-unit" x="250" y="149" text-anchor="middle">TB / 8.0 TB</text>
              <g class="diagram-labels">
                <text x="410" y="67">DATASETS</text>
                <text x="410" y="99">SNAPSHOTS</text>
                <text x="410" y="131">REPLICAS</text>
                <text x="410" y="163">FREE</text>
              </g>
            </svg>
          {:else if capability.kind === "network"}
            <span class="figure-reading">10.0.0.0 / 24</span>
            <svg viewBox="0 0 600 260" role="presentation">
              <g class="wire faint">
                <path d="M63 54H537M63 130H537M63 206H537"></path>
                <path d="M110 35V225M300 35V225M490 35V225"></path>
              </g>
              <g class="network-links">
                <path d="M110 84L300 130L490 67L490 192L300 130L110 190Z"></path>
                <path d="M110 84L300 52L490 67M110 190L300 213L490 192"></path>
              </g>
              <g class="network-points">
                <circle cx="110" cy="84" r="8"></circle>
                <circle cx="300" cy="52" r="5"></circle>
                <circle cx="490" cy="67" r="8"></circle>
                <circle cx="300" cy="130" r="14"></circle>
                <circle cx="110" cy="190" r="8"></circle>
                <circle cx="300" cy="213" r="5"></circle>
                <circle cx="490" cy="192" r="8"></circle>
              </g>
              <circle class="network-pulse" cx="300" cy="130" r="31"></circle>
              <g class="diagram-labels">
                <text x="78" y="73">IGB0</text>
                <text x="505" y="62">VNET2</text>
                <text x="315" y="124">BRIDGE0</text>
                <text x="55" y="211">DHCP</text>
                <text x="505" y="210">DNS</text>
              </g>
            </svg>
          {:else}
            <span class="figure-reading">quorum / 03</span>
            <svg viewBox="0 0 600 260" role="presentation">
              <g class="wire faint">
                <circle cx="300" cy="130" r="111"></circle>
                <circle cx="300" cy="130" r="74"></circle>
                <line x1="72" y1="130" x2="528" y2="130"></line>
              </g>
              <g class="cluster-paths">
                <path d="M300 43L460 181L140 181Z"></path>
                <path d="M300 43V130L460 181M300 130L140 181"></path>
              </g>
              <g class="cluster-points">
                <circle cx="300" cy="43" r="20"></circle>
                <circle cx="460" cy="181" r="20"></circle>
                <circle cx="140" cy="181" r="20"></circle>
                <circle cx="300" cy="130" r="7"></circle>
              </g>
              <g class="cluster-glyphs">
                <path d="M292 43H308M300 35V51"></path>
                <path d="M452 181H468M460 173V189"></path>
                <path d="M132 181H148M140 173V189"></path>
              </g>
              <g class="diagram-labels">
                <text x="318" y="39">VEGA / LEADER</text>
                <text x="480" y="185">LYRA</text>
                <text x="77" y="185">ORION</text>
                <text x="316" y="126">RAFT 24819</text>
              </g>
            </svg>
          {/if}
        </div>
      </article>
    {/each}
  </div>
</section>

<style>
  .system-figure {
    --diagram-accent: var(--foreground);
    position: relative;
    min-height: 18rem;
    margin-top: 0;
    margin-inline: -1.75rem;
    overflow: hidden;
    border-top: 1px solid color-mix(in oklab, var(--foreground) 10%, transparent);
    background-image:
      linear-gradient(color-mix(in oklab, var(--foreground) 5%, transparent) 1px, transparent 1px),
      linear-gradient(90deg, color-mix(in oklab, var(--foreground) 5%, transparent) 1px, transparent 1px);
    background-position: center;
    background-size: 2.4rem 2.4rem;
    color: var(--foreground);
  }

  .figure-label,
  .figure-reading {
    position: absolute;
    z-index: 1;
    top: 0.95rem;
    color: var(--muted-foreground);
    font: 0.55rem "IBM Plex Mono", monospace;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .figure-label { left: 1.35rem; }
  .figure-reading { right: 1.35rem; }

  svg {
    position: absolute;
    inset: 2rem 0 0;
    width: 100%;
    height: calc(100% - 2rem);
    overflow: visible;
  }

  .wire,
  .network-links,
  .cluster-paths,
  .storage-scale {
    fill: none;
    stroke: color-mix(in oklab, var(--foreground) 25%, transparent);
    stroke-width: 1;
    vector-effect: non-scaling-stroke;
  }

  .faint {
    stroke: color-mix(in oklab, var(--foreground) 10%, transparent);
    stroke-dasharray: 3 7;
  }

  .nodes circle,
  .network-points circle,
  .cluster-points circle {
    fill: var(--background);
    stroke: color-mix(in oklab, var(--foreground) 50%, transparent);
    stroke-width: 1;
    vector-effect: non-scaling-stroke;
  }

  .nodes circle:nth-child(2),
  .nodes circle:nth-child(5),
  .network-points circle:nth-child(4),
  .cluster-points circle:first-child {
    fill: var(--diagram-accent);
    stroke: var(--diagram-accent);
  }

  .core-mark rect {
    fill: color-mix(in oklab, var(--diagram-accent) 14%, var(--background));
    stroke: var(--diagram-accent);
    stroke-width: 1;
  }

  .core-mark path,
  .cluster-glyphs path {
    fill: none;
    stroke: var(--foreground);
    stroke-width: 1;
    vector-effect: non-scaling-stroke;
  }

  .diagram-labels,
  .figure-unit {
    fill: var(--muted-foreground);
    font: 9px "IBM Plex Mono", monospace;
    letter-spacing: 0.08em;
  }

  .storage-arcs {
    fill: none;
    stroke: var(--diagram-accent);
    stroke-width: 4;
    stroke-linecap: square;
    vector-effect: non-scaling-stroke;
  }

  .storage-scale circle { fill: var(--diagram-accent); stroke: none; }
  .figure-number { fill: var(--foreground); font: 500 38px Inter, sans-serif; letter-spacing: -0.06em; }
  .figure-unit { font-size: 8px; }

  .network-links,
  .cluster-paths {
    stroke: color-mix(in oklab, var(--diagram-accent) 72%, var(--foreground));
    stroke-dasharray: 4 6;
  }

  .network-pulse {
    fill: none;
    stroke: var(--diagram-accent);
    stroke-width: 1;
    opacity: 0.35;
    transform-origin: 300px 130px;
    animation: diagram-pulse 4s ease-out infinite;
  }

  .cluster-glyphs path { stroke: var(--foreground); }
  .cluster-glyphs path:first-child { stroke: var(--background); }

  @keyframes diagram-pulse {
    0%, 30% { opacity: 0.4; transform: scale(0.7); }
    80%, 100% { opacity: 0; transform: scale(1.35); }
  }

  @media (max-width: 560px) {
    .system-figure {
      min-height: 15.5rem;
      margin-inline: -1.1rem;
    }

    .figure-label { left: 1rem; }
    .figure-reading { right: 1rem; }
  }
</style>
