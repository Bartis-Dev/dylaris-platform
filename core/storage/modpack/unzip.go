package modpack

import (
	"archive/zip"
	"bytes"
	"io"
	"path"
	"strings"
)

// maxInnerJarBytes bounds how much we decompress from a mod zip when pulling
// out the inner jar for hashing, so a zip bomb cannot exhaust memory.
const maxInnerJarBytes = 512 << 20

// FirstInnerJar returns the bytes of the first .jar entry inside a Solder mod
// zip (a Solder mod zip holds the jar at mods/<file>.jar). It is used to
// compute the inner jar's sha1/sha512 for Modrinth hash-linking on import. ok
// is false when the zip is unreadable, has no .jar (e.g. a config or
// resourcepack bundle), or the jar exceeds the decompression cap - in which
// case the content stays an unlinked upload.
func FirstInnerJar(zipBytes []byte) (name string, data []byte, ok bool) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return "", nil, false
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Name), ".jar") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", nil, false
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxInnerJarBytes+1))
		rc.Close()
		if err != nil || int64(len(b)) > maxInnerJarBytes {
			return "", nil, false
		}
		name := path.Base(strings.ReplaceAll(f.Name, `\`, "/"))
		if name == "." || name == "/" || name == "" || strings.Contains(name, "..") {
			name = ""
		}
		return name, b, true
	}
	return "", nil, false
}

// HasUnsafeZipEntry reports whether any file entry in the zip has a path that,
// after normalizing backslashes, is absolute or escapes the archive root via a
// ".." segment. Such an archive must not be stored: the Solder render copies
// the bytes verbatim (to keep md5 stable), so a traversal-bearing entry name
// would zip-slip a downstream launcher. An unreadable zip is treated as unsafe.
func HasUnsafeZipEntry(zipBytes []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return true
	}
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if strings.HasPrefix(name, "/") {
			return true
		}
		for _, seg := range strings.Split(name, "/") {
			if seg == ".." {
				return true
			}
		}
	}
	return false
}
