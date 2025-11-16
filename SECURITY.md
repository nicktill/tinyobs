# Security Policy

## Supported Versions

We release patches for security vulnerabilities. Which versions are currently being supported with security updates:

| Version | Supported          |
| ------- | ------------------ |
| 2.1.x   | :white_check_mark: |
| 2.0.x   | :white_check_mark: |
| < 2.0   | :x:                |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, please report them via email to: [security@tinyobs.dev] (update with actual email)

You should receive a response within 48 hours. If for some reason you do not, please follow up via email to ensure we received your original message.

Please include the following information:

- Type of vulnerability
- Full paths of source file(s) related to the vulnerability
- Location of the affected source code (tag/branch/commit or direct URL)
- Any special configuration required to reproduce the issue
- Step-by-step instructions to reproduce the issue
- Proof-of-concept or exploit code (if possible)
- Impact of the issue, including how an attacker might exploit it

This information will help us triage your report more quickly.

## Security Disclosure Process

1. **Receipt**: We will acknowledge receipt of your vulnerability report within 48 hours
2. **Assessment**: We will confirm the problem and determine affected versions (within 7 days)
3. **Fix**: We will prepare and test a fix (timeline depends on severity)
4. **Release**: We will release a security patch and publish a security advisory
5. **Credit**: We will credit you in the advisory (if you wish)

## Known Security Considerations

### Current Security Features

✅ **Cardinality Protection**
- Limits on unique time series (100,000 max)
- Per-metric series limits (10,000 max)
- Label size limits (256 chars for keys, 1KB for values)

✅ **Input Validation**
- Metric name validation
- Label validation
- Request size limits (1000 metrics per request)

✅ **Resource Protection**
- Graceful shutdown
- Context timeouts
- Connection limits

### Known Limitations (Being Addressed)

⚠️ **Authentication**
- Currently: No authentication on API endpoints
- Planned: API key authentication in V2.2
- Status: High priority

⚠️ **Rate Limiting**
- Currently: No rate limiting
- Planned: Per-IP rate limiting in V2.2
- Status: High priority

⚠️ **HTTPS**
- Currently: HTTP only
- Planned: TLS support in V2.2
- Status: High priority

⚠️ **CORS**
- Currently: No CORS configuration
- Planned: Configurable CORS in V2.2
- Status: Medium priority

## Security Best Practices for Deployment

### Network Security

1. **Use a Reverse Proxy**
   ```nginx
   # Example: Nginx with TLS termination
   server {
       listen 443 ssl;
       server_name tinyobs.example.com;
       
       ssl_certificate /path/to/cert.pem;
       ssl_certificate_key /path/to/key.pem;
       
       location / {
           proxy_pass http://localhost:8080;
           proxy_set_header Host $host;
           proxy_set_header X-Real-IP $remote_addr;
       }
   }
   ```

2. **Firewall Configuration**
   - Only expose port 8080 to trusted networks
   - Use security groups in cloud environments
   - Consider VPN for remote access

3. **Network Segmentation**
   - Run TinyObs in isolated network
   - Limit access to storage layer
   - Use internal DNS

### Access Control

1. **API Key Management** (When available)
   ```bash
   # Generate strong API keys
   openssl rand -base64 32
   
   # Rotate keys regularly
   # Store keys in secrets manager
   ```

2. **Principle of Least Privilege**
   - Run TinyObs as non-root user
   - Limit file system permissions
   - Use read-only volumes where possible

3. **Monitoring**
   - Monitor failed authentication attempts
   - Track unusual query patterns
   - Alert on anomalous behavior

### Data Security

1. **Data at Rest**
   - BadgerDB files are not encrypted by default
   - Consider full-disk encryption
   - Set appropriate file permissions (0600)

2. **Data in Transit**
   - Use HTTPS for all API communication
   - Use TLS for internal communication
   - Validate certificates

