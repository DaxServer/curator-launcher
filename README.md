# curator-launcher

A Go launcher for the [Curator](https://github.com/DaxServer/curator) application on [Wikimedia Toolforge](https://wikitech.wikimedia.org/wiki/Portal:Toolforge). It handles downloading, verifying, and exec-ing the correct binaries at container startup so the image itself stays small and always runs the latest release.

## Components

### curator-launcher (`main.go`)

Downloads the `curator-server` binary from the latest [DaxServer/curator](https://github.com/DaxServer/curator) GitHub release, verifies its [Sigstore attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) against the expected GitHub Actions workflow, then execs into it. The container never ships a stale binary.

### dragonfly-launcher (`cmd/dragonfly-launcher/`)

Downloads the [DragonflyDB](https://github.com/dragonflydb/dragonfly) binary for the running architecture (`amd64` / `arm64`), auto-detects the cgroup memory limit (v1 and v2), and execs into DragonflyDB with the appropriate flags.

## Build

```sh
toolforge build start -i launcher https://github.com/DaxServer/curator-launcher.git -L
```

`launcher` is the image name. The `-L` flag enables the latest buildpacks (required for Go module support).

## Environment Variables

### curator-launcher

| Variable | Required | Description |
|---|---|---|
| `GITHUB_TOKEN` | No | GitHub API token — avoids unauthenticated rate limits when fetching the release and attestations |

### dragonfly-launcher

| Variable | Required | Description |
|---|---|---|
| `GITHUB_TOKEN` | No | GitHub API token — avoids unauthenticated rate limits when fetching the release |
| `REDIS_PASSWORD` | No | Sets `--requirepass` on DragonflyDB |
| `REDIS_DB` | No | Sets `--dbnum` on DragonflyDB (must be a valid integer) |

## Deployment

### Initial deploy

```sh
toolforge webservice buildservice --mount=all start --image tool-launcher/tool-launcher:latest
```

### DragonflyDB worker

```sh
toolforge jobs run dragonfly --image tool-launcher/tool-launcher:latest --command "dragonfly-launcher" --continuous --emails all
```

## Development

Requires **Go 1.26+**.

```sh
# Build all binaries
go build ./...

# Run tests
go test ./...
```

## License

[MIT](LICENSE)
