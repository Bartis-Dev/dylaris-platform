/**
 * Whether an edge's splice sidecar is running a DIFFERENT image from the one
 * its `SPLICE_IMAGE` reference names.
 *
 * Separate from "a newer splice version exists", because the two are different
 * faults with different answers. Behind means schedule a bump. Mismatch means
 * the running container is not what the pin says at all, and the pin cannot be
 * trusted to describe production.
 *
 * The version pair cannot express it. A tag is not immutable: deleting it from
 * the registry frees the name, and a later build can take it. That happened to
 * `splice-0.13.0` - both production edges ran a 2026-08-19 image while the
 * registry served a 2026-08-28 one - and the panel reported both edges current,
 * because `splice_version` and `splice_version_latest` each read "0.13.0".
 *
 * An UNKNOWN half is not a match. An edge that could not inspect Docker reports
 * empty, and reading two empties as agreement would turn "I cannot tell" into a
 * green row - the failure mode this check exists to remove.
 */
export function spliceImageMismatch(running?: string, available?: string): boolean {
    if (!running || !available) return false;
    return running !== available;
}
