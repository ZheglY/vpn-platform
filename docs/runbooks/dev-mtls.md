# Development mTLS Runbook

Internal service endpoints must authenticate callers with verified client certificates and strict SPIFFE IDs:

```text
spiffe://vpn-service/ns/{environment}/sa/{service}
```

For local development, generate short-lived certificates into the ignored `secrets/dev-mtls` directory:

```powershell
scripts/dev-mtls.ps1
```

On Linux/macOS:

```bash
bash scripts/dev-mtls.sh
```

The scripts call the Go generator at `tools/devmtls/cmd/devmtls`, so OpenSSL is not required.

Generated files are local secrets and must not be committed. The generator sets `secrets/dev-mtls` to `0700`, certificates to `0644`, and private keys to `0600`; rerunning it also tightens permissions on existing files. Compose exposes that host directory only to the network-isolated credential init container. The init container copies the allowlisted runtime subset into per-owner named volumes with UID/GID `65532:65532`, `0400` private keys, `0440` public certificates, and `0500` directories. Services mount only their own staged volume read-only. Identity-service configures server TLS through `httpserver.NewMutualTLSConfig`; telegram-bot uses its staged client certificate for identity-service calls.

The only accepted service identity source is `tls.ConnectionState.VerifiedChains[0][0].URIs`. `PeerCertificates`, DNS SAN, and Common Name are not trusted for authorization.
