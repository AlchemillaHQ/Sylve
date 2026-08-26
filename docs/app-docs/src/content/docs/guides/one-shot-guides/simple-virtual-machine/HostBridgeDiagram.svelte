<script>
  import {
    Background,
    BackgroundVariant,
    MarkerType,
    SvelteFlow,
  } from "@xyflow/svelte";
  import "@xyflow/svelte/dist/style.css";
  import NetworkNode from "./NetworkNode.svelte";
  import TopologyControls from "./TopologyControls.svelte";

  const MAX_HOST_PORTS = 3;
  const MAX_VMS = 4;
  const nodeTypes = { network: NetworkNode };

  let { guestType = "vm" } = $props();
  let hostPortCount = $state(1);
  let vmCount = $state(1);
  let isJail = $derived(guestType === "jail");
  let guestLabel = $derived(isJail ? "Jail" : "VM");
  let flowHeight = $derived(350 + (Math.max(hostPortCount, vmCount) - 1) * 155);

  const centeredY = (index, count, spacing, center) =>
    center - ((count - 1) * spacing) / 2 + index * spacing;

  let nodes = $derived.by(() => {
    const hostPorts = Array.from({ length: hostPortCount }, (_, index) => "em" + index);
    const vmPorts = Array.from(
      { length: vmCount },
      (_, index) => isJail ? "epair" + index + "a" : "tap" + index,
    );

    return [
      {
        id: "router",
        type: "network",
        position: { x: 0, y: 235 },
        data: {
          kind: "router",
          eyebrow: "UPSTREAM GATEWAY",
          label: "Your router",
          description: "The physical network your host connects to.",
        },
        draggable: false,
        selectable: false,
      },
      ...hostPorts.map((port, index) => ({
        id: "host-" + index,
        type: "network",
        position: { x: 245, y: centeredY(index, hostPortCount, 185, 235) },
        data: {
          kind: "nic",
          eyebrow: "HOST ETHERNET PORT",
          label: port,
          description: "A physical cable enters the FreeBSD host here.",
        },
        draggable: false,
        selectable: false,
      })),
      {
        id: "bridge",
        type: "network",
        position: { x: 510, y: 210 },
        data: {
          kind: "switch",
          hostPorts,
          vmPorts,
        },
        draggable: false,
        selectable: false,
      },
      ...vmPorts.map((port, index) => ({
        id: "vm-" + index,
        type: "network",
        position: { x: 900, y: centeredY(index, vmCount, 185, 235) },
        data: {
          kind: "vm",
          eyebrow: isJail ? "JAIL VNET STACK" : "VIRTUAL ETHERNET PORT",
          label: guestLabel + " " + (index + 1),
          description: isJail
            ? "epair" + index + "b lives inside this jail."
            : port + " acts like a virtual network cable.",
        },
        draggable: false,
        selectable: false,
      })),
    ];
  });

  let edges = $derived.by(() => [
    ...Array.from({ length: hostPortCount }, (_, index) => ({
      id: "router-host-" + index,
      source: "router",
      target: "host-" + index,
      label: index === 0 ? "Ethernet cable" : "Additional cable",
      animated: false,
      class: "packet-edge physical-edge",
    })),
    ...Array.from({ length: hostPortCount }, (_, index) => ({
      id: "host-bridge-" + index,
      source: "host-" + index,
      target: "bridge",
      targetHandle: "host-" + index,
      label: "Bridge port",
      animated: false,
      class: "packet-edge membership-edge",
    })),
    ...Array.from({ length: vmCount }, (_, index) => ({
      id: "bridge-vm-" + index,
      source: "bridge",
      sourceHandle: "vm-" + index,
      target: "vm-" + index,
      label: isJail ? "epair" + index + " pair" : "Virtual cable",
      animated: true,
      markerEnd: MarkerType.ArrowClosed,
      class: "packet-edge virtual-edge",
    })),
  ]);

  const addHostPort = () => {
    if (hostPortCount < MAX_HOST_PORTS) hostPortCount += 1;
  };

  const addVm = () => {
    if (vmCount < MAX_VMS) vmCount += 1;
  };

  const reset = () => {
    hostPortCount = 1;
    vmCount = 1;
  };
</script>

