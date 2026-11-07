# Zelta Contrib

This directory contains optional project-adjacent tools and integrations. They are useful with Zelta, but are not required by the core runtime.

Current areas:

- `agent-skills/`: Portable agent skill files for Zelta-aware assistants.
- `belta/`: Experimental or legacy related material.
- `web-install.sh`: One-shot web installer for archive-based installs.
- `zrecurseback/`: Additional ZFS helper material.

Keep core Zelta behavior in `bin/`, `share/zelta/`, and `doc/`. Use `contrib/` for discoverable extras that should travel with the repository but should not be installed or loaded by default.
