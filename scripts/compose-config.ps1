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

docker compose --profile core --profile app config --quiet
exit $LASTEXITCODE
