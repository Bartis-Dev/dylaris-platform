import { redirect } from 'next/navigation';

// The Edges tab became Gateway: the same screen, plus the warp leaders and beam
// relays that had no screen at all. This address stays as a redirect because it
// was a real URL somebody could bookmark or paste into a ticket.
export default function EdgesRedirect() {
    redirect('/infrastructure/gateway');
}
