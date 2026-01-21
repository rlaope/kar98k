# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.x.x   | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a Vulnerability

If you discover a security vulnerability, please report it through GitHub's private vulnerability reporting:

1. Go to the Security tab of this repository
2. Click "Report a vulnerability"
3. Provide details about the vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

### What to include

- Type of issue (e.g., buffer overflow, injection, etc.)
- Full paths of source files related to the issue
- Location of the affected source code (tag/branch/commit or direct URL)
- Any special configuration required to reproduce the issue
- Step-by-step instructions to reproduce the issue
- Proof-of-concept or exploit code (if possible)
- Impact of the issue, including how an attacker might exploit it

### Response Timeline

- **Initial Response**: Within 48 hours
- **Status Update**: Within 7 days
- **Resolution**: Depends on complexity, typically 30-90 days

## Security Best Practices

When using kar98k:

1. **Never commit sensitive data** in configuration files
2. **Use environment variables** for secrets like API tokens
3. **Restrict network access** to the metrics endpoint
4. **Run as non-root** in containerized environments
5. **Keep dependencies updated** regularly
