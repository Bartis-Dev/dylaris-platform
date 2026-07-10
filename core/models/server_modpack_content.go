package models

// ServerModpackContent is one Modrinth-identified member of the modpack a
// server was installed from, snapshotted per (server, sub-server) at
// install/reinstall. It is the durable link a server otherwise lacks to its
// modpack's contents, and it backs the Content-tab cross-check (advisory,
// client-side): the panel warns before installing a mod that is already in the
// pack, or a NEW client-required mod that players would be missing.
//
// Only Modrinth-identified entries are stored; a pack member with no Modrinth
// project id (a plain uploaded jar) is not cross-checkable and is skipped.
type ServerModpackContent struct {
	ID                    int    `json:"id"`
	ServerID              int    `json:"serverId"`
	SubServerName         string `json:"subServerName"`
	ModrinthProjectID     string `json:"modrinthProjectId"`
	ModrinthVersionID     string `json:"modrinthVersionId"`
	ModrinthVersionNumber string `json:"modrinthVersionNumber"`
	FileName              string `json:"fileName"`
	Side                  string `json:"side"`
}
