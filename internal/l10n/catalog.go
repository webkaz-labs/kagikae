package l10n

import "maps"

// catalogAreas is the Japanese catalog, kept per area rather than per source
// file: a message moves between files without moving between areas. Each area
// maps an English format string, byte for byte as its call site writes it, to the
// Japanese format. The catalog test (catalog_test.go) checks the keys against the
// call sites and the values against docs/L10N-JA.md's character rules.
var catalogAreas = map[string]map[string]string{
	"errors":   jaErrors,
	"warnings": jaWarnings,
	"reports":  jaReports,
	"help":     jaHelp,
	"other":    jaOther,
}

// catalog is the union of the areas. A key in two areas is a defect the catalog
// test reports; here the later area would silently win.
var catalog = mergeAreas(catalogAreas)

func mergeAreas(areas map[string]map[string]string) map[string]string {
	merged := map[string]string{}
	for _, area := range areas {
		maps.Copy(merged, area)
	}
	return merged
}

// lookup returns the Japanese format for an English one.
func lookup(format string) (string, bool) {
	ja, ok := catalog[format]
	return ja, ok
}

// UseCatalogForTest replaces the catalog with entries until restore is called.
// It exists so a test outside this package can render a Japanese message before
// the catalog holds one; production code never calls it. Like Set, it is
// process-wide, so a test using it must not run in parallel.
func UseCatalogForTest(entries map[string]string) (restore func()) {
	saved := catalog
	catalog = maps.Clone(entries)
	return func() { catalog = saved }
}
