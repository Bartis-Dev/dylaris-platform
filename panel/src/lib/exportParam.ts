/**
 * The placeholder a dynamic route segment is exported under.
 *
 * `output: "export"` has no server, so a route like /servers/[id] cannot be
 * rendered per request - Next demands generateStaticParams and writes one file
 * per returned value. The ids are runtime data, so there is exactly one useful
 * value to return: a placeholder that stands for all of them. The build emits
 * `out/servers/__param__/audit.html`, and Core serves that file for
 * /servers/123/audit, /servers/456/audit and every other id.
 *
 * CROSS-LANGUAGE CONTRACT. Core reads the same literal to decide which path
 * segments are wildcards (core/panelfs). It is derived from the exported
 * directory names rather than from a hand-kept route table, so a new dynamic
 * route needs no Core change - but the two spellings must stay identical.
 *
 * Chosen to be unreachable as a real value: ids here are numeric or random
 * tokens, and no id is ever the literal "__param__".
 */
export const EXPORT_PARAM = '__param__';
