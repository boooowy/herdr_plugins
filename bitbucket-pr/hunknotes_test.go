package main

import "testing"

func TestSessionIDForPID(t *testing.T) {
	// Shape captured from hunk 0.17.0 `session list --json`.
	out := []byte(`{"sessions":[
	  {"sessionId":"4c14b153","pid":72567,"inputKind":"patch","sourceLabel":"pr-37.patch"},
	  {"sessionId":"beef","pid":100,"inputKind":"diff","sourceLabel":"working tree"}
	]}`)
	if id := sessionIDForPID(out, 72567); id != "4c14b153" {
		t.Errorf("id = %q", id)
	}
	if id := sessionIDForPID(out, 999); id != "" {
		t.Errorf("missing pid must return empty, got %q", id)
	}
	if id := sessionIDForPID([]byte(`not json`), 1); id != "" {
		t.Errorf("bad json must return empty, got %q", id)
	}
}

func TestParseHunkNotesArray(t *testing.T) {
	out := []byte(`[
	  {"noteId":"user:1","source":"user","filePath":"a.go","newRange":[10,12],"body":"note one"},
	  {"noteId":"agent:2","source":"agent","filePath":"a.go","newRange":[3,3],"body":"injected"},
	  {"noteId":"user:3","source":"user","filePath":"b.go","oldRange":[7,7],"body":"  "}
	]`)
	notes, err := parseHunkNotes(out)
	if err != nil {
		t.Fatal(err)
	}
	// agent note and blank body dropped.
	if len(notes) != 1 || notes[0].Body != "note one" {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestParseHunkNotesWrapped(t *testing.T) {
	out := []byte(`{"comments":[{"noteId":"user:1","source":"user","filePath":"a.go","newRange":[5,5],"body":"x"}]}`)
	notes, err := parseHunkNotes(out)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes = %+v, err = %v", notes, err)
	}
	if _, err := parseHunkNotes([]byte(`"nope"`)); err == nil {
		t.Error("unrecognized shape must error")
	}
}

func TestHunkNoteAnchor(t *testing.T) {
	n := hunkNote{FilePath: "a.go", NewRange: []int{10, 15}}
	in := n.anchor()
	if in == nil || in.To == nil || *in.To != 15 || in.From != nil {
		t.Errorf("new-range anchor = %+v", in)
	}
	if got := n.lineLabel(); got != "L10–15" {
		t.Errorf("label = %q", got)
	}

	o := hunkNote{FilePath: "b.go", OldRange: []int{7, 7}}
	in = o.anchor()
	if in == nil || in.From == nil || *in.From != 7 || in.To != nil {
		t.Errorf("old-range anchor = %+v", in)
	}
	if got := o.lineLabel(); got != "旧L7" {
		t.Errorf("label = %q", got)
	}

	if (hunkNote{FilePath: "c.go"}).anchor() != nil {
		t.Error("rangeless note must not anchor")
	}
}

func TestExpandCtxArg(t *testing.T) {
	argv := []string{"hunk", "patch", "{patch}", "--agent-context", "{ctx}"}
	out := expandCtxArg(argv, "/tmp/ctx.json")
	if out[4] != "/tmp/ctx.json" {
		t.Errorf("expanded = %v", out)
	}
	out = expandCtxArg(argv, "")
	want := []string{"hunk", "patch", "{patch}"}
	if len(out) != len(want) || out[2] != "{patch}" {
		t.Errorf("dropped = %v", out)
	}
	// Tools without the placeholder are untouched.
	plain := []string{"delta", "{patch}"}
	if out := expandCtxArg(plain, "/tmp/ctx.json"); len(out) != 2 {
		t.Errorf("plain = %v", out)
	}
}
