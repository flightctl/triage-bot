# Triage Bot

AI-powered Jira bug triage bot. Monitors projects for unresolved bugs,
runs configurable AI triage workflows, and posts structured assessment
reports as Jira comments. Labels issues suitable for automated fixing.
Deploys on OpenShift via Helm with multi-team support.

## How It Works

1. **Polls Jira** for unresolved bugs across configured projects
2. **Runs AI triage** via Claude Code CLI (or Gemini) using a
   pluggable workflow (default:
   [ai-workflows](https://github.com/flightctl/ai-workflows) triage
   `/assess` skill)
3. **Posts a formatted comment** on each bug with the triage assessment
   (recommendation, error signature, duplicates, regression hints)
4. **Labels AUTO_FIX candidates** when the AI assessment indicates
   high likelihood of automated fixing
5. **Re-triages** when a bug's description is updated (detected via
   description hash embedded in the comment)

## Features

- **Workflow-agnostic** — bring your own triage workflow via
  configurable task templates and git-importable skill repos
- **ADF rendering** — triage comments render with proper headings,
  tables, and formatting in Jira
- **Webhook support** — optional Jira webhook endpoint for
  near-real-time triage after description edits (HMAC-SHA256 verified)
- **Dry-run mode** — log assessments without writing to Jira
- **Multi-team** — deploy per-team instances with layered Helm values
- **OCP-native** — Helm chart with restricted SCC, Vertex AI support,
  and configurable secrets

## Quick Start

### Prerequisites

- Go 1.26+
- Podman or Docker
- Helm 3
- Access to an OpenShift cluster
- Jira API token
- GCP service account key for Vertex AI (or Anthropic API key)

### Build

```bash
make build
make push REGISTRY=quay.io/your-org
```

### Deploy

```bash
# Create namespace and secrets
oc create ns my-triage-bot

oc -n my-triage-bot create secret generic triage-bot-jira-token \
  --from-file=jira-api-token=/path/to/jira-token.txt

oc -n my-triage-bot create secret generic triage-bot-vertex \
  --from-file=gcp-sa-key.json=/path/to/gcp-sa-key.json

# Install with Helm
helm install triage-bot chart/triage-bot \
  -f deploy/shared-values.yaml \
  -f deploy/myteam/values.yaml \
  -n my-triage-bot
```

See [docs/ocp-deployment.md](docs/ocp-deployment.md) for the full
deployment guide including webhook setup, proxy configuration, and
troubleshooting.

## Configuration

Configuration via YAML file and/or `TRIAGE_BOT_*` environment
variables. See [chart/triage-bot/values.yaml](chart/triage-bot/values.yaml)
for the complete reference.

Key settings:

| Setting | Description | Default |
| ------- | ----------- | ------- |
| `jira.project_keys` | Jira projects to monitor | (required) |
| `jira.interval_seconds` | Polling interval | 300 |
| `ai.model` | AI model for triage | claude-sonnet-4-6 |
| `ai.max_concurrent` | Parallel triage workers | 3 |
| `triage.auto_fix_threshold` | Minimum AUTO_FIX likelihood to label | 80 |
| `triage.import.repo` | Git repo with workflow files | (optional) |
| `dry_run` | Log instead of writing to Jira | false |

## Multi-Team Deployment

Use layered Helm values — shared infrastructure defaults plus
per-consumer overrides:

```bash
helm install triage-bot-alpha chart/triage-bot \
  -f deploy/shared-values.yaml \
  -f deploy/alpha/values.yaml \
  -n alpha-triage-bot
```

Per-consumer files only need to set what varies (Jira username,
project keys, Vertex project ID).

## Related Projects

- [jira-ai-issue-solver](https://github.com/flightctl/jira-ai-issue-solver) —
  automated bug fixing bot (triage-bot labels candidates for it)
- [ai-workflows](https://github.com/flightctl/ai-workflows) —
  AI workflow skills including the triage `/assess` workflow

## Development

```bash
# Run tests
go test -v -race ./...

# Lint
make fmt
make lint
make docs-lint

# Build locally
go build .
./triage-bot -config config.yaml
```
