// Tests for the shape reporter behind `jsonsaw paths` and the counter
// behind `jsonsaw count`. The depth budget must limit reporting, never
// accuracy: counts stay exact below the cutoff.
package inspect

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/jsonsaw/internal/jpath"
	"github.com/JaydenCJ/jsonsaw/internal/stream"
	"github.com/JaydenCJ/jsonsaw/internal/token"
)

func inspectAll(t *testing.T, input string, depth int) []Entry {
	t.Helper()
	entries, err := Inspect(strings.NewReader(input), depth)
	if err != nil {
		t.Fatalf("Inspect(%q): %v", input, err)
	}
	return entries
}

// labels flattens entries into "path kind" strings for compact assertions.
func labels(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Path+" "+e.Label())
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestInspectScalarRootAndFlatObject(t *testing.T) {
	got := labels(inspectAll(t, `42`, 2))
	if !eq(got, []string{". number"}) {
		t.Fatalf("got %v", got)
	}
	got = labels(inspectAll(t, `{"a":1,"b":"x","c":true,"d":null}`, 2))
	want := []string{". object{4}", ".a number", ".b string", ".c boolean", ".d null"}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInspectArraySamplesFirstElementOnly(t *testing.T) {
	got := labels(inspectAll(t, `{"users":[{"id":1},{"id":2},{"id":3}]}`, 3))
	want := []string{". object{1}", ".users array[3]", ".users[] object{1}", ".users[].id number"}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInspectDepthLimitsReportingNotCounts(t *testing.T) {
	// At depth 1 the users array's children are not reported, but its
	// element count must still be exact — that requires traversal.
	got := labels(inspectAll(t, `{"users":[{"id":1},{"id":2},{"id":3}]}`, 1))
	want := []string{". object{1}", ".users array[3]"}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Depth 0 reports only the root, again with an exact key count.
	got = labels(inspectAll(t, `{"a":{"b":1}}`, 0))
	if !eq(got, []string{". object{1}"}) {
		t.Fatalf("got %v", got)
	}
}

func TestInspectEmptyContainers(t *testing.T) {
	got := labels(inspectAll(t, `{"a":[],"b":{}}`, 2))
	want := []string{". object{2}", ".a array[0]", ".b object{0}"}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInspectQuotesAwkwardKeys(t *testing.T) {
	got := labels(inspectAll(t, `{"weird.key":1,"with space":2}`, 1))
	want := []string{`. object{2}`, `."weird.key" number`, `."with space" number`}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInspectPathsRoundTripThroughParser(t *testing.T) {
	// Every reported path (minus the [] sampler suffix) must parse, so
	// users can paste it straight into --path.
	entries := inspectAll(t, `{"data":{"weird.key":{"list":[1]}},"n":1}`, 3)
	for _, e := range entries {
		p := strings.ReplaceAll(e.Path, "[]", "")
		if _, err := jpath.Parse(p); err != nil {
			t.Fatalf("reported path %q does not parse: %v", e.Path, err)
		}
	}
}

func TestInspectValidatesWholeDocument(t *testing.T) {
	if _, err := Inspect(strings.NewReader(`{"a":1,`), 2); err == nil {
		t.Fatal("truncated document accepted")
	}
}

func TestCountArray(t *testing.T) {
	// Nested arrays count as one element each; empty arrays count to zero.
	for input, want := range map[string]int{
		`[{"a":[1,2,3]},2,[],"x"]`: 4,
		`[]`:                       0,
	} {
		tz := token.New(strings.NewReader(input))
		first, _ := tz.Next()
		n, err := Count(tz, first, ".")
		if err != nil || n != want {
			t.Fatalf("Count(%s): got %d, %v", input, n, err)
		}
	}
}

func TestCountNonArrayFails(t *testing.T) {
	tz := token.New(strings.NewReader(`{"a":1}`))
	first, _ := tz.Next()
	_, err := Count(tz, first, "somewhere")
	pe, ok := err.(*stream.PathError)
	if !ok || !strings.Contains(pe.Msg, "expected an array to count, found object") {
		t.Fatalf("got %v", err)
	}
}
