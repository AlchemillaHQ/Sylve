# Scripts

These are some cool scripts that we've written to make our life easier.

## add_license.sh

This script adds a license to all files (.go and .ts specifically), this skips .svelte files as they are not supported yet.

## check_deps.sh

This script checks if all dependencies are installed and ready for use.

## generate_swagger.sh

This script generates a swagger file from the go code. This is useful for generating API documentation.

## build-csmwrap.sh

`make csmwrap` builds the pinned submodule under `third-party/csmwrap/source/`,
packages the helper ISO, and stages the amd64 embed inputs. See the
[CSMWrap README](../third-party/csmwrap/README.md) for prerequisites and licensing.

`./scripts/test-csmwrap.sh` checks bundle integrity and source pins using shell.

## download-csmwrap.sh

`make csmwrap-download` fetches the compressed ISO and build manifest from `tip`
using FreeBSD's `fetch` or `curl`. Build/test targets call this only when embed
inputs are missing. Downloads are disabled in CI.

### License

Please see the [LICENSE](../LICENSE) file for more information.
