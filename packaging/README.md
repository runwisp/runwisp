# Packaging

Generators for third-party package managers. The Homebrew formula reads the
published release's `checksums-sha256.txt`, so generate it only after the
GitHub release is public.

```bash
gh release download v1.2.0 --repo runwisp/runwisp --pattern checksums-sha256.txt --dir /tmp
```

## Homebrew (`runwisp/homebrew-tap`)

`release.yml` runs this on every stable release and pushes the result to the
tap's `main`. It needs the `HOMEBREW_TAP_TOKEN` repo secret (fine-grained PAT,
contents: write on `runwisp/homebrew-tap`). Without it the job fails.

```bash
packaging/homebrew/formula.sh 1.2.0 /tmp/checksums-sha256.txt > Formula/runwisp.rb
```

## Linux packages (`linux/`, pkg.runwisp.com)

`release.yml` builds `runwisp-linux-{x64,arm64}.{deb,rpm}` with
[nfpm](https://nfpm.goreleaser.com) into every release draft, next to the
tarballs. Each package installs `/usr/lib/runwisp/runwisp` (symlinked from
`/usr/bin/runwisp`) and the RunWisp apt or dnf repository entry.

On every stable release, the `linux-repo` job rebuilds that repository from the
release's packages and deploys it to the `runwisp-packages` Cloudflare Pages
project, served as `pkg.runwisp.com`. It needs these repo secrets:

- `PACKAGES_GPG_KEY`: the armored private key that signs the rpms and the
  repository metadata. `linux/runwisp.asc` is its public half, which every
  package ships. Changing the key means users must re-import it by hand.
- `CLOUDFLARE_API_TOKEN` (Pages: Edit) and `CLOUDFLARE_ACCOUNT_ID`.

```bash
packaging/linux/packages.sh 1.2.0 apps/runwisp/dist out   # RPM_SIGNING_KEY=key.asc signs the rpms
packaging/linux/repo.sh 1.2.0 out site                   # needs apt-ftparchive, createrepo_c, gpg
packaging/linux/tests/packages_test.sh                   # install + upgrade in Debian and Fedora containers
packaging/linux/tests/repo_smoke.sh https://pkg.runwisp.com 1.2.0   # install from a deployed repository
```

`linux-packages-rehearsal.yml` runs the release's packaging and publishing
actions with the real key and Cloudflare project, deploying to the
`rehearsal` preview branch instead of `pkg.runwisp.com`. It runs on PRs that
touch the packaging, and by hand with
`gh workflow run linux-packages-rehearsal.yml`.
