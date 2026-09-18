THE WEAZL LITE ETHOS
Weazl knows. Weazl is wise. Weazl will never tax the gig.
This document is a surface law for friends who are not hardcore and still deserve the real Weazl. It does not replace `weazl_ethos.md`. If this file and the parent ethos collide on cryptography, sovereignty, the LLM boundary, or flow state, the parent wins.

Lite is how Weazl enters a living room without becoming a tourist product.

I. WHO LITE IS FOR
The architect keeps the TUI, the SSH session, the 80x24 CRT, the Omarchy floppy. Lite is for casual-but-experienced people: they know what a backup is, they can pick a folder or an SSH host, they will learn six keyboard shortcuts, and they will not assemble CLI flags for a Tuesday.

They are not beginners who need a cloud nanny. They are not sysadmins who want the engine exposed. They want the job done, in a browser they already have, on a machine they own.

Weazl Lite is not a "virtual assistant," a "copilot," or a simplified cipher. It is the same uncompromising daemon with a listening-desk face.

The proven pattern is WeazlTunes web: a first-class browser desk over a local Go process, while Subweazl remains the hardcore original. The anti-pattern is wrapping the TUI in a browser terminal. That is for people who already live in the TUI. Lite rebuilds the job. It does not stream the console.

II. WHAT NEVER RELAXES
Lite does not get a weaker vault.

Zero Cloud SaaS. No telemetry. No Electron. No npm build in production. No passphrase recovery, escrow, reset questions, or support bypass. Local AES-GCM at rest. Data in motion stays on the machine or the user's own network path to their own store.

The 300 LOC law still applies to Go. Unix-style modularity still applies. The LLM still judges; the application still enforces. Idle still means idle.

If a Lite feature needs a cloud account, a Chromium shell, a second password in front of the vault, or a training-wheels recovery path, it does not ship.

III. THE SURFACE: A DESK, NOT A TERMINAL COSPLAY
The graphical user interface is still bloat when it is a platform. A local desk served by the same statically compiled binary is not Electron. It is the portable widget.

Craft, taken from WeazlTunes:

- One Go process serves the UI, the API, and the work. Assets are embedded. Vanilla HTML, CSS, and JS modules. No frontend framework, no Node runtime, no telemetry beacons, no webfonts from the network.
- Mockup first. The static desk is the contract. The API is paint.
- Persistent chrome: a numbered sidebar, a hero that states the job in one sentence, a sticky footer for the thing happening now.
- Keyboard remains first-class. Chords are visible. Mouse and touch are welcome. `?` is the short-route card, not a manual.
- Outcome language. "Where should the encrypted copies live?" not a Restic locator as the first label. Engine words belong in diagnostics.
- One primary action per screen. Expert tools (tune, nuke, capsules, connection-count trials, LLM curators) live behind a fold or an account menu, not on the home rail.
- Destruction is still explicit. Daily confirms speak in consequences. Break-glass ritual phrases stay behind the expert fold.
- Responsive enough for a laptop and a phone. Not a CRT, not a 4K dashboard of gauges.

On Omarchy, the TUI remains first and forever. Lite is a sibling surface, not a replacement. On Windows and on generic Linux without a native widget, Lite may be the only surface the friend ever sees. The daemon underneath is the same.

IV. NETWORK POSTURE
A listening desk and a vault are not the same threat.

WeazlTunes may bind a household port because it is a music node in front of Navidrome. A Weazl vault in memory is an unlock oracle. Lite binds loopback by default. Closing the tab detaches; it does not lock. Explicit lock zeroes the key. The passphrase is POSTed once into the process. It never lives in the URL, localStorage, query strings, argv, or environment.

Do not invent a web-app username in front of a vault. Unlock is the vault passphrase. Create is confirm-the-passphrase plus the no-recovery warning, spoken plainly.

CSRF, origin, and a same-origin request mark are mandatory. Strict CSP. No third-party scripts. Status shown to the page stays sanitized; real paths and engine diagnostics stay behind unlock, as the Omarchy widget already does.

LAN bind, reverse proxies, and multi-user household accounts are not Lite defaults. If a later product is genuinely a household node, say so in that product's law. Do not smuggle WeazlTunes' 0.0.0.0 into a backup vault because the CSS matched.

V. LANGUAGE AND ATTENTION
Casual-experienced people still hate noise. Lite drops filler from the interface the way Weazl drops filler from an album.

No platform wizard. No package-manager vocabulary on the happy path. No compatibility maze. Detect backstage. When source and target disagree, one warning, then continue with what is compatible.

No verbose AI chatter. No apology screens. Toasts report outcomes. Empty states say what to do next. Errors are actionable and do not leak secrets.

Setup is two steps, not twelve: unlock or forge the vault, then choose where the copies live. Quiet measurement (the 100 MiB non-dedupable pipe probe, connection trials) may run after the first real job. It does not gate the first backup behind a dyno.

VI. POWER IS STILL SOVEREIGNTY
A Lite tab that polls the binary every second is the old widget sin with nicer CSS.

- A stopped application creates no recurring processes, polls, writes, or network.
- A hidden or closed desk owns no animation clock.
- Progress is pushed from a process that is already doing the work.
- Do not launch the full binary from a browser timer.
- Decorative visualization stays off. The footer shows authoritative counters, then sleeps.

Measure before claiming the desk is cheap. Lite does not get a pass because it looks calm.

VII. THE NORTH STAR, FOR THIS ROOM
Protect the friend's flow state.

They came to back up, restore a file, or cut a recovery USB, then return to the gig. They should not have to become the architect to do that. They also should not be protected from the truth: lose the passphrase and the backups are gone; a stolen USB is a brick; Core may not follow them to a different operating system.

Signal over noise. One yellow button for the job. The rest of Weazl stays in the walls.

The system works for the architect. Lite lets the friend into the same house without moving the vault. The gig remains untaxed.