3. **Data Retention**
   - Understand data lifecycle
   - Implement retention policies
   - Securely delete old data

### Operational Security

1. **Regular Updates**
   - Keep TinyObs updated to latest version
   - Monitor security advisories
   - Apply patches promptly

2. **Dependency Management**
   - Review dependency updates
   - Use `go mod verify` before updates
   - Run `govulncheck` regularly

3. **Logging and Monitoring**
   - Enable audit logging
   - Monitor security events
   - Set up alerts for suspicious activity

4. **Backup and Recovery**
   - Regular backups of BadgerDB data
   - Test restore procedures
   - Store backups securely

## Secure Configuration Examples

### Production Server Configuration

```go
// Example: Secure server setup
server := &http.Server{
    Addr:         ":8080",
    Handler:      router,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  120 * time.Second,
    
    // Security headers
    Handler: securityHeaders(router),
    
    // TLS configuration (recommended)
    TLSConfig: &tls.Config{
        MinVersion: tls.VersionTLS12,
        CipherSuites: []uint16{
            tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
            tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
        },
    },
}
```

### Docker Security

```dockerfile
# Run as non-root user
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o tinyobs cmd/server/main.go

FROM alpine:latest
RUN addgroup -g 1000 tinyobs && \
    adduser -D -u 1000 -G tinyobs tinyobs

COPY --from=builder /app/tinyobs /usr/local/bin/
USER tinyobs

EXPOSE 8080
CMD ["tinyobs"]
```

### Kubernetes Security

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: tinyobs
spec:
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
    fsGroup: 1000
    
  containers:
  - name: tinyobs
    image: tinyobs:latest
    
    securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop:
        - ALL
    
    resources:
      limits:
        memory: "1Gi"
        cpu: "1000m"
      requests:
        memory: "512Mi"
        cpu: "500m"
```

## Security Checklist for Deployment

### Pre-Deployment

- [ ] Review security policy
- [ ] Configure authentication (when available)
- [ ] Set up TLS/HTTPS
- [ ] Configure firewall rules
- [ ] Set up security monitoring
- [ ] Review logs for sensitive data
- [ ] Test backup and restore

### Post-Deployment

- [ ] Monitor security events
- [ ] Review access logs regularly
- [ ] Keep software updated
- [ ] Rotate credentials
- [ ] Test incident response
- [ ] Review security configuration
- [ ] Conduct security audit

### Ongoing

- [ ] Monthly security review
- [ ] Quarterly penetration testing
- [ ] Annual security audit
- [ ] Monitor CVE databases
- [ ] Track dependency vulnerabilities
- [ ] Review and update security policy

## Vulnerability Disclosure Timeline

We aim to address security vulnerabilities according to the following timeline:

| Severity | Initial Response | Fix Released | Public Disclosure |
|----------|-----------------|--------------|-------------------|
| Critical | 24 hours | 7 days | 14 days |
| High | 48 hours | 14 days | 30 days |
| Medium | 1 week | 30 days | 60 days |
| Low | 2 weeks | 90 days | 120 days |

*Note: These are targets, not guarantees. Complex issues may take longer.*

## Security Audit History

| Date | Type | Findings | Status |
|------|------|----------|--------|
| 2025-11-16 | Internal Code Review | 3 High, 5 Medium | In Progress |

## Contact

For security concerns, please contact:
- **Email**: [security@tinyobs.dev] (update with actual email)
- **PGP Key**: [Link to PGP key] (optional)

For general issues, use GitHub Issues: https://github.com/nicktill/tinyobs/issues

## Credits

We thank the following security researchers for responsibly disclosing vulnerabilities:

- *List will be maintained here*

## References

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [CWE Top 25](https://cwe.mitre.org/top25/)
- [Go Security Best Practices](https://golang.org/doc/security/)
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework)

---

**Last updated:** November 16, 2025  
**Policy version:** 1.0  
**Next review:** February 2026
