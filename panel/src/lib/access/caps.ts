import type { CatalogScope } from '@/lib/api/authzCatalog';

// siblingRead maps a *.write capability id to its *.read sibling, else null.
// Used by the write-implies-read UI convenience.
export function siblingRead(capId: string): string | null {
    if (!capId.endsWith('.write')) return null;
    return capId.slice(0, -'.write'.length) + '.read';
}

// withImpliedReads returns selected plus, for each selected *.write cap whose
// *.read sibling EXISTS in the catalog (known), that sibling. Deduped, order not
// guaranteed. Presentation-only; the stored/enforced unit is still each cap id.
export function withImpliedReads(selected: string[], known: Set<string>): string[] {
    const out = new Set(selected);
    for (const id of selected) {
        const r = siblingRead(id);
        if (r && known.has(r)) out.add(r);
    }
    return Array.from(out);
}

// filterScopes narrows the catalog to the requested scope names, order preserved.
export function filterScopes(catalog: CatalogScope[], scopes: string[]): CatalogScope[] {
    const set = new Set(scopes);
    return catalog.filter(s => set.has(s.scope));
}

// capIdsForScopes flattens every capability id under the requested scopes.
export function capIdsForScopes(catalog: CatalogScope[], scopes: string[]): string[] {
    const set = new Set(scopes);
    const out: string[] = [];
    for (const s of catalog) {
        if (!set.has(s.scope)) continue;
        for (const cat of s.categories) for (const c of cat.capabilities) out.push(c.id);
    }
    return out;
}
