# Releasing Vega

Releases are automated via [GoReleaser](https://goreleaser.com/) and GitHub Actions.

## Distribution model

govega is private. Public distribution flows through two public sister repos:

| Repo | Visibility | Holds |
|---|---|---|
| `everydev1618/govega` | Private | Source code |
| `everydev1618/vega-releases` | **Public** | Built binaries (release artifacts only, no source) |
| `everydev1618/homebrew-tap` | **Public** | Homebrew formula (one file per release) |

End-user install: `brew install everydev1618/tap/vega`. Brew downloads the binary tarball from `vega-releases` (public, no auth) and the formula from `homebrew-tap`.

## How to release

Tag and push:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions (in govega) will automatically:

1. Build the React frontend (`serve/frontend/`)
2. Cross-compile binaries for macOS (arm64/amd64), Linux (arm64/amd64), Windows (amd64)
3. Inject the version into the binary (`vega version` prints the tag)
4. Create a GitHub Release on **`everydev1618/vega-releases`** with tarballs, zips, and checksums
5. Push an updated Homebrew formula to **`everydev1618/homebrew-tap`** (`Formula/vega.rb`)

## One-time setup

### Public sister repos

Create the two public repos (one-time):

```bash
gh repo create everydev1618/vega-releases --public \
  --description "Public release artifacts for Vega. Source lives at v3ga.dev."

gh repo create everydev1618/homebrew-tap --public \
  --description "Homebrew tap for everydev1618 tools. brew install everydev1618/tap/vega"
```

### GitHub secrets

`GITHUB_TOKEN` (auto-provided by Actions) only has access to the govega repo, so we override it with a fine-grained PAT that can push to the two public repos.

Mint a fine-grained PAT (Settings > Developer settings > Personal access tokens > Fine-grained):

- **Resource owner:** `everydev1618`
- **Repository access:** Only select repositories → `vega-releases`, `homebrew-tap`
- **Permissions:** `Contents: Read and write`

Add to govega repo secrets (Settings > Secrets and variables > Actions):

| Secret | Value |
|---|---|
| `RELEASES_TOKEN` | The PAT (used by goreleaser to publish releases to vega-releases) |
| `HOMEBREW_TAP_TOKEN` | The same PAT (used by goreleaser to push the formula to homebrew-tap) |

A single PAT works for both since the scopes overlap. They're named separately so we can rotate independently if needed later.

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
