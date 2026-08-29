package assess

import "unicode/utf8"

func utf8ValidString(s string) bool { return utf8.ValidString(s) }
