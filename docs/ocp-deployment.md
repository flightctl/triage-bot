# Deploying triage-bot on OpenShift

## Prerequisites

- OpenShift cluster with `oc` or `helm` CLI configured
- Container image built and pushed to a registry accessible from the cluster
- Jira API token for the bot's service account
- Vertex AI GCP service account key (if using Claude via Vertex AI)

## 1. Build and push the container image

```bash
make build
make push REGISTRY=quay.io/your-org
```

## 2. Create secrets

Create the Kubernetes Secrets in your target namespace:

```bash
oc create ns my-triage-bot

oc -n my-triage-bot create secret generic triage-bot-jira-token \
  --from-file=jira-api-token=/path/to/jira-token.txt

oc -n my-triage-bot create secret generic triage-bot-vertex \
  --from-file=gcp-sa-key.json=/path/to/gcp-sa-key.json
```

The key names (`jira-api-token` and `gcp-sa-key.json`) must match
what the Helm templates expect.

## 3. Configure values files

Use layered values files — shared defaults plus per-consumer overrides:

**`deploy/shared-values.yaml`** — common across all consumers
(image, resource limits, Jira site, workflow repo, MCP config).

**`deploy/<consumer>/values.yaml`** — consumer-specific settings
(Jira username, project keys, Vertex project ID).

See `deploy/shared-values.yaml` and `deploy/osac/values.yaml` for examples.

## 4. Install with Helm

```bash
helm install triage-bot-myteam chart/triage-bot \
  -f deploy/shared-values.yaml \
  -f deploy/myteam/values.yaml \
  -n my-triage-bot
```

To upgrade:

```bash
helm upgrade triage-bot-myteam chart/triage-bot \
  -f deploy/shared-values.yaml \
  -f deploy/myteam/values.yaml \
  -n my-triage-bot
```

## 5. Verify

```bash
# Check pod is running
oc get pods -n my-triage-bot

# Check logs
oc logs -f deploy/triage-bot-myteam -n my-triage-bot

# You should see:
#   "Starting triage-bot" with your projects
#   "Wrote Claude Code MCP settings"
#   "Scanning for bugs" with your JQL query
#   "Found issues" with the count
```

## 6. Webhooks (optional)

By default the bot polls Jira at a configured interval. For near-real-time
triage after description edits, you can enable Jira webhooks.

### Requirements

- A **webhook secret** — a shared HMAC secret between Jira and the bot.
  Without it, the webhook endpoint is not exposed (no Route created,
  no handler registered).
- The OCP cluster must be **reachable from Jira Cloud** (the cluster's
  wildcard domain must be internet-accessible).

### Setup

1. **Add the webhook secret** to your Kubernetes Secret:

   ```bash
   # Generate a random secret
   WEBHOOK_SECRET=$(openssl rand -hex 32)

   # Add it to the existing Jira secret (recreate with the extra key)
   oc -n my-triage-bot create secret generic triage-bot-jira-token \
     --from-file=jira-api-token=/path/to/jira-token.txt \
     --from-literal=webhook-secret="$WEBHOOK_SECRET" \
     --dry-run=client -o yaml | oc apply -f -
   ```

2. **Enable in Helm values** (either in shared or per-consumer):

   ```yaml
   secrets:
     webhookSecret: ""  # only needed if using secrets.create: true
     # If using pre-created secrets, just ensure the Secret has the
     # 'webhook-secret' key as shown above.
   ```

3. **Upgrade the Helm release** to create the Route:

   ```bash
   helm upgrade triage-bot-myteam chart/triage-bot \
     -f deploy/shared-values.yaml \
     -f deploy/myteam/values.yaml \
     -n my-triage-bot
   ```

4. **Get the webhook URL:**

   ```bash
   oc get route -n my-triage-bot -o jsonpath='{.items[0].spec.host}'
   ```

   The full URL is `https://<host>/webhook`.

5. **Configure in Jira:** Go to **Settings → System → Webhooks → Create**:
   - **URL:** the webhook URL from above
   - **Secret:** the same `$WEBHOOK_SECRET` value
   - **Events:** check "Issue created" and "Issue updated"
   - **JQL filter:** `project = MYPROJ AND issuetype = Bug`

### Security

- The webhook endpoint is **only exposed when a webhook secret is configured**.
  No secret = no Route, no handler, nothing listening.
- All incoming requests are verified via **HMAC-SHA256** signature
  (`X-Hub-Signature` header). Requests with missing or invalid signatures
  are rejected with 401.
- Concurrent webhook processing is bounded by `ai.max_concurrent`
  (same limit as the polling scanner).

### Polling + webhooks

Both can run simultaneously. The description-hash check in the processor
ensures an issue is never double-processed — if a webhook triggers triage
and the poll finds the same issue, the poll skips it (hash matches).

## Proxy configuration

If the cluster requires an HTTP proxy for external access (Jira, Vertex AI, npm):

```yaml
extraEnv:
  - name: HTTPS_PROXY
    value: "http://proxy.example.com:3128"
  - name: HTTP_PROXY
    value: "http://proxy.example.com:3128"
  - name: NO_PROXY
    value: "localhost,127.0.0.1,.cluster.local"
```

## Pod Security

The chart is designed for OCP's **restricted** Security Context Constraint:

- Runs as non-root (UID 1001, GID 0)
- Drops all Linux capabilities
- Disallows privilege escalation
- Uses RuntimeDefault seccomp profile

No special SCC or RBAC is required beyond the default restricted policy.

## Resource sizing

Default requests/limits are sized for moderate workloads (3 concurrent triage workers):

| Resource | Request | Limit |
|----------|---------|-------|
| CPU      | 500m    | 2     |
| Memory   | 512Mi   | 2Gi   |

Claude Code CLI invocations are CPU/memory-intensive. If you increase
`ai.max_concurrent` beyond 3, scale the limits proportionally.

## Troubleshooting

**Pod CrashLoopBackOff with config errors:**
Check `oc logs` — usually a missing required field (`jira.base_url`,
`jira.api_token`, `jira.project_keys`, or Claude auth).

**MCP server not connecting:**
Verify the MCP server package is accessible (npm registry reachable, or
pre-installed in the image). Check that `jira.site_name` matches your
Atlassian Cloud subdomain.

**Workflow import failing:**
If `triage.import.repo` is a private repo, ensure git credentials are
available in the container (SSH key or token in the URL).

**Triage comments not appearing:**
Check that `jira.username` matches the Jira user's email/display name.
The bot identifies its own comments by matching this against comment authors.

**Webhook not receiving events:**
Verify the Route exists (`oc get route`), the webhook secret matches
between Jira and the Secret, and the cluster is reachable from Jira Cloud.
Check logs for `"Webhook signature verification failed"` messages.
