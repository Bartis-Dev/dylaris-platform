import { redirect } from 'next/navigation';

// Routes moved to /admin/routes, next to Users and All Servers: they are an
// administrative list of what the gateway serves, not a property of the
// infrastructure the way a machine or an edge is.
export default function RoutesRedirect() {
    redirect('/admin/routes');
}
