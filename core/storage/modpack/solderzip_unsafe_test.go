package modpack

import "testing"

// Every Solder zip the platform authors goes through these two, and the
// launcher extracts what they produce verbatim into the instance root. The
// guard belongs here rather than at each call site: the pack text editor
// re-wraps a stored zip around a target path read back out of the database,
// which skips the validation the original upload passed through.
func TestSolderZipAuthorsRefuseAnEscapingEntry(t *testing.T) {
	content := []byte("x")

	for _, bad := range []string{
		"../../../../etc/cron.d/evil",
		"mods/../../evil.jar",
		`mods\..\..\evil.jar`,
		"/etc/passwd",
		"C:/windows/system32/evil.dll",
	} {
		if _, err := BuildSolderContentZip(bad, content); err == nil {
			t.Errorf("BuildSolderContentZip wrote an entry at %q", bad)
		}
	}
	for _, bad := range []string{"../evil.jar", `..\evil.jar`, "../../x/y.jar"} {
		if _, err := WrapJarAsSolderZip(bad, content); err == nil {
			t.Errorf("WrapJarAsSolderZip wrote an entry at mods/%s", bad)
		}
	}

	// The ordinary cases must keep working, byte-stably.
	if _, err := BuildSolderContentZip("mods/sodium.jar", content); err != nil {
		t.Fatalf("an ordinary inner path must still build: %v", err)
	}
	if _, err := WrapJarAsSolderZip("sodium.jar", content); err != nil {
		t.Fatalf("an ordinary file name must still wrap: %v", err)
	}
}
