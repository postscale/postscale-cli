# Changelog

## 1.0.0 — 2026-09-30

Initial stable release of the official Postscale CLI.

- Domain listing, creation, inspection, DNS records, and verification by name or UUID.
- Email sending from JSON files or stdin, local dry-run validation, and support
  for API-native templates and base64 attachments.
- Outbound email listing, inspection, and delivery events; inbound listing and inspection.
- Webhook endpoint listing and delivery-history diagnostics.
- Named profiles with OS keychain storage, environment authentication, endpoint
  isolation, and explicit test/live credential context.
- JSON results and errors, request IDs, filters, checked pagination, total API
  timeouts, and distinct exit codes. Read retries never replay mutations.
- Bash, Zsh, Fish, and PowerShell completion.
- Downloads for Linux and macOS amd64/arm64 and Windows amd64, plus Go installation.

### Known limits

- `auth login` and `auth status` inspect local configuration without verifying a
  key against the API; logout removes local credentials without revoking keys.
- Get/events commands use the email resource UUID from `emails list`, not the
  SMTP `message_id` returned by sending.
- The pinned SDK omits newer API response `environment` fields. Use
  `context.credential_environment` together with `data.status`.
- Send idempotency is not supported. After an uncertain network outcome, inspect
  history before resubmitting. Sends and other mutations are not retried.
- Local webhook forwarding, batch sending, deletion, and dedicated template or
  attachment helpers are not included.
