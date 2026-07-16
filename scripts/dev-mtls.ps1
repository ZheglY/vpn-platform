$ErrorActionPreference = "Stop"

$outDir = Join-Path (Resolve-Path ".").Path "secrets/dev-mtls"
New-Item -ItemType Directory -Path $outDir -Force | Out-Null

$openssl = Get-Command openssl -ErrorAction SilentlyContinue
if (-not $openssl) {
    throw "openssl is required to generate local development mTLS certificates"
}

$caKey = Join-Path $outDir "ca.key"
$caCert = Join-Path $outDir "ca.crt"
$serverKey = Join-Path $outDir "identity-service.key"
$serverCSR = Join-Path $outDir "identity-service.csr"
$serverCert = Join-Path $outDir "identity-service.crt"
$clientKey = Join-Path $outDir "telegram-bot.key"
$clientCSR = Join-Path $outDir "telegram-bot.csr"
$clientCert = Join-Path $outDir "telegram-bot.crt"
$serverExt = Join-Path $outDir "identity-service.ext"
$clientExt = Join-Path $outDir "telegram-bot.ext"

@"
[req]
distinguished_name=req
[v3_req]
subjectAltName=DNS:identity-service.local,DNS:localhost,IP:127.0.0.1
extendedKeyUsage=serverAuth
"@ | Set-Content -Encoding ASCII $serverExt

@"
[req]
distinguished_name=req
[v3_req]
subjectAltName=URI:spiffe://vpn-service/ns/local/sa/telegram-bot
extendedKeyUsage=clientAuth
"@ | Set-Content -Encoding ASCII $clientExt

& openssl req -x509 -newkey rsa:3072 -sha256 -days 30 -nodes -keyout $caKey -out $caCert -subj "/CN=vpn-service-dev-ca"
& openssl req -newkey rsa:3072 -nodes -keyout $serverKey -out $serverCSR -subj "/CN=identity-service.local"
& openssl x509 -req -in $serverCSR -CA $caCert -CAkey $caKey -CAcreateserial -out $serverCert -days 30 -sha256 -extfile $serverExt -extensions v3_req
& openssl req -newkey rsa:3072 -nodes -keyout $clientKey -out $clientCSR -subj "/CN=ignored"
& openssl x509 -req -in $clientCSR -CA $caCert -CAkey $caKey -CAcreateserial -out $clientCert -days 30 -sha256 -extfile $clientExt -extensions v3_req

Remove-Item $serverCSR, $clientCSR, $serverExt, $clientExt -Force
Write-Output "Generated local development mTLS material under $outDir"
