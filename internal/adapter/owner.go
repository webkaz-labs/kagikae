package adapter

// OwnerComparer is implemented by a tool whose credential payload names the
// account it belongs to, so that two payloads can be compared for their owner
// without an identity-only artifact (codex). Both recaptures ask it whether the
// live login is the one the snapshot it would overwrite holds
// (docs/ARCHITECTURE.md § Adapter Interface; what codex compares is
// docs/ADAPTERS.md § Recapture attribution).
type OwnerComparer interface {
	// CompareOwner answers whether recorded and live, two credential payloads
	// from either store, belong to the same account. It reports no owner value,
	// so nothing in it needs redacting.
	CompareOwner(recorded, live []byte) OwnerVerdict
}

// OwnerVerdict is CompareOwner's answer. The zero value is OwnerUnknown, which
// changes no decision a caller would otherwise take.
type OwnerVerdict int

const (
	// OwnerUnknown: either payload is not a shape the tool can judge, or the
	// values it compares are not readable on both sides.
	OwnerUnknown OwnerVerdict = iota
	// OwnerSame: the values compared name the same account on both sides.
	OwnerSame
	// OwnerDifferent: a value compared names different accounts on the two sides.
	OwnerDifferent
)
