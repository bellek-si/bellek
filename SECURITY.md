# Security

## Report a vulnerability

Email info@bellek.si with the subject "Security". Do not open a public issue for it.

Say what you found, how to reproduce it and what it lets someone do. We reply to every report, tell you what we will change, and say when the fix is out.

## What is in scope

This repository is the Bellek client: the `bellek` command, the Claude Code plugin and the hook settings. It runs on your machine and sends what your coding sessions do to the Bellek server you point it at.

Report anything that lets the client:

- send something it should not, such as a secret the redaction should have removed, a file under `BELLEK_CAPTURE_IGNORE`, or text from a session that capture is off for
- send your API key anywhere but `BELLEK_URL`
- write outside its state directory or the folder you passed to `bellek export`
- run a command or change a file because of something the server sent

The hosted service at https://bellek.si is in scope too. Report problems with it to the same address.

## What the client sends

[docs/privacy.md](docs/privacy.md) lists every request the hooks make and what each one carries.
