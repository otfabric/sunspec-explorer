// SPDX-License-Identifier: MIT

package api

import (
	"fmt"
	"testing"
)

// A Range header is a single range that names its first byte, or it is
// refused; what comes out of it is a range a reader can seek to.
func FuzzParseRange(f *testing.F) {
	for _, s := range []string{
		"", "bytes=0-", "bytes=5-10", "bytes= 5 - 10 ", "bytes=-5", "bytes=1-2,3-4", "bytes=10-5", "items=0-1", "bytes=",
		"bytes=9223372036854775807-", "bytes=9223372036854775808-", "bytes=-", "bytes=0-9223372036854775807", "bytes=+1-",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, header string) {
		start, end, ranged, ok := parseRange(header)
		if !ok {
			return
		}
		if ranged != (header != "") || start < 0 || (end != -1 && end < start) {
			t.Fatalf("parseRange(%q) = %d, %d, %v, %v", header, start, end, ranged, ok)
		}
		if !ranged {
			return
		}
		// Parsing what the range says gives the same range.
		again := fmt.Sprintf("bytes=%d-", start)
		if end >= 0 {
			again += fmt.Sprint(end)
		}
		if s, e, _, ok := parseRange(again); !ok || s != start || e != end {
			t.Fatalf("%q and %q differ", header, again)
		}
	})
}
