# Postscale CLI

The official Postscale CLI. Configure domains, send email, and inspect outbound,
inbound, and webhook delivery from your terminal or scripts. Current release:
**1.0.0**.

## Install

Download the archive for your platform and `checksums.txt` from the
[1.0.0 release](https://github.com/postscale/postscale-cli/releases/tag/v1.0.0).
The prebuilt binaries do not require Go.

| Platform | Archive |
| --- | --- |
| macOS Apple Silicon | `postscale_1.0.0_darwin_arm64.tar.gz` |
| macOS Intel | `postscale_1.0.0_darwin_amd64.tar.gz` |
| Linux ARM64 | `postscale_1.0.0_linux_arm64.tar.gz` |
| Linux AMD64 | `postscale_1.0.0_linux_amd64.tar.gz` |
| Windows AMD64 | `postscale_1.0.0_windows_amd64.zip` |

On macOS or Linux, verify the SHA-256 hash against the matching line in
`checksums.txt`. For example, on Apple Silicon:

```sh
shasum -a 256 postscale_1.0.0_darwin_arm64.tar.gz
tar -xzf postscale_1.0.0_darwin_arm64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 755 postscale "$HOME/.local/bin/postscale"
export PATH="$HOME/.local/bin:$PATH"
postscale --version
```

Use your platform's archive name. Linux also provides `sha256sum` for hash
verification. Add the PATH export to your shell startup file to keep it across
sessions. Archives include `examples/message.json` for the quickstart below.

On Windows, compare the SHA-256 hash with `checksums.txt`, extract the archive,
and add the destination directory to your user PATH:

```powershell
Get-FileHash .\postscale_1.0.0_windows_amd64.zip -Algorithm SHA256
Expand-Archive .\postscale_1.0.0_windows_amd64.zip -DestinationPath "$env:LOCALAPPDATA\Postscale\bin" -Force
& "$env:LOCALAPPDATA\Postscale\bin\postscale.exe" --version
```

### Install with Go

Requires Go 1.25 or newer:

```sh
go install github.com/postscale/postscale-cli/cmd/postscale@v1.0.0
```

The binary is installed in `GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is
unset. Add that directory to PATH.

### Upgrade or uninstall

To upgrade, download and verify the desired release, then replace the installed
binary using the same installation steps. Go users can install a specific new
tag or use `go install github.com/postscale/postscale-cli/cmd/postscale@latest`.

To uninstall, remove the installed binary. Run `postscale auth logout --profile
NAME` for each saved profile first if you also want to remove its local keychain
credential. This does not revoke API keys at Postscale. Environment credentials
and application data are not removed by uninstalling the binary.

### Shell completion

Generate completion scripts for your shell:

```sh
postscale completion bash > postscale.bash
postscale completion zsh > _postscale
postscale completion fish > postscale.fish
postscale completion powershell > postscale.ps1
```

Run `postscale completion SHELL --help` for the shell's installation instructions.

## Quickstart

Set `POSTSCALE_API_KEY` through your shell or secret manager using a `ps_test_`
credential, then save it as a profile and inspect a domain:

```sh
postscale auth login --profile test
postscale auth status --profile test
postscale domains list --profile test
postscale domains dns example.com --profile test
postscale emails send --file examples/message.json --dry-run
postscale emails send --file examples/message.json --profile test
```

Edit the sample sender and recipients before sending. The local dry-run needs
no credentials. A profile name does not select test mode; the key must start
with `ps_test_` to simulate sending.

## Build from source

The CLI is a standalone Go module. No API server, database, or local SDK checkout
is needed to build it.

```sh
git clone https://github.com/postscale/postscale-cli.git
cd postscale-cli
make build
./bin/postscale --help
export PATH="$PWD/bin:$PATH"
```

The SDK is pinned to its publicly available commit in `go.mod`; there is no
local `replace` directive.

## Domain setup and email inspection

```sh
postscale domains create example.com
postscale domains dns example.com
postscale domains verify example.com
postscale domains list --all --json
postscale emails list --subject receipt --limit 25 --json
postscale emails get EMAIL_RESOURCE_UUID
postscale emails events EMAIL_RESOURCE_UUID
postscale inbound list --query invoice --all
postscale inbound get EMAIL_RESOURCE_UUID
postscale webhooks list
postscale webhooks deliveries list --status failed --days 7
```

Domain get/dns/verify accept a domain name or UUID. A name is resolved using the
selected account's paginated domain list. `domains create` defaults to outbound;
`--type` also accepts inbound, both, and alias. Inbound setup can explicitly
acknowledge MX requirements with `--acknowledge-inbound-mx`.

`emails get/events` take `data[].id` from `emails list`, not the SMTP `message_id`
returned by sending. Domain verification prints DNS diagnostics and exits 1 if
the domain is still unverified. Incomplete webhook history preserves the returned
data and warnings on stdout, emits an error on stderr, and exits 1.

## Sending

Create a request using the API's field names:

```json
{
  "from": "sender@example.com",
  "to": ["recipient@example.net"],
  "subject": "Hello from Postscale",
  "text_body": "Hello"
}
```

```sh
postscale emails send --file message.json --dry-run
postscale emails send --file message.json --profile test
cat message.json | postscale emails send --file - --profile test --json
```

`--dry-run` only validates local JSON, required fields, and attachment limits. It
makes no API request and does not verify domain ownership or sending eligibility.
Unknown fields, multiple JSON documents, and inputs over 80 MiB are rejected.
Templates and base64 attachments use the Go SDK/API request fields (`template`,
`template_id`, `variables`, `attachments`, `html_body`, etc.).

A `ps_test_` credential records a simulated email without delivery, quota usage,
warming effects, or delivery webhooks. A `ps_live_` credential submits live mail.
The `context.credential_environment` field reports the credential prefix,
independently of the profile's name. The pinned SDK currently omits newer API
`environment` response fields, so use this context together with `data.status`.

Sends and other mutations are never automatically retried. The API does not
support send idempotency keys. If a network failure or timeout leaves the result
uncertain, inspect email history before submitting again.

## Pagination

Domain, email, inbound, and delivery-history lists accept `--limit` (1–100,
default 50), `--offset`, and `--all`. They return an array in `data` and a
`pagination` object with offset, limit, returned count, optional total, has_more,
and optional next_offset. `--all` collects remaining pages within the total
command timeout; results are emitted only after successful collection.

The domain API has no total count. A full page means another page may exist;
`--all` stops at the first short/empty page. Outbound, inbound, and webhook
delivery lists supply totals. Repeated or inconsistent pages fail instead of
silently duplicating or dropping results. Lists are not atomic snapshots during
concurrent changes.

`webhooks list` uses the server's unpaginated endpoint and returns every endpoint
in one request; it has no pagination flags.

## Authentication

Set `POSTSCALE_API_KEY` through your shell or CI secret manager. Run
`postscale auth status` to inspect local selection; it does not verify the key
with the API. Commands never load application `.env` files.

Alternatively, `postscale auth login --profile test` saves the environment key
in the OS keychain. Use `--key-stdin` to read a key piped from a secret manager.
No API-key command-line flag or plaintext credential fallback is provided.

```sh
postscale auth profiles
postscale auth use test
postscale auth status --profile test
postscale auth logout --profile test
```

Login and logout manage local credentials only. Login does not validate or
create an API key; logout does not revoke it at Postscale.

Explicit `--profile` (then `POSTSCALE_PROFILE`) selects a keychain profile and
ignores ambient `POSTSCALE_API_KEY` and `POSTSCALE_BASE_URL`. Otherwise an
environment API key takes precedence over the default saved profile. A saved
credential stays bound to its endpoint; create a separate profile for a different
endpoint. A profile named `test` only simulates sends when its key is `ps_test_`.

Profile metadata is saved atomically with mode 0600 in
`<os.UserConfigDir()>/postscale/config.json`. Set `POSTSCALE_CONFIG_DIR` to override
that directory. API keys are stored in macOS Keychain, Windows Credential Manager,
or the Linux Secret Service. If the keychain is unavailable, use environment
authentication; the CLI never falls back to saving keys in files.

## Output and execution

Results use a JSON envelope with `data` and, for authenticated operations,
`context` (`profile`, `base_url`, `credential_source`, `credential_environment`).
Use `--json` for compact output. Errors are JSON on stderr with an `error` object
containing `code`, `message`, and available HTTP status, request ID, and retry
delay. Help and version output are plain text. There are no interactive prompts.
Successful API results include `request_id` when the server supplies it (the
last page's request ID for a collected list).

`--base-url` accepts an HTTPS origin without `/v1`. Loopback HTTP is supported for
local development. API redirects are rejected. `--timeout` bounds the entire API
command, including pagination and retry delays. `--retries` affects reads only.

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | API, protocol, output, or unsuccessful workflow result |
| 2 | Invalid arguments, input, or local configuration |
| 3 | Authentication or permission failure (401/403) |
| 4 | Rate limited (429 after read retries) |
| 5 | Network failure or timeout |
| 130 | Interrupted |

## Development

From the standalone repository root (or `cli/` in the Postscale monorepo):

```sh
make check
make cross-check
make build VERSION=1.0.0
```

Automated tests use local HTTP fixtures and an in-memory keychain. They do not
send email or contact real accounts. See [CHANGELOG.md](CHANGELOG.md) for release
history and [RELEASING.md](RELEASING.md) for the release process.

The repository root also provides `make build-cli` and `make check-cli`.
`cross-check` compiles Linux/macOS amd64 and arm64 plus Windows amd64 into
`dist/` within the CLI module. `make release VERSION=1.0.0` packages a clean,
committed source tree into five archives and a SHA-256 manifest under
`dist/release/1.0.0/`. GitHub Actions verifies the packaged binaries on Linux,
macOS, and Windows before creating a draft release.

Report issues at [postscale/postscale-cli](https://github.com/postscale/postscale-cli/issues).