<figure class="network-map not-content" aria-labelledby="network-map-title">
  <figcaption>
    <div>
      <span id="network-map-title">BUILD THE HOST NETWORK</span>
      <small>Add ports and {guestLabel}s to see the software switch grow.</small>
    </div>
    <span class="live"><i></i> LIVE TOPOLOGY</span>
  </figcaption>

  <div
    class="flow"
    style:height={String(flowHeight) + "px"}
    aria-label={`Interactive host bridge topology for ${guestLabel}s`}
  >
    <SvelteFlow
      {nodes}
      {edges}
      {nodeTypes}
      fitView
      fitViewOptions={{ padding: 0.12, maxZoom: 0.9 }}
      minZoom={0.45}
      maxZoom={1.15}
      nodesDraggable={false}
      nodesConnectable={false}
      elementsSelectable={false}
      zoomOnScroll={false}
      zoomOnDoubleClick={false}
      panOnDrag={true}
      preventScrolling={false}
      proOptions={{ hideAttribution: true }}
      colorMode="system"
    >
      <Background variant={BackgroundVariant.Lines} gap={56} size={1} />
      <TopologyControls
        hostPortCount={hostPortCount}
        vmCount={vmCount}
        maxHostPorts={MAX_HOST_PORTS}
        maxVms={MAX_VMS}
        onAddHostPort={addHostPort}
        onAddVm={addVm}
        onReset={reset}
        {guestLabel}
      />
    </SvelteFlow>
  </div>

  <div class="key">
    <span><i class="solid"></i> Physical cable</span>
    <span><i class="membership"></i> Interface attached to bridge</span>
    <span><i class="dashed"></i> {isJail ? "epair pair" : "Virtual interface"}</span>
    <span>Drag the background to inspect the topology.</span>
  </div>

  <p class="plain-language">
    <strong>What this demonstrates:</strong>
    a Standard Switch is a software Ethernet bridge inside the FreeBSD host. It forwards Ethernet frames like a physical switch, while the bridge interface itself can hold the host's IP address.
    {#if isJail}
      Physical interfaces such as <code>em0</code> and host-side epair ends such as <code>epair0a</code> occupy its ports. The matching <code>epair0b</code> end is moved into the jail's VNET network stack.
    {:else}
      Physical interfaces such as <code>em0</code> and virtual interfaces such as <code>tap0</code> occupy its ports.
    {/if}
  </p>
</figure>

<style>
  .network-map {
    --flow-line: color-mix(in oklab, var(--sl-color-white) 25%, transparent);
    --flow-strong: color-mix(in oklab, var(--sl-color-white) 62%, transparent);
    --flow-faint: color-mix(in oklab, var(--sl-color-white) 8%, transparent);
    --flow-grid-opacity: 0.42;
    --flow-muted: var(--sl-color-gray-3);
    margin: 1.75rem 0;
    overflow: hidden;
    border: 1px solid var(--flow-line);
    border-radius: 0.25rem;
    background: var(--sl-color-bg);
    color: var(--sl-color-white);
  }

  :global(:root[data-theme="light"]) .network-map {
    --flow-faint: color-mix(in oklab, var(--sl-color-white) 7%, transparent);
    --flow-grid-opacity: 0.18;
  }

  figcaption {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.85rem 1rem;
    border-bottom: 1px solid var(--flow-faint);
    color: var(--flow-muted);
    font: 500 0.55rem var(--sl-font-mono);
    letter-spacing: 0.09em;
  }
  figcaption > div span,
  figcaption > div small { display: block; }
  figcaption > div small {
    margin-top: 0.25rem;
    color: var(--flow-muted);
    font-size: 0.55rem;
    letter-spacing: 0;
  }
  .live { display: flex; align-items: center; gap: 0.45rem; white-space: nowrap; }
  .live i {
    width: 0.38rem;
    height: 0.38rem;
    border-radius: 50%;
    background: var(--sl-color-white);
    box-shadow: 0 0 0.5rem var(--sl-color-white);
    animation: status-pulse 2s ease-in-out infinite;
  }

  .flow { transition: height 350ms ease; }

  :global(.svelte-flow) {
    --xy-background-color: transparent;
    --xy-edge-stroke: var(--flow-line);
    --xy-edge-stroke-width: 1.5;
    --xy-edge-label-color: var(--flow-muted);
    --xy-edge-label-background-color: var(--sl-color-bg);
    --xy-edge-label-background-opacity: 0.96;
    --xy-edge-label-padding: 4px 7px;
    --xy-edge-label-border-radius: 2px;
    background: transparent;
  }
  :global(.svelte-flow__background-pattern) {
    stroke: var(--flow-faint) !important;
    opacity: var(--flow-grid-opacity);
  }
  :global(.svelte-flow__node) { cursor: default; }
  :global(.svelte-flow__pane.draggable) { cursor: grab; }
  :global(.svelte-flow__pane.dragging) { cursor: grabbing; }
  :global(.svelte-flow__edge.packet-edge .svelte-flow__edge-path) {
    stroke: var(--flow-strong);
    stroke-dasharray: none;
  }
  :global(.svelte-flow__edge.membership-edge .svelte-flow__edge-path) {
    stroke-dasharray: 9 5 2 5;
    opacity: 0.75;
  }
  :global(.svelte-flow__edge.virtual-edge .svelte-flow__edge-path) {
    stroke-dasharray: 3 8;
    opacity: 0.8;
    animation-duration: 1.15s;
  }
  :global(.svelte-flow__edge-text) {
    font: 0.48rem var(--sl-font-mono);
    letter-spacing: 0.04em;
  }
  :global(.svelte-flow__arrowhead polyline) {
    stroke: var(--sl-color-white);
    fill: var(--sl-color-white);
  }

  .key {
    display: flex;
    flex-wrap: wrap;
    gap: 0.8rem 1.25rem;
    padding: 0.7rem 1rem;
    border-top: 1px solid var(--flow-faint);
    color: var(--flow-muted);
    font: 0.52rem var(--sl-font-mono);
  }
  .key span { display: flex; align-items: center; gap: 0.4rem; }
  .key span:last-child { margin-left: auto; }
  .key i { width: 1.4rem; border-top: 1px solid var(--flow-strong); }
  .key i.membership {
    height: 1px;
    border: 0;
    background: repeating-linear-gradient(
      to right,
      var(--flow-strong) 0 8px,
      transparent 8px 12px,
      var(--flow-strong) 12px 14px,
      transparent 14px 18px
    );
    opacity: 0.75;
  }
  .key i.dashed { border-top-style: dotted; }

  .plain-language {
    margin: 0;
    padding: 0.9rem 1rem;
    border-top: 1px solid var(--flow-faint);
    color: var(--flow-muted);
    font-size: 0.72rem;
    line-height: 1.55;
  }
  .plain-language strong { color: var(--sl-color-white); font-weight: 600; }
  .plain-language code { color: var(--sl-color-white); font-size: 0.68rem; }

  @keyframes status-pulse { 50% { opacity: 0.35; } }

  @media (max-width: 42rem) {
    .key span:last-child { width: 100%; margin-left: 0; }
  }
  @media (prefers-reduced-motion: reduce) {
    .live i,
    :global(.svelte-flow__edge.packet-edge .svelte-flow__edge-path) {
      animation: none;
    }
  }
</style>
