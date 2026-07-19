$ErrorActionPreference = "Stop"

function Set-DefaultEnv([string]$name, [string]$value) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

Set-DefaultEnv "POSTGRES_USER" "vpn_local"
Set-DefaultEnv "POSTGRES_PASSWORD" "local-compose-password"
Set-DefaultEnv "POSTGRES_DB" "vpn_platform"
Set-DefaultEnv "REDIS_PASSWORD" "local-compose-redis"
Set-DefaultEnv "KAFKA_PORT" "9094"
Set-DefaultEnv "IDENTITY_DB_PASSWORD" "local-compose-identity"
Set-DefaultEnv "CATALOG_DB_PASSWORD" "local-compose-catalog"
Set-DefaultEnv "BILLING_DB_PASSWORD" "local-compose-billing"
Set-DefaultEnv "SUBSCRIPTION_DB_PASSWORD" "local-compose-subscription"
Set-DefaultEnv "ACCESS_DB_PASSWORD" "local-compose-access"
Set-DefaultEnv "PROVISIONING_DB_PASSWORD" "local-compose-provisioning"
Set-DefaultEnv "ACCESS_CREDENTIAL_KEY_BASE64" "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
Set-DefaultEnv "ACCESS_TOKEN_HMAC_KEY_BASE64" "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
Set-DefaultEnv "SUBSCRIPTION_PUBLIC_BASE_URL" "https://127.0.0.1:8087"
Set-DefaultEnv "TELEGRAM_WEBHOOK_SECRET" "local-compose-webhook-secret"
Set-DefaultEnv "TELEGRAM_BOT_TOKEN" "local-compose-fake-bot-token"
Set-DefaultEnv "FAKE_TELEGRAM_SEND_DELAY" "250ms"
Set-DefaultEnv "TERMS_URL" "https://example.invalid/terms/terms-v1"
Set-DefaultEnv "YOOKASSA_SHOP_ID" "test-shop"
Set-DefaultEnv "YOOKASSA_SECRET_KEY" "local-compose-yookassa-key"
Set-DefaultEnv "PAYMENT_RETURN_URL" "https://example.invalid/payment-return"

& powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-mtls.ps1
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
& powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-xray.ps1
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

docker compose --profile core --profile app --profile vpn config --quiet
exit $LASTEXITCODE
