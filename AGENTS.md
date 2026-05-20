# AGENTS.md

This file provides guidance to AIs when working with code in this repository.

## Project Overview

Triage-bot is a Go service that monitors Jira projects for unresolved bugs, runs AI-powered triage assessments via Claude Code CLI, and posts the results as Jira comments. It is **workflow-agnostic** — the actual triage analysis is delegated to a configurable workflow (defaulting to the ai-workflows triage `/assess` skill). Teams plug in their own workflow by providing a different repo, task template, and MCP config.

## Architecture

```
┌──────────────────────────────────────────┐
│ Container (single OCP pod)               │
│                                          │
│  ┌──────────────┐    ┌────────────────┐  │
│  │  Go Bot      │───>│  Claude CLI    │  │
│  │              │    │                │  │
│  │  Scanner     │    │  MCP → Jira    │  │
│  │  Processor   │    │  (read-only)   │  │
│  │  Jira Client │    │                │  │
│  └──────┬───────┘    └────────────────┘  │
│         │                                │
└─────────┼────────────────────────────────┘
          ▼
     ┌─────────┐
     │  Jira   │
     └─────────┘
```

The **Go bot** handles the control plane (poll for bugs, check comment state, post/update comments, apply labels). **Claude CLI** handles the analysis (triage assessment via MCP-based Jira reads).

### Package Structure

| Package      | Role                                                                                     |
|--------------|------------------------------------------------------------------------------------------|
| `config/`    | Viper-based configuration loading with env var override (`TRIAGE_BOT_` prefix)           |
| `jira/`      | Jira REST API client (search, comments, labels) and ADF models; adapted from jira-ai-issue-solver |
| `triage/`    | Core logic — processor (comment state machine), executor (CLI invocation), metadata parser |
| `scanner/`   | Polling-based Jira scanner with ticker loop and semaphore-bounded worker pool            |
| `workflow/`  | Git-based workflow importer (full clone or sparse checkout at startup)                    |
| `server/`    | HTTP server: `/health` endpoint + optional `/webhook` handler (HMAC-SHA256 verified)     |

### Key Design Decisions

- **Stateless**: Jira is the sole source of truth. The bot identifies its own prior comments by matching author + a description hash marker in the comment footer. No database, no local state file.
- **Description hash**: SHA-256 of issue description text, first 12 hex chars, embedded as `_triage-bot | desc:<hash>_` in the comment footer. Re-triage only fires when the hash changes — not on any issue update.
- **Metadata sidecar**: The AI writes a JSON file (`{KEY}.meta.json`) alongside the markdown assessment. The bot uses this for structured decisions (AUTO_FIX labeling) without parsing markdown. Missing sidecar degrades gracefully — comment still posts, label logic skipped.
- **Workflow-agnostic**: Triage analysis is driven by a Go template (`task.tmpl`) rendered per issue with variables like `{{.IssueKey}}`, `{{.OutputPath}}`, `{{.WorkflowPath}}`. The workflow repo is imported at startup via configurable git clone.
- **Webhook + polling coexistence**: Both can run simultaneously. The description-hash check prevents double-processing. Webhooks give near-real-time response; polling is the reliability backstop.
- **Webhook security**: The `/webhook` endpoint and OCP Route are only created when `server.webhook_secret` is configured. No secret = nothing exposed. All requests verified via HMAC-SHA256.

### Workflow

1. **Startup**: Load config → write `~/.claude/settings.json` (MCP config) → import workflow repo → start scanner and HTTP server
2. **Polling** (`scanner/scanner.go`): JQL query at configured interval, fan out to worker pool bounded by `ai.max_concurrent`
3. **Per-issue processing** (`triage/processor.go`):
   - Fetch comments → find bot's comment by author + `desc:` marker
   - Compare stored hash with current description hash → `ActionSkip` / `ActionCreate` / `ActionUpdate`
   - Render task template → write `task.md` → invoke Claude CLI
   - Read assessment `.md` + metadata `.meta.json`
   - Append hash footer → post/update Jira comment
   - If metadata indicates AUTO_FIX ≥ threshold → add label
4. **Webhooks** (`server/webhook.go`): Jira POSTs on issue create/update → HMAC verified → fed to same processor asynchronously
5. **Dry run**: When `dry_run: true`, the bot runs the full flow (including AI invocation) but logs the comment instead of writing to Jira

### MCP Configuration

`setupMCPConfig()` in `main.go` writes `~/.claude/settings.json` at startup with the Jira MCP server. Common env vars (`ATLASSIAN_SITE_NAME`, `ATLASSIAN_USER_EMAIL`, `ATLASSIAN_API_TOKEN`) are auto-populated from the bot's Jira config. The default MCP server is `@aashari/mcp-server-atlassian-jira` (pre-installed in the container image).

### OCP Deployment Model

