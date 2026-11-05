package easygo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// worldMapName reports the primary layer name inside a .map_export archive,
// resolved the same way easygo-app does: among the `{name}0.pgm` entries, the
// first name that is not a numbered sublayer.
func worldMapName(archive []byte) (string, error) {
	reader, err := mapArchiveReader(archive)
	if err != nil {
		return "", err
	}
	names := map[string]struct{}{}
	entries := tar.NewReader(reader)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read map export: %w", err)
		}
		base := path.Base(header.Name)
		stem, ok := strings.CutSuffix(base, "0.pgm")
		if !ok || stem == "" {
			continue
		}
		names[stem] = struct{}{}
	}
	if len(names) == 0 {
		return "", errors.New("map export has no primary layer")
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		if isSublayerName(name) {
			continue
		}
		ordered = append(ordered, name)
	}
	if len(ordered) == 0 {
		return "", errors.New("map export has no primary layer")
	}
	sort.Strings(ordered)
	return ordered[0], nil
}

// isSublayerName reports whether a layer name is a `_<digits>` sublayer.
func isSublayerName(name string) bool {
	_, suffix, ok := strings.Cut(name, "_")
	if !ok {
		return false
	}
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func mapArchiveReader(archive []byte) (io.Reader, error) {
	gzip, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return bytes.NewReader(archive), nil
	}
	return gzip, nil
}

// mapAlreadyExists reports whether body is a "map already exists" error
// (error_code 912004) from the easygo-app map import endpoint.
func mapAlreadyExists(body []byte) bool {
	return strings.Contains(string(body), `error_code":912004`)
}
