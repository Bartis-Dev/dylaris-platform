package modpack

import (
	"archive/zip"
	"bytes"
	"fmt"
	"time"
)

// zeroModTime is a fixed timestamp stamped into every Solder content-zip entry so
// the archive bytes (and therefore the md5) are reproducible run-to-run. The
// Technic launcher's differential cache keys on that md5, so an unstable mod-time
// (the default archive/zip behaviour) would make it re-download unchanged mods.
var zeroModTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// BuildSolderContentZip packages a single file into a Solder-format zip whose one
// entry sits at innerPath (a .minecraft-relative path such as "mods/foo.jar" or
// "resourcepacks/bar.zip"). The launcher extracts the zip into the instance root,
// so innerPath must already be instance-relative. The entry's mod-time is zeroed
// for byte-stable, reproducible md5 output.
func BuildSolderContentZip(innerPath string, content []byte) ([]byte, error) {
	if innerPath == "" {
		return nil, fmt.Errorf("solderzip: empty inner path")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: innerPath, Method: zip.Deflate}
	hdr.Modified = zeroModTime
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		_ = zw.Close()
		return nil, err
	}
	if _, err := w.Write(content); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
