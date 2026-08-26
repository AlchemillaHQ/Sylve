# Tag Checklist

Use this checklist for a stable `vMAJOR.MINOR.PATCH` release. The tagged commit must already be on `master`, have a successful CI run, and contain its matching release changelog.

## Prepare the release

- [ ] Choose the release version without the `v` prefix, such as `0.3.0`; its tag will be `v0.3.0`.
- [ ] Run `npm audit --prefix web` and review any dependency changes needed for the release.
- [ ] Run `go mod tidy` and review the resulting module changes.
- [ ] Run `./scripts/add_license.sh` to add missing license headers.
- [ ] Run `./scripts/version.sh <version>` to update the backend, web, documentation, and API versions.
- [ ] Run `./scripts/generate_swagger.sh` to regenerate the Swagger documentation.
- [ ] Update `docs/app-docs` and other user documentation as needed.
- [ ] Create `docs/changelogs/v<version>.md`. Start it with an introductory paragraph beginning `Sylve v<version>`. Remove any `TODO`, `TBD`, or `PLACEHOLDER` text.
- [ ] Run `./scripts/validate-release-changelog.sh v<version>`.
- [ ] Review the changelog's upgrade notes, previous-version comparison link, documentation links, sponsor acknowledgments, and light/dark rendering.

## Validate and tag

- [ ] Commit all release preparation changes and push the commit to `master`.
- [ ] Wait for the **CI** workflow on that exact commit to complete successfully.
- [ ] Record the exact successful `master` commit and confirm it is still the intended release commit.
- [ ] Create and push the `v<version>` tag pointing to that commit.

## Review and publish

- [ ] Wait for the **Release** workflow to create or update the draft GitHub release.
- [ ] Confirm the draft body exactly matches `docs/changelogs/v<version>.md`.
- [ ] Confirm the draft contains `sylve-web-assets.tar.gz`, `sylve-web-demo-assets.tar.gz`, `sylve-amd64`, and `sylve-arm64`.
- [ ] Smoke-test the release artifacts as appropriate for the release.
- [ ] Publish the draft manually only after its notes and all expected assets have been reviewed.