Multi-consumer via layered Helm values:
- `deploy/shared-values.yaml` — infrastructure shared across all consumers (image, resources, Jira site, workflow repo, MCP config)
- `deploy/<consumer>/values.yaml` — consumer-specific (Jira username, project keys, Vertex project ID, dry_run)

See `docs/ocp-deployment.md` for full setup including secrets and webhooks.

## Common Development Commands

### Build and Test

```bash
# Build binary
go build .

# Run all tests with race detection
go test -v -race ./...

# Run tests for a specific package
go test -v ./triage/

# Run a specific test
go test -v ./triage/ -run TestComputeHash
```

### Linting

```bash
# Go code
make fmt
make lint

# Markdown (after any .md changes)
make docs-lint
```

AGENTS.md and CLAUDE.md are excluded from markdown linting.

### Container and Deployment

```bash
# Build and push container image
make build
make push REGISTRY=quay.io/your-org

# Lint Helm chart
make helm-lint

# Deploy / upgrade on OCP
helm install triage-bot-myteam chart/triage-bot \
  -f deploy/shared-values.yaml \
  -f deploy/myteam/values.yaml \
  -n my-namespace

helm upgrade triage-bot-myteam chart/triage-bot \
  -f deploy/shared-values.yaml \
  -f deploy/myteam/values.yaml \
  -n my-namespace
```

## Testing Guidelines

### Requirements

Every code change must include corresponding unit tests. Code and tests are always committed together — never defer test writing to a later step. Run `go test -v -race ./...` after every change and fix failures before continuing.

### What to Test

- **New functions/methods**: Cover the happy path, error cases, and edge cases
- **Changed behavior**: Update existing tests to match, add new tests for new code paths
- **Bug fixes**: Write a test that reproduces the bug first, verify it fails, then fix and verify it passes
- **Interface changes**: Update all callers, mocks, and test code to match the new signature

### Patterns in This Codebase

**Table-driven tests** — preferred for functions with multiple input/output combinations:
```go
tests := []struct {
    name string
    // inputs + expected outputs
}{...}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) { ... })
}
```

**Interface-based mocking** — the `triage` package defines a `JiraClient` interface with only the methods it needs. Tests can provide a minimal stub:
```go
type JiraClient interface {
    GetComments(key string) ([]jira.JiraComment, error)
    AddComment(key, comment string) error
    UpdateComment(key, commentID, body string) error
    AddLabel(key, label string) error
}
```

**Test helpers** — use `t.TempDir()` for file-based tests, `t.Fatal()` for setup failures that shouldn't continue.

**Testing the Jira client** — use `NewClientForTest()` which accepts a custom `*http.Client` and sleep function for deterministic retry testing.

### What NOT to Test

- Don't test the AI CLI invocation end-to-end in unit tests (it requires Claude Code installed). The executor's `execFn` field allows stubbing the command.
- Don't test Helm templates in Go. Use `make helm-lint` and manual review.

## Configuration Reference

Environment variables follow `TRIAGE_BOT_<SECTION>_<FIELD>`:

| Env Var | Config Path |
|---------|-------------|
| `TRIAGE_BOT_JIRA_BASE_URL` | `jira.base_url` |
| `TRIAGE_BOT_JIRA_SITE_NAME` | `jira.site_name` |
| `TRIAGE_BOT_JIRA_API_TOKEN` | `jira.api_token` |
| `TRIAGE_BOT_AI_CLAUDE_VERTEX_PROJECT_ID` | `ai.claude.vertex_project_id` |
| `TRIAGE_BOT_AI_CLAUDE_VERTEX_REGION` | `ai.claude.vertex_region` |
| `TRIAGE_BOT_SERVER_WEBHOOK_SECRET` | `server.webhook_secret` |
| `TRIAGE_BOT_DRY_RUN` | `dry_run` |

See `chart/triage-bot/values.yaml` for the complete configuration reference with comments.

### Vertex AI Authentication

When using Claude via Vertex AI, the executor sets these env vars for the CLI process:
- `CLAUDE_CODE_USE_VERTEX=1`
- `CLOUD_ML_PROJECT_ID`
- `CLOUD_ML_REGION`
- `GOOGLE_APPLICATION_CREDENTIALS` (path to GCP SA key, mounted from Secret)

These are also set as pod-level env vars by the Helm Deployment template.

## Implementation Details

### Description Hash Functions

All in `triage/processor.go`:
- `computeHash(s string) string` — SHA-256, first 12 hex chars
- `appendHashFooter(body, hash string) string` — appends `\n\n---\n_triage-bot | desc:<hash>_\n`
- `extractHash(body string) string` — finds last `triage-bot | desc:` marker, extracts hash

### Comment Identification

`findBotComment()` scans comments in reverse order (newest first), matching on:
1. Author email, display name, or username matches `jira.bot_username` (defaults to `jira.username`)
2. Comment body contains the `triage-bot | desc:` hash marker

