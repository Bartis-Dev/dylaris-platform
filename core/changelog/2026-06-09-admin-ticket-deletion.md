---
date: 2026-06-09
type: feature
audience: admin
title: Admins can now delete tickets with audit trail
---
A new admin-only **Delete** button on the ticket detail page hard-deletes a ticket and all its messages, attachments, and watchers. The action is gated by a separate toggle in Settings → Ticket Settings (default off).

Every deletion writes an immutable audit row — subject, owner username, category, and the admin who triggered it — visible at **Settings → Ticket Settings → Deletion log**. Ticket content and replies are not retained.
