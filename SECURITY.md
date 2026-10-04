# Security Policy

## Reporting a Vulnerability

Please **do not** open a public issue or discussion that includes any
vulnerability details.

GitHub's private vulnerability reporting (the "Report a vulnerability"
button under the Security tab) is only available on public repositories,
and this repository is currently private, so that path isn't available to
reporters here yet.

Until then:

1. Open a regular issue that contains **no vulnerability details** -
   just a note that you'd like to report a security issue and be
   contacted privately.
2. A maintainer ([@jeremyje](https://github.com/jeremyje)) will follow up
   to arrange a private channel, and can manually open a draft
   [security advisory](https://github.com/cloudfra/template-go/security/advisories)
   from there to continue the discussion and coordinate a fix
   confidentially - repo admins can create one directly regardless of
   whether the self-serve reporting feature is enabled.

Once this repository is public (or private vulnerability reporting
becomes available on this plan for private repositories), we'll switch
back to GitHub's built-in "Report a vulnerability" flow and update this
document accordingly.

This is especially relevant given the code-signing pipeline this template
ships (`make release-binaries`, `certtool`, `osslsigncode`/`openssl cms`) -
please report anything affecting binary signing or verification the same
way.

We'll acknowledge new reports as soon as possible and keep you updated as
the issue is investigated and resolved.
