# Security reporting

Agentmux executes owner-selected tools and commands. Its managed runtime and
workbench are experimental. Read the [trust boundaries](docs/security-model.md)
before using it with sensitive repositories or credentials.

## Report privately

Use the repository's **Security → Report a vulnerability** action if it is
available. Otherwise contact the maintainer through an existing trusted private
channel to arrange a private report. Do not open a public issue containing an
exploit, credential, owner link, native transcript or private repository content.
The maintainer account is [pagel-s](https://github.com/pagel-s).

Include the Agentmux version/commit, OS, relevant provider/tool versions,
selected storage/isolation settings, expected boundary, observed behavior and
minimal reproduction using synthetic data. Redact secrets and keep original
evidence private. A report without working exploitation details is still useful.

## Scope and maintenance

Reports involving authorization bypass, cross-agent disclosure, unsafe source
application, credential exposure, execution outside a requested boundary or
false verification/recovery success are especially relevant. Provider-side
behavior and host compromise have the limits described in the trust model.

Reproduce against current `main` or the latest release when practical. There is
no long-term-support branch or guaranteed response time. Confirmed fixes will
be delivered in a new version; published tags and assets will not be replaced.

Checksums detect a mismatch with the release manifest. They are not an
independent signature or a third-party security assessment.
