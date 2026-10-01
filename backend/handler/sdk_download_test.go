package handler

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
	"testing/fstest"
)

func testSdkFS() fstest.MapFS {
	return fstest.MapFS{
		"php/AuthProLicense.php": &fstest.MapFile{Data: []byte("<?php // sdk"), Mode: 0644},
		"php/example.php":        &fstest.MapFile{Data: []byte("<?php // example"), Mode: 0644},
		"php/README.md":          &fstest.MapFile{Data: []byte("# readme"), Mode: 0644},
	}
}

func TestBuildSdkZip(t *testing.T) {
	payload, filename, err := buildSdkZip(testSdkFS(), "PHP")
	if err != nil {
		t.Fatalf("buildSdkZip failed: %v", err)
	}
	if filename != "auth-pro-php-sdk.zip" {
		t.Fatalf("unexpected filename: %s", filename)
	}

	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("zip.NewReader failed: %v", err)
	}
	want := map[string]string{
		"auth-pro-php-sdk/AuthProLicense.php": "<?php // sdk",
		"auth-pro-php-sdk/example.php":        "<?php // example",
		"auth-pro-php-sdk/README.md":          "# readme",
	}
	if len(reader.File) != len(want) {
		t.Fatalf("zip entry count = %d, want %d", len(reader.File), len(want))
	}
	for _, entry := range reader.File {
		content, ok := want[entry.Name]
		if !ok {
			t.Fatalf("unexpected zip entry: %s", entry.Name)
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatalf("open entry %s failed: %v", entry.Name, err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatalf("read entry %s failed: %v", entry.Name, err)
		}
		if string(data) != content {
			t.Fatalf("entry %s content = %q, want %q", entry.Name, data, content)
		}
	}
}

func TestBuildSdkZipRejectsInvalidLang(t *testing.T) {
	fsys := testSdkFS()
	for _, lang := range []string{"", "../sdk", "php/../", "php.php", "php sdk", "java"} {
		if _, _, err := buildSdkZip(fsys, lang); err == nil {
			t.Fatalf("buildSdkZip(%q) should fail", lang)
		}
	}
}
