# GunTanks Server

Authoritative Go server for the GunTanks multiplayer client. It provides JWT authentication, matchmaking, custom rooms, WebSocket battle commands, deterministic battle simulation, terrain snapshots, reconnect handling, and match history.

## Local development

Start MongoDB and Redis (Docker example):

```powershell
docker run --name guntanks-mongo -p 27017:27017 -d mongo:8
docker run --name guntanks-redis -p 6379:6379 -d redis:8
```

Run the server from this directory:

```powershell
$env:JWT_SECRET='replace-this-development-secret'
go run .
```

Open `http://127.0.0.1:8889`. The server serves the sibling `guntanks-client` directory by default.

When MongoDB is unavailable, development mode uses an in-memory store. Redis readiness is reported by `/api/v1/health/ready`; persistent accounts, match history, and cross-process leases require the real services.

## Configuration

See `.env.example`. Environment variables are read directly by the process. Important defaults are `WEB_ADDR=:8889`, `STATIC_DIR=../guntanks-client`, `TURN_TIMEOUT_SECONDS=30`, `RECONNECT_GRACE_SECONDS=60`, and `BATTLE_TICK_HZ=60`.

Production deployments must set a strong `JWT_SECRET`, make MongoDB and Redis mandatory at the process supervisor level, terminate TLS, and restrict WebSocket origins.

## Verification

```powershell
$env:GOCACHE='C:\GoProject\GunTanks\.gocache'
go test ./...
go test -race ./...
cd ..\guntanks-client
node build.mjs
node --check src\main.js
node --check src\socket.js
```

The race build requires a C compiler supported by the installed Go toolchain.
