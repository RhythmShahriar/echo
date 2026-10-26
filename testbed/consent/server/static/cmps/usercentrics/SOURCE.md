# Usercentrics replica

captured: 2026-10-26

Written fresh for the testbed; no Usercentrics code is copied. The shadow
root on `#usercentrics-root`, the `data-testid` buttons, localStorage
`uc_settings` and `uc_user_interaction`, `window.UC_UI`
(`showFirstLayer`, `showSecondLayer`, `acceptAllConsents`,
`denyAllConsents`) and `window.__ucCmp` (the v3 API with
`denyAllConsents`) follow these public pages:

- https://usercentrics.atlassian.net/wiki/spaces/SKB/pages/705593353
- https://usercentrics.com/beta-docs/control-functionality

The `data-tb-shadow="closed"` switch builds a closed shadow root, the
variant Echo can't click into without the API.
