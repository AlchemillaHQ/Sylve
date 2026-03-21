<script lang="ts">
  import { onMount } from "svelte";

  type Props = {
    className?: string;
    quantity?: number;
    staticity?: number;
    ease?: number;
    size?: number;
    refresh?: boolean;
    color?: string;
    vx?: number;
    vy?: number;
  };

  const {
    className = "",
    quantity = 100,
    staticity = 50,
    ease = 50,
    size = 0.4,
    refresh = false,
    color = "#ffffff",
    vx = 0,
    vy = 0,
  }: Props = $props();

  let canvasRef: HTMLCanvasElement;
  let canvasContainerRef: HTMLDivElement;
  let context: CanvasRenderingContext2D | null = null;
  let circles: Circle[] = [];
  let animationFrame = 0;
  let dpr = 1;

  const mouse = { x: 0, y: 0 };
  const canvasSize = { w: 0, h: 0 };

  interface Circle {
    x: number;
    y: number;
    translateX: number;
    translateY: number;
    size: number;
    alpha: number;
    targetAlpha: number;
    dx: number;
    dy: number;
    magnetism: number;
  }

  function hexToRgb(hex: string): number[] {
    let normalized = hex.replace("#", "");

    if (normalized.length === 3) {
      normalized = normalized
        .split("")
        .map((char) => char + char)
        .join("");
    }

    const hexInt = Number.parseInt(normalized, 16);
    return [(hexInt >> 16) & 255, (hexInt >> 8) & 255, hexInt & 255];
  }

  function circleParams(): Circle {
    return {
      x: Math.floor(Math.random() * canvasSize.w),
      y: Math.floor(Math.random() * canvasSize.h),
      translateX: 0,
      translateY: 0,
      size: Math.floor(Math.random() * 2) + size,
      alpha: 0,
      targetAlpha: Number.parseFloat((Math.random() * 0.6 + 0.1).toFixed(1)),
      dx: (Math.random() - 0.5) * 0.1,
      dy: (Math.random() - 0.5) * 0.1,
      magnetism: 0.1 + Math.random() * 4,
    };
  }

  function clearContext() {
    if (!context) return;
    context.clearRect(0, 0, canvasSize.w, canvasSize.h);
  }

  function drawCircle(circle: Circle, update = false) {
    if (!context) return;

    const rgb = hexToRgb(color);

    context.translate(circle.translateX, circle.translateY);
    context.beginPath();
    context.arc(circle.x, circle.y, circle.size, 0, 2 * Math.PI);
    context.fillStyle = `rgba(${rgb.join(", ")}, ${circle.alpha})`;
    context.fill();
    context.setTransform(dpr, 0, 0, dpr, 0, 0);

    if (!update) circles.push(circle);
  }

  function resizeCanvas() {
    if (!canvasContainerRef || !canvasRef || !context) return;

    circles = [];
    canvasSize.w = canvasContainerRef.offsetWidth;
    canvasSize.h = canvasContainerRef.offsetHeight;

    canvasRef.width = canvasSize.w * dpr;
    canvasRef.height = canvasSize.h * dpr;
    canvasRef.style.width = `${canvasSize.w}px`;
    canvasRef.style.height = `${canvasSize.h}px`;

    context.setTransform(1, 0, 0, 1, 0, 0);
    context.scale(dpr, dpr);
  }

  function drawParticles() {
    clearContext();
    for (let i = 0; i < quantity; i += 1) {
      drawCircle(circleParams());
    }
  }

  function remapValue(value: number, start1: number, end1: number, start2: number, end2: number): number {
    const remapped = ((value - start1) * (end2 - start2)) / (end1 - start1) + start2;
    return remapped > 0 ? remapped : 0;
  }

  function animate() {
    clearContext();

    circles.forEach((circle, index) => {
      const edge = [
        circle.x + circle.translateX - circle.size,
        canvasSize.w - circle.x - circle.translateX - circle.size,
        circle.y + circle.translateY - circle.size,
        canvasSize.h - circle.y - circle.translateY - circle.size,
      ];

      const closestEdge = edge.reduce((a, b) => Math.min(a, b));
      const remapClosestEdge = Number.parseFloat(remapValue(closestEdge, 0, 20, 0, 1).toFixed(2));

      if (remapClosestEdge > 1) {
        circle.alpha += 0.02;
        if (circle.alpha > circle.targetAlpha) {
          circle.alpha = circle.targetAlpha;
        }
      } else {
        circle.alpha = circle.targetAlpha * remapClosestEdge;
      }

      circle.x += circle.dx + vx;
      circle.y += circle.dy + vy;
      circle.translateX += (mouse.x / (staticity / circle.magnetism) - circle.translateX) / ease;
      circle.translateY += (mouse.y / (staticity / circle.magnetism) - circle.translateY) / ease;

      drawCircle(circle, true);

      if (
        circle.x < -circle.size ||
        circle.x > canvasSize.w + circle.size ||
        circle.y < -circle.size ||
        circle.y > canvasSize.h + circle.size
      ) {
        circles.splice(index, 1);
        drawCircle(circleParams());
      }
    });

    animationFrame = window.requestAnimationFrame(animate);
  }

  function initCanvas() {
    resizeCanvas();
    drawParticles();
  }

  function onMouseMove(event: MouseEvent) {
    if (!canvasRef) return;

    const rect = canvasRef.getBoundingClientRect();
    const x = event.clientX - rect.left - canvasSize.w / 2;
    const y = event.clientY - rect.top - canvasSize.h / 2;
    const inside = x < canvasSize.w / 2 && x > -canvasSize.w / 2 && y < canvasSize.h / 2 && y > -canvasSize.h / 2;

    if (inside) {
      mouse.x = x;
      mouse.y = y;
    }
  }

  onMount(() => {
    dpr = typeof window !== "undefined" ? window.devicePixelRatio : 1;
    context = canvasRef.getContext("2d");
    initCanvas();
    animate();

    window.addEventListener("resize", initCanvas);
    window.addEventListener("mousemove", onMouseMove);

    return () => {
      window.removeEventListener("resize", initCanvas);
      window.removeEventListener("mousemove", onMouseMove);
      window.cancelAnimationFrame(animationFrame);
    };
  });

  $effect(() => {
    color;
    if (!context) return;
    initCanvas();
  });

  $effect(() => {
    refresh;
    if (!context) return;
    initCanvas();
  });
</script>

<div class={className} bind:this={canvasContainerRef} aria-hidden="true">
  <canvas bind:this={canvasRef} class="size-full"></canvas>
</div>
