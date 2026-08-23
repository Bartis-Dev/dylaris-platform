// <input type="datetime-local"> works in the reader's LOCAL time and has no
// timezone in its value; every API field it feeds is RFC3339. These two are the
// conversion, kept in one place because a second caller (custom-tab share-link
// expiry) now needs exactly what the maintenance window already did, and two
// copies of a timezone conversion drift in the direction nobody notices - an
// hour off, only for readers not on UTC.

// toLocalInput renders an RFC3339 instant as the "YYYY-MM-DDTHH:mm" the input
// expects, in local time. Returns "" for anything unparseable so the field
// simply shows empty rather than "Invalid Date".
export function toLocalInput(iso: string): string {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// fromLocalInput turns the input's local-time value into an RFC3339 UTC
// instant. Returns "" when the field is empty or half-typed - callers treat
// that as "no value", which is what an empty picker means.
export function fromLocalInput(local: string): string {
    if (!local) return '';
    const d = new Date(local);
    if (Number.isNaN(d.getTime())) return '';
    return d.toISOString();
}
