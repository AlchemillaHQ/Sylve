# Tag Checklist

This is a checklist of items to review before tagging a new release. It is not exhaustive, but it should cover the most important items.

- [ ] Make sure CI build/tests pass for the last commit on the master branch
- [ ] Run `npm audit fix --prefix web` to fix any vulnerabilities in web dependencies
- [ ] Run `go mod tidy` to clean up Go dependencies
- [ ] Run `./scripts/add_license.sh` to add license headers to all source files
- [ ] Run `./scripts/version.sh <version>` to update the version number(s) in the code
- [ ] Run `./scripts/generate_swagger.sh` to generate swagger documentation
- [ ] Update documentation in `docs/app-docs` if necessary
- [ ] Make a commit with the changes and push to master
