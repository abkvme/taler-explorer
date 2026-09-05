package web

import (
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	reDataT  = regexp.MustCompile(`data-t="([^"]*)"`)
	reDataTX = regexp.MustCompile(`data-t-(?:placeholder|title|aria-label)="([^"]*)"`)
)

// TestI18nCoverage fails when a template asks for a string no catalog carries.
//
// A missing key is invisible at runtime: the i18n helper falls back to the
// English key, so the page renders correctly in English and stays English in
// every other language. That is how the peer Mode/API columns and the whole
// About page shipped untranslated.
func TestI18nCoverage(t *testing.T) {
	want := map[string]bool{}
	err := fs.WalkDir(embedded, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		b, err := fs.ReadFile(embedded, p)
		if err != nil {
			return err
		}
		for _, re := range []*regexp.Regexp{reDataT, reDataTX} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				// data-t values are HTML attributes, so "&" arrives escaped
				// while the catalog key is the plain text.
				want[strings.ReplaceAll(m[1], "&amp;", "&")] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	if len(want) == 0 {
		t.Fatal("no data-t keys found; the scan is broken, not the catalogs")
	}

	for _, lang := range []string{"ru", "uk", "be"} {
		b, err := fs.ReadFile(embedded, "static/i18n/"+lang+".json")
		if err != nil {
			t.Fatalf("read %s catalog: %v", lang, err)
		}
		var cat map[string]string
		if err := json.Unmarshal(b, &cat); err != nil {
			t.Fatalf("parse %s catalog: %v", lang, err)
		}
		var missing []string
		for k := range want {
			if v, ok := cat[k]; !ok || v == "" {
				missing = append(missing, k)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s.json is missing %d key(s): %q", lang, len(missing), missing)
		}
	}
}
