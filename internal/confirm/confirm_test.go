package confirm

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptAcceptsY(t *testing.T) {
	out := new(bytes.Buffer)
	ok, err := Prompt(out, strings.NewReader("y\n"), "Delete it? [y/N]: ")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true for \"y\"")
	}
}

func TestPromptAcceptsYesAnyCase(t *testing.T) {
	cases := []string{"yes\n", "YES\n", "Yes\n", "Y\n"}
	for _, in := range cases {
		ok, err := Prompt(new(bytes.Buffer), strings.NewReader(in), "Delete it? [y/N]: ")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("expected true for %q", in)
		}
	}
}

func TestPromptRejectsEmptyInput(t *testing.T) {
	ok, err := Prompt(new(bytes.Buffer), strings.NewReader("\n"), "Delete it? [y/N]: ")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false for empty input (bare Enter)")
	}
}

func TestPromptRejectsNo(t *testing.T) {
	ok, err := Prompt(new(bytes.Buffer), strings.NewReader("n\n"), "Delete it? [y/N]: ")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false for \"n\"")
	}
}

func TestPromptRejectsGarbage(t *testing.T) {
	ok, err := Prompt(new(bytes.Buffer), strings.NewReader("maybe\n"), "Delete it? [y/N]: ")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false for garbage input")
	}
}

func TestPromptWritesPromptText(t *testing.T) {
	out := new(bytes.Buffer)
	if _, err := Prompt(out, strings.NewReader("n\n"), "Delete DNS record record-1 from example.com? [y/N]: "); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Delete DNS record record-1 from example.com? [y/N]: " {
		t.Fatalf("prompt text = %q", out.String())
	}
}

func TestPromptHandlesNoTrailingNewline(t *testing.T) {
	ok, err := Prompt(new(bytes.Buffer), strings.NewReader("y"), "Delete it? [y/N]: ")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true for \"y\" without trailing newline")
	}
}
