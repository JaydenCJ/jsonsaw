// In-process integration tests for the CLI: real flags, real files in
// t.TempDir(), real exit codes — everything a shell user would observe,
// without building a binary or touching the network.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/jsonsaw/internal/version"
)

// run invokes the CLI with stdin content and returns (exit, stdout, stderr).
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb strings.Builder
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// write drops content into a fresh temp file and returns its path.
func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sample = `{"meta":{"page":1},"data":{"users":[{"id":1,"name":"Ann"},{"id":2,"name":"Ben"},{"id":3,"name":"Cy"}]}}`

func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, out, _ := run(t, "", arg)
		if code != ExitOK || out != "jsonsaw "+version.Version+"\n" {
			t.Fatalf("%s: code %d out %q", arg, code, out)
		}
	}
}

func TestHelpAndBareInvocation(t *testing.T) {
	code, out, _ := run(t, "", "help")
	if code != ExitOK || !strings.Contains(out, "jsonsaw split") {
		t.Fatalf("help: code %d out %q", code, out)
	}
	code, out, _ = run(t, "")
	if code != ExitOK || !strings.Contains(out, "Usage:") {
		t.Fatalf("bare: code %d", code)
	}
}

func TestUnknownCommandExitsUsage(t *testing.T) {
	code, _, errb := run(t, "", "frobnicate")
	if code != ExitUsage || !strings.Contains(errb, "unknown command") {
		t.Fatalf("code %d stderr %q", code, errb)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	// Each of these must fail fast — before reading any input — with
	// exit 2 and a message that names the problem.
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"split", "--bogus"}, "flag provided but not defined"},
		{[]string{"split", "--chunk", "2"}, "--chunk requires --out"},
		{[]string{"split", "--path", "a..b"}, "invalid path"},
		{[]string{"split", "--skip", "-1"}, "--skip must be"},
		{[]string{"join", "--path", "a[0]"}, "object keys only"},
		{[]string{"join", "--indent", "0"}, "--indent must be"},
		{[]string{"paths", "--format", "yaml"}, "unknown --format"},
		{[]string{"count", "a.json", "b.json"}, "at most one input file"},
	}
	for _, c := range cases {
		code, _, errb := run(t, `[1]`, c.args...)
		if code != ExitUsage || !strings.Contains(errb, c.want) {
			t.Fatalf("%v: code %d stderr %q (want %q)", c.args, code, errb, c.want)
		}
	}
}

func TestSplitStdinToStdout(t *testing.T) {
	code, out, errb := run(t, `[{"a":1},{"b":2}]`, "split")
	if code != ExitOK {
		t.Fatalf("code %d stderr %q", code, errb)
	}
	if out != "{\"a\":1}\n{\"b\":2}\n" {
		t.Fatalf("out %q", out)
	}
	if !strings.Contains(errb, "jsonsaw split: 2 elements written") {
		t.Fatalf("stderr %q", errb)
	}
	// --quiet suppresses the summary so pipelines stay silent on success.
	code, _, errb = run(t, `[1]`, "split", "--quiet")
	if code != ExitOK || errb != "" {
		t.Fatalf("quiet: code %d stderr %q", code, errb)
	}
}

func TestSplitWithPathFromFile(t *testing.T) {
	f := write(t, "export.json", sample)
	code, out, _ := run(t, "", "split", "--path", "data.users", f)
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if out != "{\"id\":1,\"name\":\"Ann\"}\n{\"id\":2,\"name\":\"Ben\"}\n{\"id\":3,\"name\":\"Cy\"}\n" {
		t.Fatalf("out %q", out)
	}
}

