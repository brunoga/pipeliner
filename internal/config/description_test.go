package config

import "testing"

func TestPipelineDescription(t *testing.T) {
	cfg, err := ParseBytes([]byte(`
src = input("rss", url="https://example.com/rss")
out = output("print", upstream=src)
pipeline("a", schedule="1h", description="  Grabs ratio fillers.\nSecond line.  ")

src2 = input("rss", url="https://example.com/rss2")
out2 = output("print", upstream=src2)
pipeline("b", description="   ")
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.GraphDescriptions["a"]; got != "Grabs ratio fillers.\nSecond line." {
		t.Errorf("a: got %q", got)
	}
	if _, ok := cfg.GraphDescriptions["b"]; ok {
		t.Errorf("blank description must not be recorded: %+v", cfg.GraphDescriptions)
	}
}

func TestPipelineDescriptionMustBeString(t *testing.T) {
	_, err := ParseBytes([]byte(`
src = input("rss", url="https://example.com/rss")
out = output("print", upstream=src)
pipeline("a", description=3)
`))
	if err == nil {
		t.Fatal("want an error for a non-string description")
	}
}
