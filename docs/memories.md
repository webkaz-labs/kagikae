# Working preferences

For this repository's maintenance and verification programs, choose Go by
default and look for reusable Go helpers before introducing another interpreter.
Keep shell for bootstrap glue and for fixtures whose subject is shell behavior.
The picker's PTY suite ([VALIDATION.md](VALIDATION.md) § Picker PTY suite) is
JavaScript because the Go CLI standard drives built-binary PTY tests through
Microsoft `tui-test`'s Node binding; other verification programs follow the
default above.
