package modpack

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
)

// WrapJarAsSolderZip packages a single mod jar into a Solder-format zip whose
// contents extract into the .minecraft root: the jar lands at mods/<fileName>.
// The launcher downloads this zip, verifies its md5, and extracts it as-is.
func WrapJarAsSolderZip(fileName string, jar []byte) ([]byte, error) {
	if fileName == "" {
		return nil, fmt.Errorf("wrap: empty file name")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("mods/" + fileName)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(jar); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Hashes returns hex md5 (Solder), sha1 + sha512 (Modrinth) over the bytes.
func Hashes(data []byte) (md5hex, sha1hex, sha512hex string) {
	m := md5.Sum(data)
	s1 := sha1.Sum(data)
	s5 := sha512.Sum512(data)
	return hex.EncodeToString(m[:]), hex.EncodeToString(s1[:]), hex.EncodeToString(s5[:])
}
