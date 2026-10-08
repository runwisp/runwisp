# Packaging

Generators for third-party package managers. They read the published
release's `checksums-sha256.txt`, so run them only after the GitHub release is
public.

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
