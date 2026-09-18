# WeazlCloud Multiuser Workplan

Date: 2026-09-18

This workplan moves WeazlCloud from a single household vault to a multiuser node that can run in Docker. The first implementation should establish correct ownership, quota, and isolation before adding convenience features.

## Sovereignty boundary

Identity is local to the WeazlCloud node. There will be no third-party identity system: no OIDC, OAuth, SSO, hosted account, external directory, social login, identity relay, or Weazl-operated account service. User records, password verifiers, sessions, vault keys, recovery material, and authorization decisions live on the node and are administered by its owner.

## Product decisions

### Users and vaults

- The node has a local user registry stored in the data volume. It is the sole identity authority for the node.
- Every user has a separate vault and encryption root. A user can unlock only their own vault.
- User credentials are verifier material only; passwords are never stored in plaintext or passed through Compose environment variables.
- The first account is the node administrator. It can create, disable, and remove users and configure the node quota.
- Library, capsule, Places, and recovery-kit operations carry an authenticated user identity.
- A capsule remains a capability link, but its owner and revocation authority are recorded.
- Sessions are short-lived, server-side or signed with keys held by the node, and must not place passwords in URLs or local storage.

### Global quota

- The usable quota is 97% of the filesystem capacity of the Docker data volume.
- The remaining 3% is reserved for filesystem safety, metadata, temporary restic work, and recovery operations.
- The quota meter displays `used / usable quota`, so reaching 97% physical usage displays as 100%.
- There is no per-file filesize quota. Users may store large files such as ISO images, provided the write fits within the remaining global and user allocation.
- Writes are rejected before they can exceed the usable quota. Failed writes must not leave catalog entries or orphaned temporary objects.
- Each user receives an allocated share from the global quota. The first version should support an administrator-set per-user quota, with the sum of allocations limited to the global usable quota.
- Usage accounting must distinguish logical user bytes, deduplicated physical bytes, and temporary working bytes.

The quota needs a defined source of truth. Do not derive it only from `statfs` or only from the encrypted catalog. Use filesystem capacity for the hard global ceiling and a durable object index for logical and physical accounting.

### Global deduplication

- Deduplication is desirable, but it must not make one user’s encrypted content or metadata visible to another user.
- The preferred design is a global content-addressed object store keyed by a cryptographic digest, with per-user encrypted catalog entries pointing to objects.
- A digest must be computed from plaintext only inside the user’s unlocked context, then used as an opaque object identifier. The object contents remain encrypted at rest.
- Identical plaintext across users may share an encrypted object only if the encryption design does not reveal ownership, filenames, or content to other users. If that cannot be guaranteed, use per-user encryption and accept reduced cross-user dedupe.
- Deduplication must not allow an unauthorized user to test whether another user owns a particular file. Avoid an existence oracle in upload responses and timing where practical.
- Restic is currently the storage engine; before changing its repository layout, measure whether restic’s pack-level dedupe can provide the needed behavior without making quota accounting unreliable. If not, introduce a storage abstraction before implementing cross-user dedupe.

The decision gate is privacy first: per-user isolation wins over maximum dedupe.

### Photo viewing

- Add an image viewer to the authenticated library UI for common raster formats first: JPEG, PNG, GIF, and WebP.
- The viewer must enforce the same user authorization as normal file reads.
- Start with browser-native rendering from an authenticated file endpoint. Do not create a thumbnail cache until the authorization and cache ownership rules are defined.
- Add folder previews and a lightweight gallery only after individual image viewing works.
- Video, RAW formats, EXIF editing, and server-side transcoding are outside today’s scope.

### Library interaction benchmark

The library should feel as simple to use as Google Drive for ordinary file
work. The visual language can remain WeazlCloud’s, but actions must be obvious,
consistent, and available from the item being acted on.

- Add **New folder** from the library toolbar and the current folder’s context menu.
- Add right-click context menus for files and folders. A file menu includes **Share this**, **Open preview** when the browser can render the file, **Download**, **Rename**, and **Delete**. A folder menu includes **Open**, **Upload into**, **Share this**, **Rename**, and **Delete**.
- Keep context-menu actions keyboard accessible and provide an equivalent action menu for touch devices. Right-click cannot be the only route to sharing or destructive actions.
- Clicking a browser-readable file opens an in-library preview. Start with images, PDF, plain text, Markdown, audio, and browser-renderable video. Unsupported files open a details/download view.
- Previews use the authenticated user’s authorization and never create public URLs. **Share this** remains an explicit sealed capsule action.
- Add recursive folder upload. Preserve the selected destination and every file’s relative path, including nested directories and empty folders where supported.
- Show aggregate upload progress and per-file progress, current filename, bytes transferred, failures, retry controls, and a final summary. A failed file must not hide successful files or leave partial catalog records.
- Do not impose an application-level per-file size limit. Large files are limited only by available global quota and the user’s allocation.

## Implementation order for today

