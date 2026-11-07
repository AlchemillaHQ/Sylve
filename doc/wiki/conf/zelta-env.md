# Configuration: `zelta.env`

`zelta.env` stores defaults that apply across commands: SSH behavior, logging, snapshot naming, send/receive options, and retention defaults.

Zelta reads user-local configuration under `~/.config/zelta` when present; otherwise it uses `/usr/local/etc/zelta`. You can override locations with `ZELTA_ETC`, `ZELTA_ENV`, and `ZELTA_CONFIG`.

This file uses Bourne shell syntax: `KEY=value`. For the configuration overview, see [Environment & Policy Files](/docs/conf/env/). For the option reference, use `zelta help options` or `zelta-options(7)`.
