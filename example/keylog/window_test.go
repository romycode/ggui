package main

import (
	"testing"

	"github.com/romycode/ggui/keyboard"
)

func TestSymLabelRecognizesFunctionKey(t *testing.T) {
	if got, want := symLabel(keyboard.Keysym(0xffbe)), "F1(0x00ffbe)"; got != want {
		t.Fatalf("symLabel(F1) = %q, want %q", got, want)
	}
	if got := symRune(keyboard.Keysym(0xffbe)); got != "-" {
		t.Fatalf("symRune(F1) = %q, want non-printable marker", got)
	}
}
