# External Sources Checked

Checked on 2026-07-16.

These sources are used only to shape Stage 0 decisions. Implementation stages must re-check the relevant official documentation before coding an integration.

| Area | Official source | Stage 0 finding |
|---|---|---|
| Codex instructions | https://developers.openai.com/codex/guides/agents-md | Codex reads repository `AGENTS.md` as project guidance, so this repository keeps rules compact and points to detailed docs. |
| Telegram Bot API | https://core.telegram.org/bots/api | Stage 2 re-checked the official Bot API for webhook `secret_token`, the `X-Telegram-Bot-Api-Secret-Token` header, `Update.update_id`, `Message`, and `sendMessage`. |
| YooKassa payment process | https://yookassa.ru/developers/payment-acceptance/getting-started/payment-process | Payment creation uses authentication, `Idempotence-Key`, amount, confirmation data, and status transitions such as `pending`, `succeeded`, and `canceled`. |
| YooKassa webhooks | https://yookassa.ru/developers/using-api/webhooks | Webhooks report object status changes such as `payment.succeeded`, `payment.canceled`, and `refund.succeeded`; receipt must be acknowledged. |
| YooKassa response handling | https://yookassa.ru/developers/using-api/response-handling/http-codes | Ambiguous provider errors require retry with the same idempotency key or GET verification rather than assuming success or failure. |
| Happ developer docs | https://www.happ.su/main/dev-docs | Happ supports subscription delivery through a URL returning proxy links and parameters. |
| Happ app management | https://www.happ.su/main/dev-docs/app-management | Happ accepts subscription parameters via headers or body, including `profile-title`, `subscription-userinfo`, `support-url`, and fallback behavior. A fallback URL may be used if the primary URL returns 300-599 or times out. |
| Happ link examples | https://www.happ.su/main/dev-docs/examples-of-links-and-parameters | Subscription body compatibility must be verified with official examples during Stage 5. |
| Xray-core | https://github.com/XTLS/Xray-core | The official Xray-core project is the source for pinned release selection, config validation, and security review. |
| Xray examples | https://github.com/XTLS/Xray-examples | VLESS + REALITY examples are reference material for Stage 5/6 golden tests, not a substitute for implementation validation. |
| Go releases/downloads | https://go.dev/dl/ | Stage 1 pins stable Go 1.26.5 after `govulncheck` found standard-library vulnerabilities in 1.26.2. Release candidates are not used. |
| Go vulnerability database | https://go.dev/security/vuln/database | Stage 1 `make vuln` uses the official Go vulnerability database through `govulncheck`; the Windows fallback mirrors only the needed official JSON endpoints into a temporary local database. |
| govulncheck versions | `go list -m -versions golang.org/x/vuln` | Stage 1 pins `golang.org/x/vuln/cmd/govulncheck` v1.6.0. |
| Gitleaks versions | `go list -m -versions github.com/gitleaks/gitleaks/v8` and module redirect to `github.com/zricethezav/gitleaks/v8` | Stage 1 pins Gitleaks v8.30.1 for local and CI secret scanning. |
| Trivy image | `docker run aquasec/trivy:latest --version` | Stage 1 verified the current Trivy image reports version 0.72.0, and `make image-scan` pins `aquasec/trivy:0.72.0`. |
| GitHub Actions refs | `git ls-remote` for `actions/checkout`, `actions/setup-go`, and `actions/setup-node` | Stage 1 pins the v5/v6/v6 action refs by full commit SHA in CI. |
| Local container image digests | `docker buildx imagetools inspect` | Stage 1 pins `postgres:18-alpine`, `redis:8-alpine`, and `apache/kafka:4.3.1` by manifest digest in Compose. |
| Apache Kafka releases | https://kafka.apache.org/downloads | Stage 1 local Compose uses Apache Kafka in KRaft mode; image version must be revisited before production. |
| Go module registry | `go list -m -versions` | Stage 1 pinned current module versions for zap, pgx/v5, franz-go, go-redis/v9, Prometheus client, and goose. |
