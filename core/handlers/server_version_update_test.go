package handlers

import (
	"reflect"
	"testing"

	"dylaris-core/models"
)

func TestUnmanagedJars(t *testing.T) {
	listings := map[string][]dirEntry{
		"mods": {
			{Name: "sodium.jar", Size: 100},
			{Name: "handdropped.jar", Size: 200},
			{Name: "notes.txt", Size: 10},
			{Name: "somefolder", IsDir: true},
		},
		"plugins": {
			{Name: "worldedit.jar", Size: 300},
		},
	}

	tests := []struct {
		name          string
		known         []models.ServerMod
		installerType string
		want          []unmanagedFile
	}{
		{
			name:          "nothing installed means every jar is unmanaged",
			known:         nil,
			installerType: "fabric",
			want: []unmanagedFile{
				{Directory: "mods", Name: "sodium.jar", Size: 100},
				{Directory: "mods", Name: "handdropped.jar", Size: 200},
				{Directory: "plugins", Name: "worldedit.jar", Size: 300},
			},
		},
		{
			name: "an installed jar is claimed and drops out",
			known: []models.ServerMod{
				{FileName: "sodium.jar", TargetDir: "mods"},
			},
			installerType: "fabric",
			want: []unmanagedFile{
				{Directory: "mods", Name: "handdropped.jar", Size: 200},
				{Directory: "plugins", Name: "worldedit.jar", Size: 300},
			},
		},
		{
			// The trap this function exists to avoid: the row records "plugins",
			// the server was later re-declared as fabric, and deriving the
			// directory from the loader would look in "mods" and report the
			// plugin the panel itself installed as a stray file.
			name: "the recorded directory wins over the loader derivation",
			known: []models.ServerMod{
				{FileName: "worldedit.jar", TargetDir: "plugins"},
			},
			installerType: "fabric",
			want: []unmanagedFile{
				{Directory: "mods", Name: "sodium.jar", Size: 100},
				{Directory: "mods", Name: "handdropped.jar", Size: 200},
			},
		},
		{
			// Rows written before target_dir existed hold "", and those jars
			// were placed by the derived value, so the fallback is what they
			// need.
			name: "a row with no recorded directory falls back to the loader",
			known: []models.ServerMod{
				{FileName: "worldedit.jar", TargetDir: ""},
			},
			installerType: "paper",
			want: []unmanagedFile{
				{Directory: "mods", Name: "sodium.jar", Size: 100},
				{Directory: "mods", Name: "handdropped.jar", Size: 200},
			},
		},
		{
			// Same file name in both directories: claiming one must not silence
			// the other.
			name: "the claim is per directory, not per file name",
			known: []models.ServerMod{
				{FileName: "sodium.jar", TargetDir: "plugins"},
			},
			installerType: "fabric",
			want: []unmanagedFile{
				{Directory: "mods", Name: "sodium.jar", Size: 100},
				{Directory: "mods", Name: "handdropped.jar", Size: 200},
				{Directory: "plugins", Name: "worldedit.jar", Size: 300},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := unmanagedJars(tc.known, tc.installerType, listings)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUnmanagedJarsIgnoresNonJars(t *testing.T) {
	got := unmanagedJars(nil, "fabric", map[string][]dirEntry{
		"mods": {
			{Name: "config.yml"},
			{Name: "backup.jar.disabled"},
			{Name: "SODIUM.JAR"}, // case-insensitive suffix match
			{Name: "subdir", IsDir: true},
		},
	})
	want := []unmanagedFile{{Directory: "mods", Name: "SODIUM.JAR"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The empty result must serialise as [] rather than null, or the panel renders
// "no data" where it means "nothing unmanaged".
func TestUnmanagedJarsReturnsEmptySliceNotNil(t *testing.T) {
	got := unmanagedJars(nil, "fabric", map[string][]dirEntry{})
	if got == nil {
		t.Fatal("got nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}
