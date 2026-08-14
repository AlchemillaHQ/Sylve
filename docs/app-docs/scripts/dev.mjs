import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const docsDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const webDirectory = path.resolve(docsDirectory, "../../web");

const processes = [
  spawn("npm", ["run", "dev:docs"], {
    cwd: docsDirectory,
    stdio: "inherit",
  }),
  spawn(
    "npm",
    ["run", "dev:demo", "--", "--host", "127.0.0.1", "--port", "5173", "--strictPort"],
    {
      cwd: webDirectory,
      stdio: "inherit",
    },
  ),
];

let stopping = false;

function stop(exitCode = 0) {
  if (stopping) return;
  stopping = true;

  for (const child of processes) {
    if (!child.killed) child.kill("SIGTERM");
  }

  process.exitCode = exitCode;
}

for (const child of processes) {
  child.on("error", (error) => {
    console.error(error);
    stop(1);
  });

  child.on("exit", (code, signal) => {
    if (!stopping) stop(code ?? (signal ? 1 : 0));
  });
}

process.on("SIGINT", () => stop(0));
process.on("SIGTERM", () => stop(0));
