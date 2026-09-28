# Production login latency trace — September 28, 2026

Implementation plan: [September 28 responsiveness workbook](../responsiveness_workbook_2026-09-28.md).

The import watcher reports completion. Production was inspected through the
jumpbox without rebuilding, restarting, deploying code or changing vault data.
The diagnostic created an authenticated session against the server's loopback
listener, timed the existing API requests, and logged out. Credentials, session
cookies, file paths and response contents were not recorded in this document.

## Measurements

Sequential requests on production, excluding the user's network and browser:

| Request | First measurement | Repeat | Response |
| --- | ---: | ---: | ---: |
| POST `/api/login` | 107 ms | 70 ms | 73 bytes |
| POST `/api/unlock` | 96 ms | — | 18 bytes |
| GET `/api/status` | 1.1 ms | — | 206 bytes |
| GET `/api/library` | 2,230 ms | 2,161 ms | 11,696,193 bytes; 96,971 entries |
| GET `/api/quota` | 4,222 ms | 4,225 ms | 229 bytes |
| GET `/api/capsules` | 1.8 ms | — | 977 bytes |
| GET `/api/places` | 0.9 ms | — | 121 bytes |
| GET `/api/uploads` | 0.8 ms | — | 3 bytes |

The first API probe used a cookie jar that correctly refused to send a Secure
cookie over ordinary loopback HTTP, causing a 401 on unlock. The successful
probe used the existing watcher's explicit loopback-only cookie-header approach.
That diagnostic 401 is not evidence of an application login failure.

## Confirmed request path

The deployed `unlock-form` handler awaits login, unlock, `loadLibrary`, capsules,
places, access requests (for administrators) and upload-session restoration before
calling `openDesk`. `loadLibrary` also awaits `loadQuota`. No pending submit state
or stage feedback is shown during these waits.

`listLibraryFor` calls `Library.Ensure`, which reloads/decrypts/parses the catalog.
The quota handler calls both `Library.Dedupe` and `Library.Trash`, and each calls
`ensure` again. Thus the serialized login path reloads the catalog three times.
The two-second listing and four-second quota measurements are consistent with
that code path; these are endpoint timings, not function-level CPU profiles.

The measured server-side waits add approximately 6.6 seconds before the desk can
open. Browser parsing/rendering and transferring the 11.7-MB listing add further
delay. No browser main-thread or WAN trace was collected in this investigation.

## Deployment implication and next changes

The local Photos/UI work skips the pre-login full listing when opening directly
into Library or Photos, but the default view remains Home and still awaits it.
On Library, `loadLibrary` still delays the final render until quota returns.
Consequently last night's build is not a complete fix for the reported delay.

Before rollout:

1. Open the authenticated desk after unlock and show explicit loading state for
   the selected view. Fetch secondary panels independently; Home must not require
   an entire library download. Disable duplicate submission and announce progress.
2. Render Library results without waiting for quota/dedupe. Keep failures visible
   and prevent late responses from an old account/locked session populating UI.
3. Combine quota's catalog-derived statistics into one authorized snapshot rather
   than two reloads. Do not weaken password hashing or vault authentication.
4. Follow up with paginated folder listing/search and safe catalog reuse; an
   11.7-MB whole-library response is still expensive on a remote connection.

Validate with delayed-response browser tests (login feedback, desk visibility,
library rendering before slow quota, wrong credentials and a separate vault
passphrase), plus catalog-statistics tests if the backend path changes.

This turn traced the issue; it did not implement or deploy these follow-up changes.
