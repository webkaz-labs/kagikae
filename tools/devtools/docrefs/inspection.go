package docrefs

// Files returns the same pruned input set used by reference extraction.
func Files(root string) ([]string, error) { return docFiles(root) }

// CitationText applies the extractor's fence and wrapping rules.
func CitationText(text string) string      { return unwrap(stripFences(text)) }
func DeclaredNames(text string) [][]string { return sectionNames(text) }
func Headings(text string) [][]string      { return headingNames(stripFences(text)) }
