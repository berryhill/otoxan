# Configuration

Otoxan reads configuration from two sources, in order of increasing precedence:

1. `$OTOXAN_HOME/config.yaml` (or `$XDG_DATA_HOME/otoxan/config.yaml`)
2. Environment variables prefixed with `OTOXAN_`

Environment variables always override the YAML file.

## YAML Schema

```yaml
# config.yaml
default_agent: "default"          # Agent ID used when none is specified
mongo_uri: ""                     # MongoDB connection string (optional if Infisical provides it)
mongo_db: "otoxan"                # Database name
strict_mode: false                # Reject unknown OTOXAN_* env vars

infisical:
  base_url: "https://app.infisical.com"
  token: ""                       # Service token (prefer env var)
  project_id: ""                    # Infisical project ID
  env: "dev"                        # Environment slug (dev, staging, prod)

agents:
  alice:
    profile_path: "~/.otoxan/profiles/alice"
    role: "admin"
```

## Environment Variables

| Variable | Maps to | Default |
|----------|---------|---------|
| `OTOXAN_HOME` | Config directory path | `$XDG_DATA_HOME/otoxan` or `~/.local/share/otoxan` |
| `OTOXAN_DEFAULT_AGENT` | `default_agent` | `default` |
| `OTOXAN_MONGO_URI` | `mongo_uri` | *(none)* |
| `OTOXAN_MONGO_DB` | `mongo_db` | `otoxan` |
| `OTOXAN_INFISICAL_BASE_URL` | `infisical.base_url` | `https://app.infisical.com` |
| `OTOXAN_INFISICAL_TOKEN` | `infisical.token` | *(none)* |
| `OTOXAN_INFISICAL_PROJECT_ID` | `infisical.project_id` | *(none)* |
| `OTOXAN_INFISICAL_ENV` | `infisical.env` | `dev` |
| `OTOXAN_STRICT_MODE` | `strict_mode` | `false` |

## Strict Mode

Set `OTOXAN_STRICT_MODE=true` to make the loader reject any `OTOXAN_*` variable
it does not recognise. This catches typos early.

## Missing Config File

If `config.yaml` is absent, the loader returns defaults and logs a warning.
No fatal error is raised so that a fully-env-driven deployment works out of the box.

## Hermes Shadow Mode

During the Hermes migration, otoxan runs alongside the legacy system. The
`OTOXAN_` prefix guarantees that otoxan never reads Hermes variables (e.g.
`HERMES_MONGO_URI`) and vice-versa.
