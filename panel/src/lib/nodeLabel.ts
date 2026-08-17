// How a node is labelled in the UI.
//
// `token` is NOT a label. Since the node-pairing hardening, Core mints an
// unguessable UUID as the node identity and keeps the node-reported hostname in
// a separate display_name column. Older rows had token == hostname, so the
// panel used to render the token and looked right - until the first node
// enrolled under the new scheme showed up as a bare UUID.
//
// Precedence: the admin-editable display name, then the row name, then the id.
// The token never appears here; where an operator needs it (deploy snippets,
// --token) it is shown explicitly as the identity, not as a name.
export function nodeLabel(node: { displayName?: string; name?: string; id?: number | string }): string {
    const display = node.displayName?.trim();
    if (display) return display;
    const name = node.name?.trim();
    if (name) return name;
    return node.id !== undefined ? `Node ${node.id}` : 'Node';
}
