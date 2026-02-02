import React from "react";
import { CopyButton } from "./CopyButton";
import HeroFrame from "./HeroFrame";

export const Hero = () => {
  return (
    <div className="text-center pt-10 bg-cover bg-center bg-background">
      <div
        className="inline-flex items-center gap-2 bg-muted text-muted-foreground px-3 py-1 rounded-full text-sm font-medium mb-8 -translate-y-4 animate-fade-in opacity-0"
        style={{ "--animation-delay": "500ms" } as React.CSSProperties}
      >
        <div className="w-2 h-2 rounded-full shrink-0 bg-foreground"></div>
        Free & Open Source
      </div>

      <h1
        className="dark-text-gradient text-gradient my-6 text-6xl md:text-8xl -translate-y-4 animate-fade-in opacity-0 font-normal"
        style={{ "--animation-delay": "500ms" } as React.CSSProperties}
      >
        {" "}
        FreeBSD Management
        <span className="block mt-2.5"> Made Simple</span>
      </h1>

      <p
        className="mb-12 text-lg text-muted-foreground md:text-xl -translate-y-4 animate-fade-in opacity-0"
        style={{ "--animation-delay": "500ms" } as React.CSSProperties}
      >
        Manage FreeBSD like never before
        <br className="hidden md:block" />
        fast, intuitive, and reliable.
      </p>

      <div
        className="flex justify-center items-center mt-10  -translate-y-4 animate-fade-in opacity-0 gap-4"
        style={{ "--animation-delay": "500ms" } as React.CSSProperties}
      >
        {" "}
        <a
          href="/guides/example/"
          className=" inline-flex items-center justify-between border px-4 py-2.5 rounded-lg font-mono text-sm dark:bg-foreground dark:text-background"
        >
          Read Docs
        </a>
        <div className="card-wrapper h-11 border">
          <div className="card-content inline-flex items-center justify-between px-4 py-2 rounded-lg font-mono text-sm">
            <span className="text-foreground">$</span>
            <span>pkg install sylve</span>
            <CopyButton text="pkg install sylve" />
          </div>
        </div>
      </div>

      <HeroFrame />
    </div>
  );
};

export default Hero;
