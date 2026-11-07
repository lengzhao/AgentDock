# Automatic application owner binding

**Goal:** Automatically bind the platform application's verified owner on first setup or the next foreground/service start of an existing installation.

**Approved design:** Query the configured application's own information with its credentials and `user_id_type=open_id`. Use the platform's owner ID, never the first message sender or registration confirmer. Preserve an existing binding. If lookup fails or the owner is unavailable, leave access closed and print the manual binding command. Persist atomically and clear obsolete pending challenges only when binding succeeds.

**Architecture:** A Lark adapter queries the official application API through the Go SDK. A CLI helper resolves credential references, bounds the lookup with a timeout, and updates the existing access store. Setup and run invoke the helper; runtime startup lets existing installations migrate without re-registering.

**Tech Stack:** Go, Lark oapi-sdk-go v3, existing access JSON store.

## Implementation plan

1. Add failing adapter tests for Lark/Feishu endpoints, explicit open-ID requests, owner precedence, missing identity, API errors, and context cancellation. Implement the owner lookup without logging credentials or API bodies.
2. Add failing CLI tests for initial binding, preserving existing owner without network access, lookup failure remaining unbound, and clearing a stale challenge. Implement the helper with a second owner check in the atomic update.
3. Wire the helper after platform setup and before runtime startup. Lookup failures warn and retain manual binding. Persist failures remain visible.
4. Run focused tests, full tests, vet, diff checks, and rebuild `bin/agentdock`. Verify the installed application's lookup and binding without sending messages or granting new platform permissions.
