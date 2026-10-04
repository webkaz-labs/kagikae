package l10n

import "flag"

// FlagUsage returns a flag's description for a person: the catalog's Japanese
// when Japanese is selected and the catalog has the English description, else
// the description as registered. A description is not a format, so it is never
// passed through fmt; the catalog test checks every registered description as a
// key.
func FlagUsage(f *flag.Flag) string {
	if ja, ok := japanese(f.Usage); ok {
		return ja
	}
	return f.Usage
}
