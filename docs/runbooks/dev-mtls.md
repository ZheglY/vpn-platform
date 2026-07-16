# Development mTLS Runbook

Internal service endpoints must authenticate callers with verified client certificates and strict SPIFFE IDs:

```text
spiffe://vpn-service/ns/{environment}/sa/{service}
```

For local development, generate short-lived certificates into the ignored `secrets/dev-mtls` directory:

```powershell
scripts/dev-mtls.ps1
```

Generated files are local secrets and must not be committed. Compose does not mount them in Stage 1 because the template service has no internal mTLS endpoints yet. Stage 2 internal endpoints must mount these files or an equivalent local CA and configure server TLS through `httpserver.NewMutualTLSConfig`.

The only accepted service identity source is `tls.ConnectionState.VerifiedChains[0][0].URIs`. `PeerCertificates`, DNS SAN, and Common Name are not trusted for authorization.
