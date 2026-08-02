GOLANGCI_LINT_VERSION ?= v2.6.2
GOVULNCHECK_VERSION ?= v1.6.0
GITLEAKS_VERSION ?= v8.30.1
TRIVY_IMAGE ?= aquasec/trivy:0.72.0@sha256:cffe3f5161a47a6823fbd23d985795b3ed72a4c806da4c4df16266c02accdd6f

export GOVULNCHECK_VERSION
export GITLEAKS_VERSION

.PHONY: fmt fmt-check tidy-check test race vet lint vuln secret-scan filesystem-secret-scan npm-audit contracts openapi asyncapi license-review-check license-publication-gate production-readiness production-preflight production-preflight-check docker-build image-scan compose-config compose-smoke vpn-smoke stage7-smoke observability-validate observability-smoke backup-restore-drill backup-cleanup-test node-hardening-test secret-rotation-drill resilience-drill release-bundle compose-up compose-down diff-check verify

fmt:
	go fmt ./...

fmt-check:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-gofmt.ps1
else
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './node_modules/*'))"
endif

tidy-check:
	go mod tidy -diff

test:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test.ps1
else
	go test ./...
endif

race:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test.ps1 -Race
else
	CGO_ENABLED=1 go test -race ./...
endif

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

vuln:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/govulncheck.ps1
else
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
endif

secret-scan:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gitleaks.ps1
else
	@if git rev-parse --verify HEAD >/dev/null 2>&1; then \
		go run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) detect --source . --redact --no-banner --no-color --log-level warn; \
	else \
		go run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) detect --source . --no-git --redact --no-banner --no-color --log-level warn; \
	fi
endif

filesystem-secret-scan:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/filesystem-secret-scan.ps1
else
	bash scripts/filesystem-secret-scan.sh
endif

npm-audit:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/npm-audit.ps1
else
	@for attempt in 1 2 3; do \
		npm audit --audit-level=high && exit 0; \
		echo "npm audit attempt $$attempt failed; retrying"; \
		sleep 5; \
	done; \
	npm audit --audit-level=high
endif

openapi:
	npm run lint:openapi

asyncapi:
	npm run lint:asyncapi

contracts: openapi asyncapi
	npm run lint:events

license-review-check:
	node --test scripts/validate-license-policy.test.mjs
	node scripts/validate-license-policy.mjs

license-publication-gate:
ifndef RELEASE_OUTPUT_DIR
	$(error RELEASE_OUTPUT_DIR is required)
endif
	node scripts/validate-license-policy.mjs --publication --output="$(RELEASE_OUTPUT_DIR)"

production-readiness: license-review-check
	node --test scripts/validate-production-readiness.test.mjs
	node scripts/validate-production-readiness.mjs

production-preflight:
ifndef ENVIRONMENT_CONFIG
	$(error ENVIRONMENT_CONFIG is required)
endif
	go run -mod=readonly ./tools/productionpreflight --config "$(ENVIRONMENT_CONFIG)" --environment "$(or $(DEPLOY_ENVIRONMENT),production)" --source-commit "$(or $(SOURCE_COMMIT),$(shell git rev-parse HEAD))"

production-preflight-check:
	go test ./tools/productionpreflight ./internal/platform/config ./internal/platform/kafka ./internal/platform/redis

docker-build:
	go run -mod=readonly ./tools/releasectl local-build --inventory deploy/release/images.json

image-scan:
	go run -mod=readonly ./tools/releasectl local-scan --inventory deploy/release/images.json

compose-config:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/compose-config.ps1
else
	bash scripts/dev-mtls.sh
	bash scripts/dev-xray.sh
	POSTGRES_USER=vpn_local POSTGRES_PASSWORD=local-compose-password POSTGRES_DB=vpn_platform REDIS_PASSWORD=local-compose-redis KAFKA_PORT=9094 IDENTITY_DB_PASSWORD=local-compose-identity CATALOG_DB_PASSWORD=local-compose-catalog BILLING_DB_PASSWORD=local-compose-billing SUBSCRIPTION_DB_PASSWORD=local-compose-subscription ACCESS_DB_PASSWORD=local-compose-access PROVISIONING_DB_PASSWORD=local-compose-provisioning NOTIFICATION_DB_PASSWORD=local-compose-notification ADMIN_DB_PASSWORD=local-compose-admin ADMIN_MIGRATOR_DB_PASSWORD=local-compose-admin-migrator ACCESS_CREDENTIAL_KEY_BASE64=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= ACCESS_TOKEN_HMAC_KEY_BASE64=ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA= SUBSCRIPTION_PUBLIC_BASE_URL=https://127.0.0.1:8087 TELEGRAM_WEBHOOK_SECRET=local-compose-webhook-secret TELEGRAM_BOT_TOKEN=local-compose-fake-bot-token FAKE_TELEGRAM_SEND_DELAY=250ms TERMS_URL=https://example.invalid/terms/terms-v1 YOOKASSA_SHOP_ID=test-shop YOOKASSA_SECRET_KEY=local-compose-yookassa-key PAYMENT_RETURN_URL=https://example.invalid/payment-return docker compose --profile core --profile app --profile vpn --profile obs --profile maintenance config --quiet
endif

compose-smoke:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/compose-smoke.ps1
else
	bash scripts/compose-smoke.sh
endif

vpn-smoke:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/vpn-smoke.ps1
else
	bash scripts/vpn-smoke.sh
endif

stage7-smoke:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/stage7-smoke.ps1
else
	bash scripts/stage7-smoke.sh
endif

observability-validate:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/observability-validate.ps1
else
	bash scripts/observability-validate.sh
endif

observability-smoke:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/observability-smoke.ps1
else
	bash scripts/observability-smoke.sh
endif

backup-restore-drill:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-restore-drill.ps1
else
	bash scripts/backup-restore-drill.sh
endif

backup-cleanup-test:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-cleanup-test.ps1
else
	bash scripts/backup-cleanup-test.sh
endif

node-hardening-test:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/node-hardening-test.ps1
else
	bash scripts/node-hardening-test.sh
endif

secret-rotation-drill:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/secret-rotation-drill.ps1
else
	bash scripts/secret-rotation-drill.sh
endif

resilience-drill:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/resilience-drill.ps1
else
	bash scripts/resilience-drill.sh
endif

release-bundle:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release-bundle.ps1
else
	bash scripts/release-bundle.sh
endif

compose-up:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-mtls.ps1
else
	bash scripts/dev-mtls.sh
endif
	docker compose --profile core --profile app up --build

compose-down:
	docker compose --profile core --profile app down

diff-check:
	git diff --exit-code

verify: fmt-check tidy-check vet test race lint vuln secret-scan filesystem-secret-scan npm-audit contracts production-readiness production-preflight-check observability-validate docker-build image-scan compose-config diff-check
