package easygo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func mapExportArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWorldMapNameResolvesPrimaryLayer(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{
		"sros/map/arena_world0.pgm":  "pgm",
		"sros/map/arena_world1.pgm":  "pgm",
		"sros/map/zone_2_0.pgm":      "sublayer",
		"sros/map/arena_world.json":  "{}",
		"sros/map/arena_world_1.png": "png",
	})
	name, err := worldMapName(archive)
	if err != nil {
		t.Fatal(err)
	}
	if name != "arena_world" {
		t.Fatalf("name=%q", name)
	}
}

func TestWorldMapNameRejectsArchiveWithoutPrimaryLayer(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/thing.json": "{}"})
	if _, err := worldMapName(archive); err == nil {
		t.Fatal("expected an error for an archive without a primary layer")
	}
}
