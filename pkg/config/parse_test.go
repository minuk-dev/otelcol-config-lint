package config_test

import (
	"errors"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

const sample = `
receivers:
  otlp:
    protocols:
      grpc:
  otlp/internal:
extensions:
  zpages:
service:
  extensions: [zpages]
  pipelines:
    traces/primary:
      receivers: [otlp, otlp/internal]
      exporters: [debug]
`

func TestParseSections(t *testing.T) {
	t.Parallel()

	f, err := config.Parse("sample.yaml", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	sec := f.Sections[config.KindReceiver]
	if sec == nil || len(sec.Components) != 2 {
		t.Fatalf("want 2 receivers, got %+v", sec)
	}

	if got := sec.Components[1].ID; got.Type != "otlp" || got.Name != "internal" {
		t.Errorf("want otlp/internal, got %+v", got)
	}

	if _, ok := f.Component(config.KindExtension, config.ID{Type: "zpages"}); !ok {
		t.Error("zpages extension not found")
	}
}

func TestParsePipeline(t *testing.T) {
	t.Parallel()

	f, err := config.Parse("sample.yaml", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	if len(f.Service.Pipelines) != 1 {
		t.Fatalf("want 1 pipeline, got %d", len(f.Service.Pipelines))
	}

	p := f.Service.Pipelines[0]
	if p.Signal != config.SignalTraces || p.Name != "primary" {
		t.Errorf("want traces/primary, got signal=%q name=%q", p.Signal, p.Name)
	}

	if len(p.Receivers) != 2 || p.Receivers[1].ID.String() != "otlp/internal" {
		t.Errorf("unexpected receivers: %+v", p.Receivers)
	}

	if want := "service.pipelines.traces/primary.receivers[0]"; p.Receivers[0].Path != want {
		t.Errorf("want path %q, got %q", want, p.Receivers[0].Path)
	}

	if len(f.Service.Extensions) != 1 {
		t.Errorf("unexpected service extensions: %+v", f.Service.Extensions)
	}
}

func TestPositionsPointAtTheRightLine(t *testing.T) {
	t.Parallel()

	f, err := config.Parse("sample.yaml", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	// "otlp:" is on line 3 of the sample, which starts with a blank line.
	c, _ := f.Component(config.KindReceiver, config.ID{Type: "otlp"})
	if pos := f.Pos(c.KeyNode); pos.Line != 3 {
		t.Errorf("want line 3, got %+v", pos)
	}
}

func TestUnknownTopLevelKeysAreCollected(t *testing.T) {
	t.Parallel()

	f, err := config.Parse("x.yaml", []byte("receivers:\n  otlp:\nrecievers:\n  otlp:\n"))
	if err != nil {
		t.Fatal(err)
	}

	if len(f.Unknown) != 1 || f.Unknown[0].Key != "recievers" {
		t.Errorf("unexpected unknown keys: %+v", f.Unknown)
	}
}

func TestDuplicateKeysAreFoundAtAnyDepth(t *testing.T) {
	t.Parallel()

	src := `
receivers:
  otlp:
    protocols:
      grpc:
      grpc:
service:
  pipelines:
    traces:
      receivers: [otlp]
      receivers: [otlp]
`

	f, err := config.Parse("x.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	if len(f.DuplicateKeys) != 2 {
		t.Fatalf("want 2 duplicates, got %+v", f.DuplicateKeys)
	}
}

func TestSyntaxErrorCarriesAPosition(t *testing.T) {
	t.Parallel()

	_, err := config.Parse("bad.yaml", []byte("receivers:\n  otlp:\n   bad: [1, 2\n"))
	if err == nil {
		t.Fatal("want a syntax error")
	}

	var syn *config.SyntaxError

	ok := errors.As(err, &syn)
	if !ok {
		t.Fatalf("want *config.SyntaxError, got %T", err)
	}

	if syn.Line == 0 {
		t.Errorf("syntax error has no line: %v", syn)
	}

	if d := syn.Diagnostic(); d.Rule != "yaml-syntax" || d.Position.File != "bad.yaml" {
		t.Errorf("unexpected diagnostic: %+v", d)
	}
}

func TestEmptyDocumentIsNotAnError(t *testing.T) {
	t.Parallel()

	f, err := config.Parse("empty.yaml", nil)
	if err != nil {
		t.Fatalf("empty config should parse: %v", err)
	}

	if f.Root != nil || f.Service.Node != nil {
		t.Error("empty config should have no root or service")
	}
}

func TestParseID(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in        string
		typ, name string
	}{
		{"otlp", "otlp", ""},
		{"otlp/internal", "otlp", "internal"},
		{"otlp/a/b", "otlp", "a/b"},
	} {
		got := config.ParseID(tt.in)
		if got.Type != tt.typ || got.Name != tt.name {
			t.Errorf("ParseID(%q) = %+v", tt.in, got)
		}

		if got.String() != tt.in {
			t.Errorf("round trip of %q gave %q", tt.in, got.String())
		}
	}
}
