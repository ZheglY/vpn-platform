GOLANGCI_LINT_VERSION ?= v2.6.2
GOVULNCHECK_VERSION ?= v1.6.0
GITLEAKS_VERSION ?= v8.30.1
TRIVY_IMAGE ?= aquasec/trivy:0.72.0@sha256:cffe3f5161a47a6823fbd23d985795b3ed72a4c806da4c4df16266c02accdd6f

export GOVULNCHECK_VERSION
export GITLEAKS_VERSION

.PHONY: fmt fmt-check tidy-check test race vet lint vuln secret-scan npm-audit contracts openapi asyncapi docker-build image-scan compose-config compose-smoke vpn-smoke compose-up compose-down diff-check verify

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

docker-build:
	docker build -f services/identity/Dockerfile -t vpn-service/identity-service:local .
	docker build -f services/catalog/Dockerfile -t vpn-service/catalog-service:local .
	docker build -f services/billing/Dockerfile -t vpn-service/billing-service:local .
	docker build -f services/subscription/Dockerfile -t vpn-service/subscription-service:local .
	docker build -f services/access/Dockerfile -t vpn-service/access-service:local .
	docker build -f services/provisioning/Dockerfile -t vpn-service/provisioning-service:local .
	docker build -f services/node-agent/Dockerfile -t vpn-service/node-agent:local .
	docker build -f services/telegram-bot/Dockerfile -t vpn-service/telegram-bot:local .

image-scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/identity-service:local
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/catalog-service:local
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/billing-service:local
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/subscription-service:local
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/access-service:local
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/provisioning-service:local
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/node-agent:local
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v vpn-service-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress vpn-service/telegram-bot:local

compose-config:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/compose-config.ps1
else
	bash scripts/dev-mtls.sh
	bash scripts/dev-xray.sh
	POSTGRES_USER=vpn_local POSTGRES_PASSWORD=local-compose-password POSTGRES_DB=vpn_platform REDIS_PASSWORD=local-compose-redis KAFKA_PORT=9094 IDENTITY_DB_PASSWORD=local-compose-identity CATALOG_DB_PASSWORD=local-compose-catalog BILLING_DB_PASSWORD=local-compose-billing SUBSCRIPTION_DB_PASSWORD=local-compose-subscription ACCESS_DB_PASSWORD=local-compose-access PROVISIONING_DB_PASSWORD=local-compose-provisioning ACCESS_CREDENTIAL_KEY_BASE64=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= ACCESS_TOKEN_HMAC_KEY_BASE64=ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA= SUBSCRIPTION_PUBLIC_BASE_URL=https://127.0.0.1:8087 TELEGRAM_WEBHOOK_SECRET=local-compose-webhook-secret TELEGRAM_BOT_TOKEN=local-compose-fake-bot-token FAKE_TELEGRAM_SEND_DELAY=250ms TERMS_URL=https://example.invalid/terms/terms-v1 YOOKASSA_SHOP_ID=test-shop YOOKASSA_SECRET_KEY=local-compose-yookassa-key PAYMENT_RETURN_URL=https://example.invalid/payment-return docker compose --profile core --profile app --profile vpn config --quiet
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

verify: fmt-check tidy-check vet test race lint vuln secret-scan npm-audit contracts docker-build image-scan compose-config diff-check
