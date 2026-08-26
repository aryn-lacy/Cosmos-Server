package resources

import (
	"encoding/json"
	"os"
	"testing"
)

// TestServiceJSONEquivalentAgainstLiveExports validates the equivalence
// function against real data: planned service_json strings (what jsonencode()
// over the generated config produces) vs live /export responses for every
// service on the comos host. Pair file is produced by a helper script; skip
// silently when absent (e.g. in CI).
func TestServiceJSONEquivalentAgainstLiveExports(t *testing.T) {
	data, err := os.ReadFile("/tmp/svc_pairs.json")
	if err != nil {
		t.Skip("no pair file; skipping live-data test")
	}
	var pairs map[string][2]string
	if err := json.Unmarshal(data, &pairs); err != nil {
		t.Fatalf("bad pair file: %v", err)
	}
	if len(pairs) == 0 {
		t.Fatal("pair file empty")
	}
	for name, pair := range pairs {
		planned, exported := pair[0], pair[1]
		if !serviceJSONEquivalent(planned, exported) {
			t.Errorf("%s: planned config vs live export should be equivalent", name)
		}
		// Determinism
		if !serviceJSONEquivalent(exported, planned) {
			t.Errorf("%s: equivalence not symmetric", name)
		}
	}
}

func TestServiceJSONEquivalentCatchesRealDrift(t *testing.T) {
	base := `{"container_name":"x","image":"img:1","ports":["0.0.0.0:80:80/tcp"]}`
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", base, base, true},
		{"key order", `{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{"routes ignored", `{"a":1,"routes":[1,2]}`, `{"a":1}`, true},
		{"routes differs but ignored", `{"a":1,"routes":[1]}`, `{"a":1,"routes":[9,9]}`, true},
		{"mac_address ignored", `{"mac_address":"aa:bb"}`, `{"mac_address":"cc:dd"}`, true},
		{"null vs absent", `{"a":1,"devices":null}`, `{"a":1}`, true},
		{"volume order", `{"volumes":[{"Source":"/a","Target":"/b","Type":"bind"},{"Source":"/c","Target":"/d","Type":"volume"}]}`, `{"volumes":[{"Source":"/c","Target":"/d","Type":"volume"},{"Source":"/a","Target":"/b","Type":"bind"}]}`, true},
		{"real env drift", `{"environment":["A=1"]}`, `{"environment":["A=2"]}`, false},
		{"image drift", `{"image":"x:1"}`, `{"image":"x:2"}`, false},
		{"port drift", base, `{"container_name":"x","image":"img:1","ports":["0.0.0.0:81:80/tcp"]}`, false},
		{"missing key", `{"a":1}`, `{"a":1,"b":2}`, false},
		{"bad json a", `{not json`, `{}`, false},
		{"bad json b", `{}`, `{not json`, false},
		{"volume contents drift", `{"volumes":[{"Source":"/a","Type":"bind","Target":"/b"}]}`, `{"volumes":[{"Source":"/a","Type":"bind","Target":"/CHANGED"}]}`, false},
		{"volume count drift", `{"volumes":[{"Source":"/a","Type":"bind","Target":"/b"}]}`, `{"volumes":[]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceJSONEquivalent(tc.a, tc.b); got != tc.want {
				t.Errorf("serviceJSONEquivalent(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
