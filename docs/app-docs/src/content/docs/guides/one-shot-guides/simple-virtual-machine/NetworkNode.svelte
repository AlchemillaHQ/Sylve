<script>
  import { Handle, Position } from "@xyflow/svelte";

  let { data } = $props();

  let switchPorts = $derived([
    ...(data.hostPorts ?? []).map((name) => ({ name, kind: "host" })),
    ...(data.vmPorts ?? []).map((name) => ({ name, kind: "vm" })),
  ]);

  const handlePosition = (index, count) => String(((index + 1) / (count + 1)) * 100) + "%";
</script>

{#if data.kind === "switch"}
  <div class="switch-node" style:--port-count={switchPorts.length}>
    {#each data.hostPorts as port, index (port)}
      <Handle
        id={"host-" + index}
        type="target"
        position={Position.Left}
        isConnectable={false}
        style={"top: " + handlePosition(index, data.hostPorts.length)}
      />
    {/each}

    <header>
      <small>SYLVE STANDARD SWITCH</small>
      <div class="switch-title">
        <strong>LAN</strong>
        <span><i></i> ETHERNET BRIDGE</span>
      </div>
    </header>

    <div class="switch-chassis">
      <div class="switch-status">
        <span>STATUS</span>
        <i></i><i></i>
      </div>

      <div class="port-bank">
        {#each switchPorts as port, index (port.name)}
          <div class="port" data-kind={port.kind}>
            <span>{String(index + 1).padStart(2, "0")}</span>
            <div class="jack">
              <i></i><i></i><i></i><i></i>
            </div>
            <small>{port.name}</small>
          </div>
        {/each}
      </div>
    </div>

    <footer>
      <span>{switchPorts.length} ACTIVE PORT{switchPorts.length === 1 ? "" : "S"}</span>
      <span>HOST IP LIVES HERE</span>
    </footer>

    {#each data.vmPorts as port, index (port)}
      <Handle
        id={"vm-" + index}
        type="source"
        position={Position.Right}
        isConnectable={false}
        style={"top: " + handlePosition(index, data.vmPorts.length)}
      />
    {/each}
  </div>
{:else}
  <div class="device-node" data-kind={data.kind}>
    {#if data.kind !== "router"}
      <Handle type="target" position={Position.Left} isConnectable={false} />
    {/if}

    <small>{data.eyebrow}</small>

    <div class="device-icon" aria-hidden="true">
      {#if data.kind === "router"}
        <svg viewBox="0 0 92 64">
          <rect x="8" y="20" width="76" height="36" rx="3"></rect>
          <path d="M28 38H64M37 29L27 38L37 47M55 29L65 38L55 47"></path>
          <path d="M25 20L18 6M67 20L74 6"></path>
          <circle cx="18" cy="6" r="2"></circle>
          <circle cx="74" cy="6" r="2"></circle>
        </svg>
      {:else if data.kind === "nic"}
        <svg viewBox="0 0 76 64">
          <rect x="13" y="5" width="50" height="43" rx="3"></rect>
          <path d="M21 48V59M32 48V59M43 48V59M54 48V59"></path>
          <path d="M23 11V21M33 11V21M43 11V21M53 11V21"></path>
          <circle class="lit" cx="22" cy="36" r="3"></circle>
          <circle cx="33" cy="36" r="3"></circle>
        </svg>
      {:else}
        <svg viewBox="0 0 92 64">
          <rect x="8" y="5" width="76" height="46" rx="3"></rect>
          <path d="M21 19L31 28L21 37M39 37H65"></path>
          <path d="M34 59H58M46 51V59"></path>
        </svg>
      {/if}
    </div>

    <strong>{data.label}</strong>
    <p>{data.description}</p>

    {#if data.kind !== "vm"}
      <Handle type="source" position={Position.Right} isConnectable={false} />
    {/if}
  </div>
{/if}

<style>
  .device-node,
  .switch-node {
    border: 1px solid var(--flow-line);
    border-radius: 0.25rem;
    background: color-mix(in oklab, var(--sl-color-bg) 96%, var(--sl-color-white));
    box-shadow: 0 1rem 3rem color-mix(in oklab, var(--sl-color-bg) 82%, transparent);
    color: var(--sl-color-white);
    font-family: var(--sl-font);
  }
  .device-node { width: 9.5rem; padding: 0.6rem 0.75rem 0.7rem; }
  .device-node > small,
  .switch-node header small {
    color: var(--flow-muted);
    font: 500 0.48rem var(--sl-font-mono);
    letter-spacing: 0.08em;
  }
  .device-icon {
    display: grid;
    place-items: center;
    height: 3.8rem;
    margin: 0.4rem 0 0.55rem;
    border-block: 1px solid var(--flow-faint);
  }
  .device-icon svg {
    width: auto;
    height: 3.35rem;
    overflow: visible;
    fill: none;
    stroke: var(--flow-strong);
    stroke-width: 1;
    vector-effect: non-scaling-stroke;
  }
  .device-icon .lit { fill: var(--sl-color-white); stroke: none; }
  .device-node strong { display: block; font-size: 0.76rem; font-weight: 650; }
  .device-node p {
    min-height: 2.6em;
    margin: 0.25rem 0 0;
    color: var(--flow-muted);
    font-size: 0.61rem;
    line-height: 1.4;
  }
  .switch-node {
    width: max(14rem, calc(var(--port-count) * 3rem + 2rem));
    overflow: hidden;
    transition: width 350ms ease;
  }
  .switch-node footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }
  .switch-node header { padding: 0.6rem 0.75rem 0.5rem; }
  .switch-node header small { white-space: nowrap; }
  .switch-title {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-top: 0.35rem;
  }
  .switch-node header strong { display: block; font-size: 0.9rem; }
  .switch-title span,
  .switch-node footer {
    color: var(--flow-muted);
    font: 0.48rem var(--sl-font-mono);
    letter-spacing: 0.07em;
  }
  .switch-title span {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    white-space: nowrap;
  }
  .switch-title span i,
  .switch-status i {
    width: 0.3rem;
    height: 0.3rem;
    border-radius: 50%;
    background: var(--sl-color-white);
    box-shadow: 0 0 0.45rem color-mix(in oklab, var(--sl-color-white) 55%, transparent);
  }
  .switch-chassis {
    margin: 0 0.65rem;
    padding: 0.7rem;
    border: 1px solid var(--flow-strong);
    border-radius: 0.15rem;
    background: color-mix(in oklab, var(--sl-color-white) 3%, var(--sl-color-bg));
  }
  .switch-status {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-bottom: 0.55rem;
    color: var(--flow-muted);
    font: 0.42rem var(--sl-font-mono);
  }
  .switch-status i:last-child { opacity: 0.24; box-shadow: none; }
  .port-bank {
    display: grid;
    grid-template-columns: repeat(var(--port-count), minmax(2.4rem, 1fr));
    gap: 0.35rem;
  }
  .port { min-width: 0; color: var(--flow-muted); text-align: center; }
  .port > span,
  .port small {
    display: block;
    overflow: hidden;
    font: 0.4rem var(--sl-font-mono);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .jack {
    display: flex;
    justify-content: space-evenly;
    align-items: flex-start;
    height: 1.55rem;
    margin: 0.2rem 0;
    padding: 0.22rem;
    border: 1px solid var(--flow-strong);
    border-radius: 0.1rem;
    background: var(--sl-color-bg);
  }
  .port[data-kind="vm"] .jack { border-style: dashed; }
  .jack i { width: 1px; height: 0.38rem; background: var(--flow-strong); }
  .port small { color: var(--sl-color-white); font-size: 0.43rem; }
  .switch-node footer {
    margin-top: 0.65rem;
    padding: 0.55rem 0.9rem;
    border-top: 1px solid var(--flow-faint);
  }
  :global(.svelte-flow__handle) {
    width: 0.55rem;
    height: 0.55rem;
    border: 1px solid var(--sl-color-white);
    background: var(--sl-color-bg);
  }
</style>