Both conditions must match. This prevents false positives from other bots or manual comments that happen to contain the marker text.

### Task Template Variables

| Variable            | Description                                          |
|---------------------|------------------------------------------------------|
| `{{.IssueKey}}`     | Jira issue key (e.g., `PROJ-123`)                   |
| `{{.IssueURL}}`     | Full Jira URL to the issue                          |
| `{{.OutputPath}}`   | Path where assessment markdown must be written       |
| `{{.MetadataPath}}` | Path where JSON metadata sidecar must be written     |
| `{{.WorkflowPath}}` | Path to the imported workflow files                  |
| `{{.ProjectKey}}`   | Jira project key (e.g., `PROJ`)                     |

Override via `triage.task_template` (inline) or `triage.task_template_path` (file path).

### Metadata Sidecar Contract

The AI is prompted to write `{KEY}.meta.json`:
```json
{
  "recommendation": "AUTO_FIX",
  "confidence": "High",
  "autoFixLikelihood": 85
}
```

- `recommendation`: one of CLOSE, FIX_NOW, AUTO_FIX, BACKLOG, NEEDS_INFO, DUPLICATE, ESCALATE, WONT_FIX
- `autoFixLikelihood`: 0–100 when recommendation is AUTO_FIX, null otherwise
- If the file is missing or unparseable, the bot logs a warning and skips label logic

### Jira Client

Adapted from jira-ai-issue-solver (`services/jira.go`). Key differences from the original:
- **Kept**: HTTP client, retry with exponential backoff + jitter, rate limit handling (429 + Retry-After), SearchTickets, GetComments, AddComment, UpdateComment
- **Added**: `AddLabel()` for AUTO_FIX labeling
- **Dropped**: status transitions, field ID cache, attachment downloads, DeleteComment, security level handling
- **ADF handling**: `TextToADF()` converts plain text to Atlassian Document Format for API v3 writes; `ADFText` type transparently unmarshals ADF responses to plain text

### Webhook Handler

- HMAC-SHA256 verification via `X-Hub-Signature` header (format: `sha256=<hex>`)
- Async processing: responds 200 immediately, processes in a goroutine
- Semaphore-bounded: shares `ai.max_concurrent` limit with the polling scanner
- When semaphore is full, logs a warning and drops the event (Jira still gets 200 to avoid retries)

## File Structure

```
├── main.go              # Entry point, component wiring, MCP setup, shutdown
├── config/
│   ├── config.go        # Config struct, Viper loading, validation
│   └── config_test.go
├── jira/
│   ├── client.go        # Jira REST client with retry/backoff
│   └── models.go        # JiraIssue, JiraComment, ADFText, TextToADF
├── triage/
│   ├── processor.go     # Per-issue state machine (skip/create/update)
│   ├── processor_test.go
│   ├── executor.go      # Task template rendering, CLI invocation
│   ├── metadata.go      # JSON sidecar parsing, AUTO_FIX threshold
│   └── metadata_test.go
├── scanner/
│   └── scanner.go       # Polling loop, JQL builder, worker pool
├── server/
│   ├── health.go        # HTTP server with /health and optional /webhook
│   ├── webhook.go       # Jira webhook handler with HMAC verification
│   └── webhook_test.go
├── workflow/
│   └── importer.go      # Git clone/sparse checkout at startup
├── task.tmpl            # Default task template for AI invocation
├── chart/triage-bot/values.yaml  # Complete configuration reference
├── Dockerfile           # Multi-stage build: Go + Claude CLI + MCP server
├── Makefile             # build, push, test, lint, docs-lint, helm-lint
├── chart/triage-bot/    # Helm chart (Deployment, ConfigMap, Secret, SA, Route)
├── deploy/              # Per-consumer Helm values (shared + overrides)
│   ├── shared-values.yaml
│   └── osac/values.yaml
└── docs/
    └── ocp-deployment.md
```

## Common Pitfalls

- **ADF format**: Jira Cloud API v3 requires Atlassian Document Format for writes. Always use `TextToADF()` — never post raw text strings to comment/description endpoints.
- **Hash extraction**: The `extractHash()` function looks for the _last_ occurrence of the marker. If the assessment text itself contains the marker string, the hash will still be correctly extracted from the footer.
- **Webhook without secret**: If `server.webhook_secret` is empty, the webhook handler is nil and the `/webhook` route is not registered. The OCP Route is also not created. This is intentional — never expose an unauthenticated webhook endpoint.
- **Polling + webhook overlap**: Safe by design. The processor's hash check is the deduplication mechanism. If both trigger for the same issue, the second one sees matching hashes and skips.
- **MCP env var auto-population**: `setupMCPConfig()` populates common env vars (ATLASSIAN_SITE_NAME, ATLASSIAN_USER_EMAIL, ATLASSIAN_API_TOKEN) from the bot's Jira config only when the MCP env map doesn't already set them. Explicit MCP env values always win.
