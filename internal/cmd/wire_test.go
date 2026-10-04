package cmd

// Reports hold their messages as l10n.Msg, which encodes to a string and
// deliberately has no UnmarshalText. A test that reads a --json report back
// decodes into these wire forms, which shadow the message fields with strings:
// the outer field wins over the embedded one of the same JSON name.

type statusReportWire struct {
	statusReport
	Tools []toolStatusWire `json:"tools"`
}

type toolStatusWire struct {
	toolStatus
	Warnings []string `json:"warnings"`
}

type doctorReportWire struct {
	doctorReport
	Checks []checkWire `json:"checks"`
}

type checkWire struct {
	Tool    string `json:"tool"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
