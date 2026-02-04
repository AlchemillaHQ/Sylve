import { useState } from "react";
import { Copy, Check, LoaderCircle } from "lucide-react";

export function CopyButton({ text }: { text: string }) {
  const [status, setStatus] = useState<"idle" | "loading" | "success">("idle");

  const handleCopy = async () => {
    try {
      setStatus("loading");

      if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const textarea = document.createElement("textarea");
        textarea.value = text;
        document.body.appendChild(textarea);
        textarea.select();
        document.execCommand("copy");
        document.body.removeChild(textarea);
      }
      setTimeout(() => setStatus("success"), 500);
      setTimeout(() => setStatus("idle"), 3000);
    } catch (err) {
      console.error("Failed to copy:", err);
      setStatus("idle");
    }
  };

  const Icon =
    status === "idle" ? Copy : status === "loading" ? LoaderCircle : Check;

  return (
    <button
      onClick={handleCopy}
      title="Copy to clipboard"
      className="flex items-center rounded-md px-1.5 py-1 text-sm hover:bg-muted disabled:opacity-50 cursor-pointer"
      disabled={status === "loading"}
    >
      <Icon
        className={`h-4 w-4 ${status === "loading" ? "animate-spin" : ""}`}
      />
    </button>
  );
}
