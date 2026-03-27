# Zelta Style Guide & Coding Standards

This document defines the coding standards for the Zelta Backup and Recovery Suite. Zelta is designed for **portability** and **safety** and is written in portable Bourne Shell and Awk.

Lean toward POSIX and "Original Awk" standards, but don't adhere obsessively to any single standard. Some POSIX features are absent from certain OS defaults, so everything needs testing on real systems.

---

## 1. Naming Conventions

Casing denotes variable scope and type.

| Type | Case Style | Example | Context |
| :--- | :--- | :--- | :--- |
| **Constants** | CAPS | `SNAP_NAME` | Values unchanged during runtime |
| **Globals** | CamelCase | `Dataset`, `NumDS` | Global state variables |
| **Locals** | _snake_case | `_idx`, `_temp_val` | Variables internal to a function or loop |
| **Arguments** | lowercase | `target_ds`, `flag` | Variables passed into a function |
| **Array Keys (Const)** | "CAPS" | `Opt["VERBOSE"]` | Settings or fixed keys |
| **Array Keys (Local)** | "lowercase" | `Dataset[ds_suffix, "match"]` | Keys applied during runtime |

---

## 2. Core Vocabulary

We follow OpenZFS language where possible, with these clarifications:

*   **endpoint (ep):** Location and name of a ZFS object.
*   **dataset (ds):** A specific individual ZFS dataset.
*   **tree:** A dataset and its recursive children.
*   **dataset tree:** Preferred term for recursive operations on a dataset and all descendants.
*   **ds_snap:** A specific snapshot instance (e.g., `pool/data@snap1`).
*   **ds_suffix:** Relative path of a child within a tree, with leading `/`.
    *   *Example:* If root is `zroot/usr` and we process `zroot/usr/local`, the `ds_suffix` is `/local`.

---

## 3. Data Structures

Global state is managed via multidimensional associative arrays in AWK.

### `Opt[]`
**User Settings.** Defined in `zelta` shell script; parsing rules in `zelta-opts.tsv`.
*   Index: `Opt["VARIABLE_NAME"]`

### `Dataset[]`
**Properties of each dataset.** Indexed by endpoint, suffix, and property name.
*   **Index:** `(endpoint, ds_suffix, property)`
*   **Standard Properties:**
    *   `"exists"`: Boolean
    *   `"earliest_snapshot"`: Oldest snapshot
    *   `"latest_snapshot"`: Newest snapshot
    *   `[zfs_property]`: Any native ZFS property (e.g., `"compression"`, `"origin"`)

### `DSPair[]`
**Derived Replication State.** Compares a dataset with its replica.
*   **Index:** `(ds_suffix, property)`
*   **Standard Properties:**
    *   `"match"`: Common snapshot or bookmark between source and target
    *   `"source_start"`: Incremental source snapshot/bookmark for send basis
    *   `"source_end"`: Target snapshot to sync

### `DSTree[]`
**Global Tree Metadata.**
*   **Index:** `(property)` or `(endpoint, property)`
*   **Standard Properties:**
    *   `"SRC", "count"`: Number of datasets on source
    *   `"TGT", "count"`: Number of datasets on target

### Global Scalars
*   `NumDS`: Dataset count in current tree
*   `DSList`: Ordered list of `ds_suffix` elements in replication order

---

## 4. Coding Standards

### Bourne Shell (`/bin/sh`)
*   **Shebang:** `#!/bin/sh`
*   **No Bashisms:** No arrays (`arr=(...)`), no `[[ ]]`, no `function name() {`. Use `name() {`.
*   **Variables:** Quote all variables unless tokenization is explicitly desired.
*   **Indentation:** Tabs for blocks, spaces for inline alignment.

### AWK (`awk`)
*   **Dialect:** Original-Awk (bwk/nawk) style; must run on stock FreeBSD, Illumos, and Debian `awk`.
*   **Indentation:** Same as shell.

### Portability
*   Assume the environment is hostile.
*   Assume `grep` behavior varies.
*   Do not assume GNU or BSD extensions.

---

## 5. Comments

Good comments explain **why** and **what**, not just **how**.

### Comment Styles

| Style | Usage | Example |
| :--- | :--- | :--- |
| `##` | Section headers | `## Loading and setting properties` |
| `#` | Function headers, inline explanations | `# Build array key` |
| `# TO-DO:` | Future work | `# TO-DO: Extract to function` |

### What to Comment

*   **Complex Array Indexing:**
    ```awk
    # Dataset properties indexed by: (endpoint, ds_suffix, property)
    Dataset[_idx, "latest_snapshot"] = snap_name
    ```

*   **Business Logic:**
    ```awk
    # If first snapshot for source, update snap counter
    if (!_src_latest)
        Dataset[_idx, "earliest_snapshot"] = snap_name
    ```

*   **State Changes:**
    ```awk
    # Target updated via sync; becomes new match
    DSPair[ds_suffix, "match"] = snap_name
    ```

*   **External Dependencies:**
    ```awk
    # Run 'zfs match' and pass to parser
    _cmd = build_command("MATCH", _cmd_arr)
    ```

### What NOT to Comment
*   Obvious operations
*   Redundant descriptions that restate the code

### Section Organization
```awk
## Usage
########

## Loading and setting properties  
#################################

## Compute derived data
#######################
```

---

## 6. Documentation Standards

### Documentation Hierarchy

| Document Type | Audience | Tone | Purpose |
| :--- | :--- | :--- | :--- |
| **Man Pages** | Sysadmins at 4am | Strictly technical | Complete reference |
| **README.md** | Evaluators, new users | Professional, approachable | "What" and "why" |
| **Wiki Pages** | Community, learners | Conversational | How-to guides |
| **Code Comments** | Developers | Technical | Intent and context |

### Man Page Standards

Man pages must be **complete**, **precise**, **scannable**, and **example-driven**.

**Structure:**
```
NAME - Brief description
SYNOPSIS - Command syntax
DESCRIPTION - What it does
OPTIONS - All flags and arguments
EXAMPLES - Common use cases
EXIT STATUS - Return codes
NOTES - Important caveats
SEE ALSO - Related commands
AUTHORS - Credit
WWW - Project URL
```

**Formatting:**
*   `**bold**` for commands, options, user input
*   `*italic*` for arguments and placeholders
*   Escape dashes in options: `**\--verbose**`

### README.md Standards

The README is the project's front door: **welcoming**, **focused**, **honest**, **actionable**.

**Avoid:** Marketing hyperbole, antagonistic comparisons, excessive casualness, unsubstantiated claims.

**Prefer:** Concrete examples, factual statements, direct language, specific technical advantages.

### Terminology

| Preferred | Avoid | Context |
| :--- | :--- | :--- |
| dataset tree | recursive datasets | Parent + children |
| endpoint | location, target system | `user@host:pool/dataset` |
| backup | replication, sync | User-facing docs |
| replicate | sync, copy | Technical docs for ZFS `-R` behavior |
| snapshot | snap | Except in code/options |

### Writing Style

**Do:**
*   Use active voice
*   Start with the most common use case
*   Explain *why* before *how*
*   Use parallel structure in lists

**Don't:**
*   Use exclamation points in technical docs
*   Make unverifiable claims
*   Mix casual and formal tone

### Cross-References

*   Man pages: `**command(section)**` (e.g., `**zfs(8)**`)
*   Internal docs: Relative links
*   External docs: Full URLs
*   Be specific: "See EXCLUSION PATTERNS in **zelta-options(7)**"
