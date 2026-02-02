import React from "react";
import { useInView } from "react-intersection-observer";
import { cn } from "../lib/utils";

export default function HeroFrame() {
  const { ref, inView } = useInView({
    threshold: 0.6,
    triggerOnce: true,
  });

  return (
    <div
      ref={ref}
      className="mt-16 perspective-[2000px] opacity-0 animate-fade-in lg:w-[65%] mx-auto"
      style={{ "--animation-delay": "600ms" } as React.CSSProperties}
    >
      <div
        className={cn(
          "relative rounded-lg bg-opacity-[0.01] bg-hero-gradient",
          inView
            ? "animate-image-rotate before:animate-image-glow"
            : "transform-[rotateX(25deg)]",
          "before:absolute before:inset-0 before:bg-hero-glow before:opacity-0 before:filter-[blur(120px)] before:pointer-events-none",
        )}
      >
        <iframe
          src="https://hayzam.com"
          className="relative z-10 w-full h-[600px] rounded-lg border bg-background"
          loading="lazy"
          referrerPolicy="no-referrer"
        />
      </div>
    </div>
  );
}
