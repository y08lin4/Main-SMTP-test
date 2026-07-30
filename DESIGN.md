# Design System

## Direction

Faithfully evolve the supplied SMTP tester frontend. This is a restrained light operations tool used on ordinary Windows displays, often while the user is troubleshooting under time pressure. Design serves scanning, accurate entry, and fast interpretation rather than product marketing.

## Color

Use OKLCH tokens. Neutral white and cool gray surfaces carry the interface; blue is reserved for the primary action, focus, and in-progress state. Green, red, and amber are semantic only. Text and placeholders meet WCAG AA contrast.

## Typography

Use the Windows/system sans stack. Keep a compact fixed type scale: 14-16px body and controls, 18-22px panel headings, and a 30px page title. Do not use fluid viewport-based font sizing or display fonts.

## Layout

Use the supplied two-column desktop composition: a wider configuration form and a narrower sticky diagnostic panel. Collapse to one column below tablet width. Use an 8px spacing grid, full-width unframed application header, and individual panels with a maximum 8px radius.

## Components

- Header: product identity on the left; execution-location badge and source/download actions on the right.
- Mode notice: a compact inline information band explaining whether the SMTP connection originates locally or from Cloudflare.
- Form: visible labels, native inputs/selects, grouped sections, inline validation, one primary send action.
- Security choice: three-option native select locally; the online service exposes only STARTTLS 587 and SSL/TLS 465.
- Diagnostic panel: stable connection/authentication/delivery stages, a clear idle/loading/success/error state, server detail, elapsed time, and actionable remediation.
- Public safety notice: concise warning that online credentials transit the Worker and are not stored; recommend the local client for sensitive accounts.
- Footer: GitHub source and Windows download links, version/environment metadata.

## Interaction

Transitions last 150-200ms and communicate state only. Keep layout dimensions stable while sending. Disable duplicate submission, announce results through `aria-live`, retain visible keyboard focus, and restore controls after every outcome.

## Responsive Behavior

Test at 375px, 768px, and 1440px. Header actions wrap without overlap, form groups become single column, the result panel loses sticky positioning, and all buttons remain at least 44px tall.
