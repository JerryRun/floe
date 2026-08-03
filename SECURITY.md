# Security policy

## Reporting a vulnerability

Please do not publish credentials, private keys, server addresses, session
files or proof-of-concept exploits in a public issue. Use the repository's
GitHub private vulnerability reporting feature instead.

Include the affected Floe version, Windows version, reproduction steps and the
security impact. Remove passwords, tokens, fingerprints and private host names
from screenshots and logs.

## Sensitive local data

Floe stores runtime data under `%LOCALAPPDATA%\Floe` on Windows. In particular,
the following files must never be committed or attached to public issues:

- `sessions.json` and `session.key`
- files under `keys/` and `terminal/`
- `tasks.json`, `activity.json` and `floe.log` unless carefully redacted

The HTTP service is designed to listen only on a loopback address. Binding it
to a non-loopback interface is unsupported and may expose file and session
operations to other machines.
