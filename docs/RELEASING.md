# Releasing Vega

Releases are automated via [GoReleaser](https://goreleaser.com/) and GitHub Actions.

## How to release

Tag and push:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions will automatically:

1. Build the React frontend (`serve/frontend/`)
2. Cross-compile binaries for macOS (arm64/amd64), Linux (arm64/amd64), Windows (amd64)
3. Inject the version into the binary (`vega version` prints the tag)
4. Create a GitHub Release with tarballs, zips, and checksums
5. Push a Homebrew formula to `everydev1618/homebrew-tap`

## One-time setup

### Homebrew tap repo

Create an empty repo at `github.com/everydev1618/homebrew-tap`. GoReleaser pushes a formula there on each release.

### GitHub secrets

In the govega repo settings (Settings > Secrets and variables > Actions), add:

| Secret | Purpose |
|---|---|
| `HOMEBREW_TAP_TOKEN` | Personal Access Token with `repo` scope. Used by GoReleaser to push the formula to the tap repo. |

`GITHUB_TOKEN` is provided automatically by GitHub Actions.

## Testing a release locally

Install GoReleaser, then:

```bash
goreleaser release --snapshot --clean
```

This builds everything in `dist/` without publishing. Useful for verifying the config.

## Version injection

`cmd/vega/main.go` declares `var version = "dev"`. GoReleaser overrides this at build time via:

```
-ldflags "-X main.version={{.Version}}"
```

So `vega version` prints `dev` in development and the tag version in releases.

## Files

| File | Purpose |
|---|---|
| `.goreleaser.yml` | GoReleaser config (builds, archives, homebrew, changelog) |
| `.github/workflows/release.yml` | GitHub Actions workflow triggered by `v*` tags |
