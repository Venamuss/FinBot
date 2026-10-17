# FinBot interface

## Context and direction

The admin panel is a family expense journal. Its primary task is finding and correcting a record. The audience is a family administrator using a trusted local installation. Adjectives: calm, precise, readable, practical. Essence: shared household journal. Light mode, moderate density, Russian copy.

Considered a compact spreadsheet, a personal finance dashboard, and a bound household ledger. The ledger fits the requested CRUD scope: one continuous table below filters, with a dark green top rule as the signature. No navigation to nonexistent sections or summary metrics.

Reference: [GOV.UK tables](https://design-system.service.gov.uk/components/table/) for row separation and numerical alignment; this is inspiration for interaction clarity, not copied code. Plain CSS and native controls keep implementation small.

## Typography and tokens

Self-hosted Cantarell variable font (SIL Open Font License, bundled alongside the font) for headings, controls, and body. Humanist Cyrillic letterforms suit a household tool. No font provider requests at runtime. Font fallbacks: Trebuchet MS, sans-serif. Scale: approximately 1.125, base 16px; 14/16/18/20.25/22.78/28.83px, line height 1.5 (titles 1.2–1.4). Weights 400/600/700/800. Amounts and IDs use tabular numerals.

Canonical CSS tokens are projected into `frontend/src/styles.css`:

| Role | OKLCH |
|---|---|
| Background | 97% 0.007 160 |
| Surface | 100% 0 0 |
| Foreground | 25% 0.025 165 |
| Muted | 45% 0.02 165 |
| Border | 83% 0.018 165 |
| Control border | 62% 0.02 165 |
| Accent | 33% 0.05 170 |
| Accent foreground | 99% 0.005 160 |
| Accent hover | 27% 0.05 170 |
| Tint | 94% 0.016 165 |
| Success | 34% 0.07 155 |
| Error | 41% 0.15 25 |
| Error surface | 97% 0.014 25 |
| Warning | 43% 0.08 75 |

Neutral surfaces dominate; green marks the masthead, journal edge, and primary action. Spacing uses 4/8/12/16/24/32/48px; radius 4px; defined borders without shadows. Desktop content maximum 1440px, 32px side margins. Filters use four columns, two below 900px, one below 600px. The journal scrolls horizontally on small screens without expanding the page.

## Controls and accessibility

Native labelled inputs and selects, 44px minimum control height, a visible 3px focus outline. Primary buttons are filled; secondary outlined; table actions text. Hover/active/disabled/loading/error states are explicit. Native modal dialogs supply focus containment and Escape; closing restores focus, and dismissal is blocked during writes. Deletion initially focuses Cancel. Empty, loading, and failed requests have separate states with retry. Inline field errors are linked to controls; errors and success use text as well as color. Comments wrap and render only as React text. No imagery, icon library, dark theme, or animation is needed.

Member management uses a native collapsible «Участники семьи» section above filters, following the same borders, spacing, and controls. Names are primary text with Telegram IDs in a secondary line; filter options combine both and retain the ID as their value. Existing IDs are static text in the name editor. Long names wrap and member rows can wrap on narrow screens. New and edited names share the existing native dialog/focus behavior.

## Audit and changelog

- 2026-10-17: added optional member-name metadata management, ID fallback, secondary labels, and responsive member rows; retained the existing journal tokens and type system.

- 2026-10-17: introduced the journal direction and token system for Slice 04. Source and rendered audit: one primary creation action, restrained color, right-aligned amounts, no metrics, decorative cards, gradients, shadows, or motion. Desktop 1440px and mobile 390px layouts inspected. Fixed mobile overflow from absolutely positioned accessible table labels; initial Cancel focus now explicitly follows showModal. Native modal excludes background controls, Escape returns focus, and invalid fields retain values. Text contrast and control boundary checks are recorded in `docs/tasks.md`; table separators intentionally use a lighter border token than interactive fields. Browser resource failures occur only during deliberate network-error simulation.
