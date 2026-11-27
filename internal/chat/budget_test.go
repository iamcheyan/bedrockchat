package chat

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestTrimKeepsNewestCompleteSuffix(t *testing.T) {
	req := Request{Model: "qwen2.5:1.5b", Text: " abc日本éＡ ", System: "rules", MaxOutput: 1,
		Images:  []Image{{Data: []byte{1}, Format: "png", Width: 751, Height: 1, Name: "not text"}},
		History: []Pair{{"a", "b"}, {strings.Repeat("x", 90), "too big"}, {"new", "yes"}}}
	// 11 overhead + 9 current text + 10 system + 2 image + 18 retained turn.
	got, estimate, dropped, err := Trim(req, 50)
	if err != nil || estimate != 50 || dropped != 2 {
		t.Fatalf("estimate=%d, dropped=%d, error=%v", estimate, dropped, err)
	}
	want := req
	want.History = req.History[2:]
	if !reflect.DeepEqual(got, want) || len(req.History) != 3 {
		t.Fatalf("request not preserved except for the dropped turns: %+v", got)
	}
}

func TestTrimRejectsInvalidInput(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"model":            func(r *Request) { r.Model = "bad model" },
		"managed prompt":   func(r *Request) { r.Model = "arn:aws:bedrock:us-east-1:123:prompt/example" },
		"output":           func(r *Request) { r.MaxOutput = 0 },
		"empty":            func(r *Request) { r.Text = " \n　" },
		"image format":     func(r *Request) { r.Images = []Image{{Data: []byte{1}, Format: "svg", Width: 1, Height: 1}} },
		"image dimensions": func(r *Request) { r.Images = []Image{{Data: []byte{1}, Format: "png", Width: 0, Height: 1}} },
		"no bytes":         func(r *Request) { r.Images = []Image{{Format: "png", Width: 1, Height: 1}} },
		"history":          func(r *Request) { r.History = []Pair{{User: "missing reply"}} },
	} {
		t.Run(name, func(t *testing.T) {
			r := Request{Model: "model", Text: "abc", MaxOutput: 1}
			mutate(&r)
			if _, _, _, err := Trim(r, 100); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestTrimImageOnlyAndOverflow(t *testing.T) {
	r := Request{Model: "model", Text: "   ", MaxOutput: 1,
		Images: []Image{{Data: []byte{1}, Format: "jpeg", Width: 750, Height: 1}}}
	if _, estimate, _, err := Trim(r, 12); err != nil || estimate != 12 {
		t.Fatalf("estimate=%d, error=%v", estimate, err)
	}
	for _, budget := range []int{11, 0} {
		if _, _, _, err := Trim(r, budget); err == nil {
			t.Fatalf("accepted budget %d", budget)
		}
	}
	r.Images[0].Width, r.Images[0].Height = math.MaxInt, math.MaxInt
	if _, _, _, err := Trim(r, math.MaxInt); err == nil {
		t.Fatal("image cost overflow bypassed the budget")
	}
}
