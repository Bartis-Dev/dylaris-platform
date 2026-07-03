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

// ReadZipEntry returns the bytes of the entry whose name equals innerPath (a
// content's TargetPath) inside a stored Solder zip, capped at maxBytes. ok is
// false when the zip is unreadable, has no such entry, or the entry exceeds
// maxBytes (checked against the declared uncompressed size first, so an
// oversized entry is never decompressed).
func ReadZipEntry(zipBytes []byte, innerPath string, maxBytes int64) (data []byte, ok bool) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, false
	}
	want := strings.ReplaceAll(innerPath, `\`, "/")
	for _, f := range zr.File {
		if strings.ReplaceAll(f.Name, `\`, "/") != want {
			continue
		}
		if f.UncompressedSize64 > uint64(maxBytes) {
			return nil, false
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
		rc.Close()
		if err != nil || int64(len(b)) > maxBytes {
			return nil, false
		}
		return b, true
	}
	return nil, false
}

// IsUnsafeEntryPath reports whether a single archive entry name, after
// normalizing backslashes, is absolute (leading "/" or a Windows drive letter)
// or escapes the archive root via a ".." segment. Used to reject an entry name
// before it is written into an archive a downstream launcher/operator extracts.
func IsUnsafeEntryPath(name string) bool {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") {
		return true
	}
	// Windows drive-letter path (e.g. "C:\evil" -> "C:/evil") is absolute on
	// the Windows launchers Technic targets; treat it as unsafe too.
	if len(name) >= 2 && name[1] == ':' &&
		((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) {
		return true
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
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
		if IsUnsafeEntryPath(f.Name) {
			return true
		}
	}
	return false
}