func TestSplitSkipAndLimitFlags(t *testing.T) {
	code, out, _ := run(t, `[1,2,3,4,5]`, "split", "--skip", "1", "--limit", "2")
	if code != ExitOK || out != "2\n3\n" {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestSplitChunkedToDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "parts")
	code, _, errb := run(t, `[1,2,3,4,5]`, "split", "--chunk", "2", "--out", dir)
	if code != ExitOK {
		t.Fatalf("code %d stderr %q", code, errb)
	}
	if !strings.Contains(errb, "5 elements written across 3 part files") {
		t.Fatalf("stderr %q", errb)
	}
	for i, want := range []string{"1\n2\n", "3\n4\n", "5\n"} {
		name := filepath.Join(dir, "part-0000"+string(rune('0'+i))+".jsonl")
		data, err := os.ReadFile(name)
		if err != nil || string(data) != want {
			t.Fatalf("part %d: %q err %v", i, data, err)
		}
	}
	// A custom --prefix names the parts.
	dir2 := filepath.Join(t.TempDir(), "parts2")
	code, _, _ = run(t, `[1]`, "split", "--chunk", "10", "--prefix", "users-", "--out", dir2)
	if code != ExitOK {
		t.Fatalf("prefix: code %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir2, "users-00000.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestSplitMissingFileExitsRuntime(t *testing.T) {
	code, _, _ := run(t, "", "split", filepath.Join(t.TempDir(), "nope.json"))
	if code != ExitRuntime {
		t.Fatalf("code %d", code)
	}
}

func TestDataErrorsExitOne(t *testing.T) {
	code, _, errb := run(t, `[1,2`, "split")
	if code != ExitData || !strings.Contains(errb, "unexpected end of input") {
		t.Fatalf("bad json: code %d stderr %q", code, errb)
	}
	code, _, errb = run(t, `{"a":1}`, "split", "--path", "missing")
	if code != ExitData || !strings.Contains(errb, `key "missing" not found`) {
		t.Fatalf("bad path: code %d stderr %q", code, errb)
	}
}

func TestJoinStdin(t *testing.T) {
	code, out, errb := run(t, "1\n2\n3\n", "join")
	if code != ExitOK || out != "[1,2,3]\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, errb)
	}
	if !strings.Contains(errb, "jsonsaw join: 3 elements from 1 source") {
		t.Fatalf("stderr %q", errb)
	}
}

func TestSummariesUseSingularForOne(t *testing.T) {
	// "1 elements written" is exactly the kind of rough edge a summary
	// line must never have; lock the singular forms in.
	code, _, errb := run(t, `[42]`, "split")
	if code != ExitOK || !strings.Contains(errb, "jsonsaw split: 1 element written") {
		t.Fatalf("split: code %d stderr %q", code, errb)
	}
	dir := filepath.Join(t.TempDir(), "one")
	code, _, errb = run(t, `[42]`, "split", "--chunk", "5", "--out", dir)
	if code != ExitOK || !strings.Contains(errb, "1 element written across 1 part file") {
		t.Fatalf("chunk: code %d stderr %q", code, errb)
	}
	code, _, errb = run(t, "42\n", "join")
	if code != ExitOK || !strings.Contains(errb, "jsonsaw join: 1 element from 1 source") {
		t.Fatalf("join: code %d stderr %q", code, errb)
	}
}

func TestJoinMultipleFilesInArgumentOrder(t *testing.T) {
	a := write(t, "a.jsonl", "1\n2\n")
	b := write(t, "b.jsonl", "3\n")
	code, out, _ := run(t, "", "join", "--quiet", a, b)
	if code != ExitOK || out != "[1,2,3]\n" {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestJoinPrettyWithWrapPath(t *testing.T) {
	code, out, _ := run(t, "{\"id\":1}\n", "join", "--pretty", "--path", "data.users", "--quiet")
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	want := "{\n  \"data\": {\n    \"users\": [\n      {\n        \"id\": 1\n      }\n    ]\n  }\n}\n"
	if out != want {
		t.Fatalf("out:\n%q\nwant:\n%q", out, want)
	}
	// --indent widens the unit.
	code, out, _ = run(t, "1\n", "join", "--pretty", "--indent", "4", "--quiet")
	if code != ExitOK || out != "[\n    1\n]\n" {
		t.Fatalf("indent: code %d out %q", code, out)
	}
}

func TestJoinBadLineExitsDataWithSourceName(t *testing.T) {
	bad := write(t, "bad.jsonl", "1\n{oops}\n")
	code, _, errb := run(t, "", "join", bad)
	if code != ExitData || !strings.Contains(errb, "bad.jsonl") || !strings.Contains(errb, "line 2") {
		t.Fatalf("code %d stderr %q", code, errb)
	}
}

func TestOutFlagWritesFiles(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.jsonl")
	code, out, _ := run(t, `[1,2]`, "split", "--out", dst)
	if code != ExitOK || out != "" {
		t.Fatalf("split: code %d out %q", code, out)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "1\n2\n" {
		t.Fatalf("split file %q err %v", data, err)
	}
	dst2 := filepath.Join(dir, "joined.json")
	code, _, _ = run(t, "1\n", "join", "--quiet", "--out", dst2)
	if code != ExitOK {
		t.Fatalf("join: code %d", code)
	}
	data, err = os.ReadFile(dst2)
	if err != nil || string(data) != "[1]\n" {
		t.Fatalf("join file %q err %v", data, err)
	}
}

func TestCount(t *testing.T) {
	f := write(t, "export.json", sample)
	code, out, _ := run(t, "", "count", "--path", "data.users", f)
	if code != ExitOK || out != "3\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	// Counting a non-array is a data error, not a silent zero.
	code, _, errb := run(t, `{"a":1}`, "count", "--path", "a")
	if code != ExitData || !strings.Contains(errb, "expected an array to count") {
		t.Fatalf("code %d stderr %q", code, errb)
	}
}

func TestPathsTextOutput(t *testing.T) {
	code, out, _ := run(t, sample, "paths", "--depth", "3")
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{".data.users    array[3]", ".data.users[]  object{2}", ".meta.page     number"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out %q missing %q", out, want)
		}
	}
}

func TestPathsJSONOutput(t *testing.T) {
	code, out, _ := run(t, sample, "paths", "--depth", "2", "--format", "json")
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{`"path": ".data.users"`, `"kind": "array"`, `"count": 3`} {
		if !strings.Contains(out, want) {
			t.Fatalf("out %q missing %q", out, want)
		}
	}
}

func TestSplitJoinRoundTripThroughCLI(t *testing.T) {
	f := write(t, "export.json", sample)
	code, jsonl, _ := run(t, "", "split", "--path", "data.users", "--quiet", f)
	if code != ExitOK {
		t.Fatalf("split code %d", code)
	}
	code, rejoined, _ := run(t, jsonl, "join", "--path", "data.users", "--quiet")
	if code != ExitOK {
		t.Fatalf("join code %d", code)
	}
	// The wrapper is data.users only, so meta is (correctly) gone; the
	// array content itself must be byte-identical.
	want := `{"data":{"users":[{"id":1,"name":"Ann"},{"id":2,"name":"Ben"},{"id":3,"name":"Cy"}]}}` + "\n"
	if rejoined != want {
		t.Fatalf("got %q want %q", rejoined, want)
	}
}
