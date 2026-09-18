# GoRaffle
A docker deploy-able version of the draw-without-placing-back raffle.

Good for picking whose turn it is when everyone should have a go eventually. Also good for picking for sports. Or for picking what to eat.

## Installation

Requires [Docker](https://docs.docker.com/get-docker/) and Docker Compose.

There are two ways to run GoRaffle: pull the prebuilt image (fastest, no source needed), or build it yourself from source.

### Option A: Use the prebuilt image

The published image is pulled from `ghcr.io`, so you only need the compose file and an env file — no source required:

```bash
mkdir goraffle && cd goraffle
curl -O https://raw.githubusercontent.com/Cyberfly100/GoRaffle/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/Cyberfly100/GoRaffle/main/.env.example
cp .env.example .env
```

Edit `.env` and set a real `POSTGRES_PASSWORD` before the first start (see note below), then:

```bash
docker compose up -d
```

### Option B: Build from source

Clone the full repo and build the image locally instead of pulling it:

```bash
git clone https://github.com/Cyberfly100/GoRaffle.git
cd GoRaffle
cp .env.example .env
```

Edit `.env` and set a real `POSTGRES_PASSWORD` before the first start (see note below), then either point `docker-compose.yml`'s `raffle` service at `build: .` instead of `image: ghcr.io/...`, or build and bring it up in one step:

```bash
docker compose up -d --build
```

### First-start note

Postgres only reads `POSTGRES_PASSWORD` when initializing a fresh volume, so it can't be changed later just by editing `.env`. Set it to a real value before the first `docker compose up`.

GoRaffle will be available at `http://localhost:8543` (or whatever `RAFFLE_PORT` you set in `.env`).

**Environment variables** (`.env`):

| Variable | Default | Description |
|---|---|---|
| `POSTGRES_DB` | `raffle` | Database name |
| `POSTGRES_USER` | `user` | Database user |
| `POSTGRES_PASSWORD` | `pass` | Database password — **change before first start** |
| `RAFFLE_PORT` | `8543` | Host port the app is exposed on |
| `PGDATA_DIR` | *(unset)* | Optional host path for Postgres data (e.g. a NAS dataset); leave unset to use a Docker volume |

To stop the app: `docker compose down`. To wipe all data: `docker compose down -v`.