### 1. Establish the multiuser boundary

- Define user, session, quota, and ownership records.
- Add a storage schema version and migration path from the current single-vault data layout.
- Separate node state from user state under `/data`.
- Decide which existing single-user data becomes the administrator’s library during migration.
- Add authorization checks at the desk API, library API, capsule API, and drive API boundaries.

Acceptance criteria:

- Two users can be created.
- A user cannot list, read, mutate, or revoke another user’s library or capsules.
- Locking one user’s vault does not expose or destroy another user’s unlocked session.

### 2. Implement authentication and sessions

- Add administrator bootstrap through the desk on a fresh volume.
- Add login, logout, session expiry, and disabled-user handling.
- Hash passwords with Argon2id and keep the password verifier separate from vault encryption keys.
- Add CSRF and origin protection to all browser mutations.
- Keep grab links independent: a recipient with a valid capsule token does not need an account.

Acceptance criteria:

- Passwords and session secrets do not appear in logs, URLs, Compose configuration, or API status responses.
- API requests without a valid session cannot reach user library data.
- Capsule downloads continue to work without an authenticated account.

### 3. Add quota enforcement

- Detect the Docker volume filesystem capacity.
- Calculate `usable = floor(capacity * 0.97)`.
- Expose sanitized quota status: physical used, usable limit, displayed percentage, and per-user allocation/usage.
- Reserve space for a write before invoking restic or object storage.
- Reconcile usage at startup and provide an administrator repair/recount operation.
- Test full-volume behavior, concurrent uploads, deletion, failed uploads, and restart recovery.

Acceptance criteria:

- The hard limit is 97% of the volume capacity.
- The UI meter reaches 100% at that limit.
- No accepted operation can push the durable store beyond the limit because of concurrent requests.

### 4. Choose and prototype dedupe

- Measure the current restic repository behavior with duplicate files from different users.
- Document whether the existing repository can report physical usage accurately enough for quota enforcement.
- Prototype a content index and encrypted object metadata without exposing cross-user existence.
- Compare three options: restic-only dedupe, global encrypted object store, and per-user repositories.
- Select one option based on privacy, recovery, quota accuracy, and migration cost.

Acceptance criteria:

- The prototype demonstrates duplicate storage savings or records a clear reason to defer global dedupe.
- A user cannot infer another user’s filenames, ownership, or file existence.
- Recovery kits and backups still have an unambiguous ownership and restore model.

### 5. Add the photo viewer

- Add MIME/type detection and browser preview actions to the library UI.
- Render through the authenticated file endpoint with correct content type and disposition.
- Add a gallery view for a folder using authorized file metadata.
- Add tests for supported types, unsupported types, missing files, and cross-user access.

Acceptance criteria:

- An authorized user can open supported photos in the browser.
- An unauthorized user receives no image bytes.
- The viewer does not create an unbounded cache or leak filenames through public routes.

### 6. Bring the library to the Google Drive interaction baseline

- Implement folder records and `New folder` in the API and UI.
- Add file and folder context menus with **Share this** as a first-class action.
- Add click-to-preview for browser-readable files and a details/download fallback.
- Add recursive directory upload with relative-path preservation, aggregate and per-file progress, retry, and partial-failure reporting.
- Replace the current request-size ceiling with streamed uploads plus quota reservation. No individual file-size limit is imposed by the application.

## Docker and deployment requirements

- Keep the container non-root with a persistent `/data` volume.
- Document bind-mount ownership for UID/GID `7272`, since the current host deployment uses a bind mount rather than a named volume.
- Do not put administrator passwords, user passwords, vault passphrases, or drive tokens in Compose environment variables.
- Keep desk, grab, and drive as separate listeners while adding authenticated user routes to desk and drive only.
- Add a migration command that can be run against the mounted volume before restarting the service.
- Add health checks for the process and data volume, without exposing user or quota details publicly.

## End-of-day deliverable

The minimum useful result for today is a reviewed multiuser design plus the first vertical slice:

1. User and session records exist with a fresh-install bootstrap.
2. Two users can authenticate and see isolated library namespaces.
3. Global quota calculation and the 97%-to-100% meter behavior are implemented behind a tested interface.
4. The dedupe decision is documented with a small measurement or prototype.
5. An authenticated user can create folders, use context menus, upload a folder recursively with progress, and preview a browser-readable file.
6. An authenticated user can store a large file subject only to available global quota, with no per-file limit.
7. Docker restart and bind-mounted volume migration are tested on `weazlcloud.teralab.local`.

## Explicitly deferred

- Third-party identity, OIDC, OAuth, SSO, hosted accounts, external directories, and multi-node identity federation.
- Public user registration.
- Collaborative sharing or shared writable folders.
- Video transcoding, RAW photo processing, and EXIF editing.
- Cross-user dedupe that weakens confidentiality.
- Replacing the existing recovery-kit format before the user/vault migration path is proven.
