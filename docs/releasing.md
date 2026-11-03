# Releasing Agentmux

Release from a reviewed `main` revision after its CI, installed first-run and
documentation checks pass. A versioned release must include the commands and
behavior promoted by its README and installation guide.

## Prepare and validate

1. Choose a new version; never move an existing published tag. Update the
   changelog, versioned installation commands and release description together.
2. Run relevant regressions and the comparison harness's offline controls.
   Confirm the Linux and macOS **Installed first run** jobs pass. Those jobs
   package all four supported archives, verify their checksums, install the
   host binary and complete `amx demo` through actual workflow evidence.
3. Review the source archive and distribution contents for private data. Include
   contribution/security instructions and the applicable license when selected.
4. Merge the reviewed revision and create the new version tag on that exact
   commit. Pushing a `v*` tag starts the release workflow.

The default release targets are Linux and macOS on x86-64 and ARM64. Native
execution in CI covers the runner architectures only; verifying a cross-built
archive's contents is not execution on that architecture. Full Bubblewrap
isolation is tested separately on Linux.

## Publication gates

The release workflow first checks tag freshness and existing publication state.
A complete published release is a successful no-op; an unfinished release
requires inspection and is never overwritten automatically.

GoReleaser creates an **unpublished draft** containing the four archives and
checksum manifest. `scripts/smoke-release.py` installs the host archive through
the normal checksum/version verifier and runs help, workflow discovery and
the deterministic demo. It requires passing revision-bound checks and a
confirmed application receipt, and retains private evidence in `smoke.json`.

`scripts/publish-release.py` then checks that every uploaded asset digest matches
the local verified bytes and rechecks the remote tag before publishing the
draft. Failed or uncertain verification leaves the draft unpublished. Inspect
the workflow and release state before resolving or retrying an interrupted run.

## Verify delivery

Download the published host archive and `checksums.txt` using the installer or
authenticated GitHub tooling as appropriate for repository access. A complete
independent smoke run needs all four platform archives:

```sh
gh release download v0.12.0 --repo pagel-s/agentmux --dir /tmp/amx-v0.12.0-artifacts
python3 scripts/smoke-release.py --artifact-dir /tmp/amx-v0.12.0-artifacts \
  --version v0.12.0 --output /tmp/amx-v0.12.0-installed-check
```

Choose new directories for every run. The smoke command never replaces an
existing installation and never makes model calls. Its passing result covers
the installed deterministic journey; native providers need their own recorded
checks. A checksum is consistency evidence against the release manifest, not
an independent signature.

If a published release is defective, create a new version. Preserve the prior
tag and assets, and document the affected behavior and upgrade path.
