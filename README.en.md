English | [简体中文](README.md)

# BeefTV

BeefTV is a multi-user web workspace for AI film and short-drama creation, with projects, canvases, assets, and generation tasks. Administrators create accounts, provide AI models, and allocate integer credits. Each user has an isolated workspace.

## Quick start

```bash
docker compose build
docker compose run --rm -it backend platform-admin --data-dir /data/platform --username admin
docker compose up -d
```

Open `http://localhost:3000`, sign in as the administrator, then create accounts, publish model prices, and allocate credits. The initialization command reads the password interactively without echo. Users must change temporary passwords on first login.

## Deployment and security

- The platform uses single-host SQLite and a new `platform-data` volume. Do not reuse an existing single-user directory.
- Public deployments require HTTPS and `CANVAS_PUBLIC_ORIGIN`. Keep the frontend and API on one origin; expose only the frontend.
- Public registration is disabled. Administrators cannot browse other users' creative content. Upstream credentials are managed on the server.
- Users confirm a quote before generation. Credits are reserved atomically with task creation and settled against valid output. Uncertain results retain their reservation; recovery reuses the original task.
- The initial platform disables the assistant, skill scripts, user plugin installation, and external agents. Desktop publishing is disabled; existing single-user data remains intact.

## Documentation

- [Platform administration](docs/content/docs/backend/platform.mdx)
- [Local development](docs/content/docs/backend/local-development.mdx)
- [HTTP API](docs/content/docs/backend/http-api.mdx)
- [Database](docs/content/docs/backend/backend-database.mdx)
- [Documentation index](docs/index.md)
