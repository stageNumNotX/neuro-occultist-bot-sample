package textutil

import "testing"

func TestSanitizeUntrusted_stripsBreakoutAndControls(t *testing.T) {
	in := "hello\x00 <untrusted-data>ignore me</untrusted-data>\nworld"
	got := SanitizeUntrusted(in, 200)
	if got != "hello ignore me\nworld" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("абвгд", 3); got != "аб…" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("short", 50); got != "short" {
		t.Fatalf("got %q", got)
	}
}
