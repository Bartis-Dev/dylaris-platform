import { redirect } from 'next/navigation';

// /infrastructure is the address the navigation still points at - the nav rows
// are seeded in the database, so the link cannot be changed from here. It sends
// you to /infrastructure/nodes so there is exactly ONE canonical URL per tab and
// no second address showing the same screen.
//
// Under `output: export` this compiles to a redirect marker the client router
// acts on after hydration. That is fine here: every authed page in this panel
// ships a shell and hydrates - none of them prerenders content, because the
// layout renders nothing until the session is known - so the redirect costs one
// client-side navigation, not a blank page that would otherwise have shown
// something.
export default function InfrastructureIndex() {
    redirect('/infrastructure/nodes');
}
