# Product

## Register

product

## Users

Windows administrators, developers, support engineers, and mail-server operators who need to verify SMTP delivery settings and identify whether a failure occurs during connection, TLS negotiation, authentication, sender validation, recipient validation, or message submission.

## Product Purpose

Provide one consistent SMTP testing workflow in two forms: a downloadable Windows client that diagnoses the user's actual local network path, and a public Cloudflare-hosted service that performs a constrained test from Cloudflare's network. Success means users receive a clear, actionable result without installing a development runtime or exposing credentials beyond the selected execution environment.

## Brand Personality

Trustworthy, restrained, and diagnostic. The interface should feel like a focused operations utility: calm while idle, explicit while working, and precise when explaining failures.

## Anti-references

Do not turn the tool into a marketing landing page, generic card dashboard, terminal cosplay, or decorative dark-mode control panel. Avoid oversized hero copy, ornamental gradients, excessive badges, hidden field labels, and vague error messages. Preserve the supplied SMTP tester's compact two-column form-and-results composition.

## Design Principles

1. Keep the primary SMTP test visible and immediately usable.
2. Show where the test runs and explain the diagnostic consequences of that location.
3. Convert protocol failures into specific, actionable Chinese guidance while retaining useful server details.
4. Treat credentials as transient secrets: never persist, echo, log, or prefill passwords.
5. Keep local and online workflows visually consistent while making their security and capability differences unmistakable.

## Accessibility & Inclusion

Target WCAG 2.2 AA. All fields retain visible labels, status never relies on color alone, keyboard focus is clearly visible, touch targets are at least 44 pixels, live results are announced, and non-essential motion is disabled when reduced motion is requested.
