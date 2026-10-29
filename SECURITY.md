# Security Policy

## Reporting a vulnerability

Please do **not** open a public issue for security vulnerabilities.

Report them privately via [GitHub Security Advisories](https://github.com/rxzsu/discordgo/security/advisories/new)
or by opening a draft advisory. You can expect a response within a week.

Vulnerabilities inherited from upstream [bwmarrin/discordgo](https://github.com/bwmarrin/discordgo)
should be reported there as well.

## Scope

- The library handles bot tokens; leaks of credentials through logs or
  error messages are in scope.
- Everything reachable from untrusted Discord payloads (gateway events,
  REST responses, interaction payloads) is treated as attacker-influenced
  input.
