# pindock

> Pin and update Docker image digests in Dockerfiles and compose files

[![PyPI: Version](https://img.shields.io/pypi/v/pindock?logo=pypi&logoColor=white)](https://pypi.org/project/pindock)
[![AUR: version](https://img.shields.io/aur/version/pindock-bin?logo=archlinux&logoColor=white)](https://aur.archlinux.org/packages/pindock-bin)
[![GitHub: Release](https://img.shields.io/github/v/release/deadnews/pindock?logo=github&logoColor=white)](https://github.com/deadnews/pindock/releases/latest)
[![Docker: ghcr](https://img.shields.io/badge/docker-gray.svg?logo=docker&logoColor=white)](https://github.com/deadnews/pindock/pkgs/container/pindock)
[![CI: Main](https://img.shields.io/github/actions/workflow/status/deadnews/pindock/main.yml?branch=main&logo=github&logoColor=white&label=main)](https://github.com/deadnews/pindock)
[![CI: Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/deadnews/pindock/refs/heads/badges/coverage.json)](https://github.com/deadnews/pindock)

**[Installation](#installation)** • **[Usage](#usage)** • **[Pre-commit](#pre-commit)**

## Installation

```sh
# PyPI
uv tool install pindock

# AUR
yay -S pindock-bin

# Docker
docker pull ghcr.io/deadnews/pindock
```

## Usage

```sh
Usage: pindock <command> [flags]

Pin and update Docker image digests.

Commands:
  run      Pin unpinned image digests.
  check    Verify all images are pinned.

run flags:
  -C, --dir=.      Directory to scan.
  -u, --update     Also update tags and pinned digests to latest.
  -v, --verbose    Show all images, including pinned.

check flags:
  -C, --dir=.      Directory to scan.
  -u, --update     Also check tags and pinned digests for updates.
  -v, --verbose    Show all images, including pinned.
```

When no files are given, `pindock` auto-discovers files recursively.

### Supported files

- `Dockerfile`, `Containerfile` (and variants like `Dockerfile.dev`, `*.dockerfile`)
- `compose*.yml`, `docker-compose*.yml` (and `.yaml`)

### Supported instructions

| Dockerfile                                           | Compose                     |
| ---------------------------------------------------- | --------------------------- |
| `FROM [--platform=...] image:tag[@digest] [AS name]` | `image: image:tag[@digest]` |
| `COPY --from=image:tag[@digest] ...`                 |                             |
| `RUN --mount=from=image:tag[@digest],... ...`        |                             |

### Authentication

Uses existing Docker credentials. If you can `docker pull`, `pindock` works too.

## Pre-commit

```yml
repos:
  - repo: https://github.com/deadnews/pindock
    rev: v1.0.0
    hooks:
      - id: pindock
      - id: pindock-check

      # example with args
      - id: pindock-check
        args: [--update, --verbose]
```
