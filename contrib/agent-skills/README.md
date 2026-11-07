# Zelta Agent Skills

This directory contains portable agent skill files for assistants that help users teach, install, configure, and operate Zelta.

The files are included in the repository so agents and users can discover the same Zelta-specific workflow guidance without depending on one website, editor, or agent runtime.

## Layout

- `zelta/SKILL.md`: General Zelta workflow skill.

## Publishing

This repository is the source location for these skill files. Website mirroring or `.well-known/agent-skills/` publishing is back-office tooling handled outside this repository.

Do not add website API clients, tokens, or uploader scripts here unless they become generally useful open-source tooling.

## Updating Skills

When updating a skill:

- Keep the skill self-contained enough to work outside this checkout.
- Link back to the repository and public documentation for deeper references.
- Avoid OpenCode-only assumptions unless the file is explicitly for OpenCode.
- Keep safety guidance concrete; Zelta workflows can affect real datasets.
